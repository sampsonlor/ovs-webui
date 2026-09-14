# 正式 REST v1 与请求恢复

日期：2026-09-14。工程任务 #33，审阅 [PR #59](https://github.com/sampsonlor/ovs-webui/pull/59)。前置 #32 / PR #58 已接受合并至 `7117620`，接受标签 `phase1-sqlite-repositories-v0.1`。本项等待评审接受，不等于产品 GA 或 Scope 原文批准。

## 交付范围与接线

`ovs-webd` 现在把 `/api/*` 交给 Go 的共享 publicapi Handler。`/api/v1/openapi.json`、`/contract` 和 `/runtime` 可用于发现契约与服务状态；`/healthz`、`/readyz` 保留 bootstrap 运维语义。运行态异常在正式 API 使用 `application/problem+json`。

契约包括 **113 个路径、130 个操作、58 个 Scope ID、全部 53 个批准 IA 页面**。[覆盖清单](../../contracts/v1.coverage.json)逐项列出对应路径、操作及业务交付责任。覆盖代表已定义公共传输入口；页上的真实查询、执行器和完整 UI 仍按 #34–#55 实现。通用观察资源允许后续增加类型化证据字段；客户端必须保留未知字段与状态。

生产接线使用 `ClosedAuthority`，没有测试身份、临时登录或通用 privileged RPC。受保护入口无凭据返回 401；有凭据但尚未接入认证返回 503。`authentication_ready`、`configuration_ready` 保持 false。`AuthorityGateway` 是业务所属进程的接口：management 写入必须由 mgrd 在 manager.db 内授权接收，不能把 webd 的传输校验当成执行权。后续 #34/#37–#40 接入编译期登记的具体 IPC 操作。

| 文件 / 模块 | 用途 |
| --- | --- |
| `contracts/public-v1.mjs` | 审阅过的初始提案加完整公开目录、输入 schema、操作权限与交付责任 |
| `contracts/v1.openapi.json` | 当前 OpenAPI 3.1.1；公开 `/openapi.json` 与之保持一致 |
| `contracts/releases/v1.0.0.openapi.json` | v1.0.0 评审候选基线；接受并合入 main 后不可改写 |
| `internal/apicontract` | 编译内嵌 JSON Schema，校验参数、输入、响应；不接受客户端 schema URL |
| `internal/apidto`、`clients/typescript` | 生成 DTO；未知字段保留，联合类型保持 JSON；恢复与乱序保护工具 |
| `internal/repository/requests` | 双域持久幂等、原子接收、按原身份恢复及有界保留 |
| `internal/publicapi` | HTTP admission、Problem、Origin/CSRF、游标和 WebSocket 提示流 |

## 身份与命令边界

管理 ID 与 instance generation 使用 UUIDv4，request_id 使用带时间的 UUIDv7。Bridge → Port → Interface 保留独立绑定；Bond 是包含成员 Interface 的 Port。名称不能取代稳定 ID。Revision 是 opaque 字符串，客户端不推算其大小或下一值。

Bridge、Port、Interface、VLAN、Bond、STP、镜像、QoS、隔离、遥测等交换配置通过 `/candidate` 的类型化 intent，随后走 Validation、Transaction 与证据资源。没有按对象直接 live-save 的接口。DPDK/Offload 保持 Observe；OpenFlow 写入目录记录 #53 的 Expert、当前管理权及验证前置条件，业务 gate 尚未启用。Standard/Expert 不授予权限。

请求对象拒绝未知字段、重复键、无效 Unicode、非 JSON 媒体、非规范路径、重复参数和超预算输入。响应先按同一契约校验再发送；无效服务输出转换为 Problem，不向客户端暴露原始内部错误。

需版本条件的写操作必须提供单一强 `If-Match: "revision"`；缺失为 428，弱 ETag、`*`、多值均拒绝。Handler 检查格式；所属业务必须在 SQL 接收事务内比较当前资源版本，冲突返回 412。原前置条件也属于幂等指纹，重放不能偷偷换成新 ETag。DTO 的 `revision` 提供响应 ETag，客户端据此准备下一次独立操作。

## 持久接收与结果恢复

新增 web/manager migration **003**。两库各有独立 `api_authority` 与 `api_receipts`；普通重启保留 epoch，恢复后的 epoch 轮换由 #48 的恢复编排负责。原 001/002 不改写；升级继续使用 #32 的原子迁移、升级前一致性备份与 checksum 检查。

幂等键为 `(principal_id, request_epoch, request_domain, request_id)`；指纹绑定 operation、HTTP method、规范路径/查询、原 If-Match 及类型化 JSON。对象键顺序和等价十进制拼写不改变指纹；大整数不经过 float64。workspace 写入只进入 web.db，management 写入只进入 manager.db。

每次执行与重放必须复核当前授权，包括原操作和资源。Repository 在写事务前、事务内均调用授权回调；Find 在返回回执前把原操作/资源交给授权方。业务拒绝会回滚 SQL，不把 403/409/412 当成数据库损坏。

先查已有回执，再检查新键时效。因此响应丢失或普通重启后仍能拿到原回执；同键更换资源、参数、方法或前置条件返回 409。无记录的新键最多早于服务器 5 分钟、最多晚于服务器 2 分钟；历史 epoch 不接收新命令。持久时钟高水位检测超过 2 分钟的回退，拒绝新写为 `CLOCK_UNSAFE`，已有回执仍可恢复。

变更 SQL 与回执在同一事务提交。202 必须包含已在该事务中存入 manager.db 的 Job，返回原 `request_id`、域、epoch、job_id、resource_ref 和 correlation_id。回执表不会把 secret 明文或无密钥密码摘要落盘：敏感请求要求所属 SecretStore 提供至少 32 字节 HMAC key；一次性 secret 只在首次响应返回，重放返回不含 secret 的回执。

已完成回执保留至少 30 天，未完成/未解决回执不按 TTL 删除。每域最多 100,000 条；达到上限时新接收返回 429，不能删除不确定证据以腾出空间。Prune 与正常接受都遵守单写者存储预算。完整异步 Job 状态变迁继续由 #37/#39 提供，不把 API receipt 的 `completed` 解读为 OVS Applied。

客户端在发送前保存原三元组，失联后查询：

```http
GET /api/v1/requests/{original_request_id}?domain=management&epoch={original_epoch}
```

没有自动生成新键或自动 POST 的恢复函数。404、410、超时、commit unknown 均不能证明 Not Applied。`RESULT_UNKNOWN` 仍应追踪原请求。这里解决 client→管理平面接收幂等；mgrd→OVSDB 的 OutcomeUnknown、Applied evidence 和 reconciliation 是 #39/#40 的另一层责任。

## 游标、权限变化与通知

分页默认 100、上限 500。游标为 HMAC 签名的有界 token，绑定 principal、权限版本、operation、过滤条件、排序、generation、snapshot、last key 与到期时间，最长 30 秒；无 offset 参数。任何绑定变化、篡改、过期或进程重启均返回 410，客户端重新取得 REST 快照。分页业务负责按这个范围读取同一快照，不允许混合分页代次。

`/api/v1/stream` 使用 `ovs.v1` subprotocol，首帧订阅 1–32 个资源，可携带旧 stream cursor。每条连接使用新 stream_id 与 uint64 十进制 sequence；初始发送 `resync.required`，不声称恢复旧流连续性。只推送资源引用/版本提示与 heartbeat，REST 是状态、Job 和 Audit 权威；订阅后的应用消息会被拒绝，不能将连接当 RPC。

浏览器写入与 WebSocket 校验固定 HTTPS Origin；Cookie 写入另需 CSRF token。部署配置 `--public-origin https://实际主机:端口`；未配置则拒绝带 Origin 的请求，不猜测代理转发头。Authorization 和 Cookie 同时出现时拒绝歧义。每次发送通知（含 heartbeat/resync）都复核当前权限与资源可见性，撤权或权限版本变化后关闭连接。

| 预算 | 实现行为 |
| --- | --- |
| HTTP body 1 MiB，处理 5 秒 | 拒绝超限，不排无界队列；连接层维持已有 64 连接上限 |
| read 8 / control 4 / heavy 2 / auth 4 | 独立 admission；大操作不会占用确认/恢复控制槽 |
| WS 全局 16 / 每 principal 4 | 握手完成释放 REST 槽；首订阅 8 KiB、3 秒 |
| 每 WS 256 条或 1 MiB | 超限发 resync.required 并关闭；不缓冲权威事务数据 |
| counter 1 秒合并 | 同资源只保留当前窗口最新提示 |
| WS 写出 2 秒、heartbeat 5 秒 | 慢接收方有界断开；服务退出主动关闭被升级的连接 |

TS `StreamTracker` 识别乱序、重复、gap、新 stream 及未知提示；`ResponseFence` 阻止旧账户/权限/刷新请求的迟到响应覆盖当前页面；未知状态不会开启写动作。这些工具供 #41 的正式 Svelte 接入使用，当前合成原型 API 保留自己的版本与测试。

## 兼容性与验证

`pnpm contracts:generate` 同步规范、内嵌资源及 DTO，要求固定 Go 工具链提供 gofmt。`pnpm contracts:check` 校验生成结果，并和 v1.0.0 比较。检查器采用保守规则，允许新路径/方法、可选字段及联合类型分支，拒绝移除、改类型/约束/必填项/权限和域；无法证明兼容的变更需另立版本。CI 从 PR base commit 读取已经发布的 releases 文件，逐字节阻止同时改 baseline 来隐藏破坏性变更。

本地已验证 Go HTTP/WebSocket/DTO/指纹测试，208 项回归、类型检查、lint 与 `pnpm build`。Windows 本地不会把跳过的 Linux 存储测试算作成功；原生 CI 执行 Linux amd64/arm64 的 vet、race、CGO-free daemon、真实 SQLite 重启/原子回执、磁盘满、systemd/Unix 权限及隔离 OVS 转发测试，并保留 Go JSON、smoke 和浏览器报告。

完整 CI 与长期执行证据将在本批审阅记录中关联；本项接受后推进 **#34：认证、Session/Token、用户与角色授权**。

参考：[OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)、[UUIDv7 RFC 9562](https://www.rfc-editor.org/rfc/rfc9562.html#section-5.7)、[jsonschema/v6](https://github.com/santhosh-tekuri/jsonschema)、[coder/websocket](https://github.com/coder/websocket)。
