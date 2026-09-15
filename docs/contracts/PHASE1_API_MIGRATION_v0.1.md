# Phase 1 公共 API 与 lab 迁移设计

日期：2026-09-09。状态：Proposed for Review，关联 #30；#33 负责将接受的设计实现并冻结首个发布契约。

正式 API 使用 `/api/v1`、OpenAPI 3.1.1、UTF-8 JSON 和 snake_case。本文及[首切片 OpenAPI 草案](../../contracts/proposals/phase1-v1.openapi.json)供契约审阅，**没有接管正在运行的 lab 路由**。全产品 API 清单由范围映射驱动，不能把本草案的首切片 endpoint 数量当作完整 Phase 1 API 覆盖。

## 当前契约如何迁移

`contracts/core-v0.1.mjs` 是现有 lab 的编辑源，生成 `openapi.v0.1.json` 和 TypeScript validator/types。它在文档中明确是未发布架构提案。本次保留其生成链、fixtures 和行为测试，不对旧数据原地转换，也不把 React 调用方直接接到新 schema。

| lab 约定 | 正式设计 | 迁移验收 |
| --- | --- | --- |
| camelCase、窄 `Id` 字符串 | snake_case，Management UUID、OVS UUID、generation 和各类 revision 分开 | 不用简单字段重命名脚本推断身份；显式 DTO adapter |
| 每次合成配置更新产生 generation | 同一 OVSDB 生命周期延续 generation；配置变化产生 config_revision | 旧 fixture ID 不迁入真实 identity registry |
| 单库合成用户/角色 | mgrd Local/TACACS+、Auth Grant、webd session 映射、独立双库 | 无默认测试用户、无 lab role selector、无浏览器自报能力 |
| 单条 VLAN、3 种可编辑 mode | 最多 32 条初始 typed intents；native-untagged、all-VLAN、Advanced QinQ 受独立 validator 约束 | 保留未知字段；首切片先完成 native modes，QinQ capability 不满足时明确只读，GA 另验收 |
| `NativeVlan` 无 cvlans | 原生观察包含 mode/tag/trunks/cvlans 及字段来源 | cvlans 不可被普通 VLAN 保存清空 |
| 节点统一 unresolved transaction 阻塞 | 只保护重叠字段/依赖/恢复域；OVSDB touched-field CAS | 不同字段可并发成功，同字段三方冲突 |
| `additionalProperties:false` 与闭合响应 enum | 请求严格；响应允许未知字段，状态是开放字符串并提供已知值清单 | 新 response field/enum 不使整个 UI 崩溃，未知值不变成允许操作 |
| 浏览器/fixture 的强制成功或 reconciliation result | mgrd 收集 commit、Applied、Health 和 rollback 证据 | 客户端不能写 outcome，Job completed 不等于应用成功 |
| lab 请求账本和恢复提示 | 两个明确 request domain、epoch、时效 key、持久 receipt/outbox | 应答丢失只查原请求；不创建第二个 OVSDB transaction |
| 局部 Event/Audit、会话 View | 有稳定 ID 的共享 Job/Event/Audit 和 REST 深链 | 刷新、换用户、权限撤销、旧响应隔离 |
| fixture SSE/轮询或页面状态 | 公共 REST 权威资源 + required WebSocket 增量提示 | WS gap/重连/慢客户端先 REST resync，再恢复提示流 |

正式 API v1 发布前这是一轮明确的未发布契约替换，不声称向 lab 提案二进制兼容。发布后保存不可改写的 OpenAPI baseline；同 major 不删除/改名/改类型/增加 mandatory input，也不让已声明错误或状态换含义。[OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)

## 首切片资源

所有 path 以下均省略 `/api/v1`。Cookie Session 与 scoped API Bearer 为两种独立认证机制；普通浏览器 session 所需 CSRF headers 不向 API token 客户端虚构 Cookie 状态。

| 方法和资源 | 权威与含义 | 前置条件 |
| --- | --- | --- |
| POST `/sessions`；GET `/session`；DELETE `/session` | mgrd 认证、webd 映射；不把 Auth Grant 返回浏览器 | 同源检查、Local/TACACS+ 限流；退出撤销映射/关联 grant |
| GET `/workspace` | 当前用户的 Candidate、latest validation/transaction、请求 epoch、安全 gate | 重新核对 mgrd grant；mgrd 不可用时显式 degraded |
| GET `/ports`、`/ports/{port_id}` | 管理 ID 聚合对象；保留 Bridge→Port→Interface bindings | 对象/字段权限和 snapshot/generation 绑定 |
| GET `/candidate`；PATCH `/candidate` | server-persisted 私有草稿；命令为 stage/remove/rebase/discard | strong If-Match；workspace-domain request；不写 OVS |
| POST `/validations`；GET `/validations/{validation_id}` | mgrd 不可变 Diff/Validation + Job | candidate revision、对象 binding、policy/权限、真实依赖 |
| POST `/transactions`；GET `/transactions/{transaction_id}` | mgrd journal 和已知/未知结果 | 有效 Validation；所有风险/身份/字段条件再次校验 |
| POST `/transactions/{transaction_id}/decisions` | confirm 或 rollback 的可恢复命令 | expected_sequence；当前 deadline、权限和 evidence |
| POST `/transactions/{transaction_id}/reconciliations` | 原事务的只读因果核对 Job | 仅 request identity；不携带期待 outcome |
| GET `/requests/{request_id}?domain=…` | workspace 或 management domain 的原始 receipt | 当前主体/epoch；不可用、404、410 都不是 Not Applied 证明 |
| GET `/jobs/{job_id}` | 持久 Job，REST 权威 | owner/operation capability；WS 状态不能代替 |
| GET `/events`、`/audit` | 不同资源、不同权限、同 correlation 深链 | 有界 cursor，Audit 不可编辑，中央脱敏 |

后续 #33 扩展 Bridges/Interfaces、各原生配置、安全账户、诊断、backup/lifecycle/search 等正式 endpoint。既有功能主单和服务责任在[范围映射](../implementation/PHASE1_SCOPE_TRACEABILITY_v0.1.md)逐页关联，不新增只供 UI 用的管理后门。

## 请求接收和幂等

除建立/退出浏览器会话外，每次新写命令使用 UUIDv7 request_id，`Idempotency-Key` 必须相等。会话建立受 Origin/限流控制，退出对当前会话幂等；二者不创建配置事务或执行账本。UUIDv7 内有毫秒时间戳；本协议选择用它约束新请求年龄，UUID 自身不代表授权。[RFC 9562](https://www.rfc-editor.org/rfc/rfc9562.html#section-5.7)

所有保存草稿的命令属于 `workspace` request domain（web.db），验证/执行/决策/诊断/安全等特权操作属于 `management` domain（manager.db）。domain 由 endpoint registry 决定，调用方不能借 domain 切换权威。receipt 查询参数消除跨账本歧义。服务端 key 为 `(principal_id, request_epoch, domain, request_id)`，其值绑定 method、canonical path/query、typed payload hash 和原资源；同 key 改内容返回 409 IDEMPOTENCY_MISMATCH。

先查已存在 receipt，再判断新 key 时效。同 key 同命令返回同一个已持久结果/Job，不重执行；token 创建之类一次性秘密应答不能从 receipt 再显示 plaintext，只返回可识别的原 token metadata 与撤销入口。请求 hash 不包含 password、token、grant 或重新认证秘密；这些安全命令使用服务器计算的敏感输入 fingerprint/HMAC 和有限保留的内部记录，不能在日志暴露原文或可离线猜测的普通 hash。

未见过的 key 仅在创建时间距服务器不超过 5 分钟、未来不超过 2 分钟、`X-OVS-Request-Epoch` 匹配时接受。GET workspace/session 告知 server time 和 epoch。终态 receipt 保留 30 天；unresolved receipt/journal 永不被 TTL 删除。超过年龄的未见 key 返回 410 REQUEST_KEY_EXPIRED，即使原 receipt 已回收也不当作新写执行。额外有总量上限，容量不足拒绝新命令而不是丢弃活动保护记录。

epoch 是对应 authority 持久随机 ID；正常 daemon 重启不变，恢复管理备份或显式管理实例恢复时旋转并拒绝旧 epoch 的新入场。保留历史 epoch 用于授权后的只读查询。检测持久时间 high-watermark 后明显 wall-clock 倒退时，阻止新受时效保护的写入，返回 CLOCK_UNSAFE；现有 Safe Apply 继续用 boot/monotonic 证据恢复。

manager-domain 命令的 durable receipt 与 journal 在一笔 manager.db SQL transaction 中入场；webd 的转发 outbox 只记录交接。接受记录未落盘前不得发送 OVSDB transact。网络超时或 body 解码失败之后，客户端保留原 request/domain/epoch，读 receipt 和关联 resource，不自动再 POST。**API 幂等不能消除 mgrd→OVSDB 的 OutcomeUnknown**。

## 状态与错误

错误采用 `application/problem+json`，保留 `type/title/status/detail` 和稳定扩展 `code/correlation_id/request_id/request_domain/command_effect/resource_ref/details`。客户端按 code 和 evidence 决策，不能按中文 message、HTTP 状态或 Job success 推导配置结果。[RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html)

| HTTP / code | 后续 |
| --- | --- |
| 401 UNAUTHENTICATED；403 FORBIDDEN/REAUTH_REQUIRED | 重新登录/提权和读取上下文，不自动重发原命令 |
| 412 ETAG_MISMATCH；428 PRECONDITION_REQUIRED | 重读 Candidate，并保留用户本地意图供审阅 |
| 409 FIELD_CONFLICT / DEPENDENCY_CHANGED | 显示原始/当前/用户三方 Diff；重新验证 |
| 409 GENERATION_RECONCILIATION_REQUIRED | 停止旧对象/事务执行，进入身份核对 |
| 409 VALIDATION_EXPIRED / VALIDATION_BLOCKED | 新验证，历史 Diff 不替换 |
| 409 RESOURCE_PROTECTED | 指向已有重叠事务/保护域；无关操作仍可进行 |
| 409 DECISION_EXPIRED / TRANSACTION_VERSION_CHANGED | 读取原事务的权威状态，不延长窗口 |
| 409 IDEMPOTENCY_MISMATCH | 核对原请求；不能换 payload 重用 key |
| 410 CURSOR_EXPIRED / REQUEST_KEY_EXPIRED / REQUEST_EPOCH_CHANGED | 重新读取完整上下文；过期请求不能据此推断未执行 |
| 422 INVALID_INTENT / UNSUPPORTED_CONFIGURATION | 定位 typed 参数，保留未知配置，只开放已证明能力 |
| 429 RESOURCE_BUDGET_EXCEEDED | 可用 retry hint；不占用安全通道 |
| 503 PROVIDER_UNAVAILABLE / MANAGEMENT_DEGRADED / IPC_VERSION_MISMATCH / CLOCK_UNSAFE | 明确读取或执行缺口；已有 unknown 命令继续原请求恢复 |
| 404 NOT_FOUND（receipt） | 只有没有可给出的 receipt；不是 Not Applied |

Transaction 分开返回 phase、knowledge、commit_outcome、applied_outcome、safe_apply、health、sequence、generation、AllowedActions 和分阶段 evidence。`allowed_actions` 只是本次快照提示，执行时重新授权。Unknown 状态或 capability 不得被未知 enum 的 fallback 变成允许操作。

## WebSocket 与分页

`GET /api/v1/stream` 只升级为实时推送；不承载普通 RPC。浏览器使用同源 Cookie，严格 Origin 检查；程序客户端用 Bearer header。token 不放 query string。订阅消息仅选择已授权的 resource type/ID 与 cursor；服务端逐次按 capability 过滤，撤销后停止投递并要求重建会话。

envelope 包含 `stream_id`、`sequence`、`type`、`resource_ref`、`resource_version`、`observed_at` 和可选 `correlation_id`。类型包括 `resource.changed`、`state.coalesced`、`resync.required`、`heartbeat`；未知类型忽略并按需要 REST 重读。队列和 counter 合并按实现设计预算；断线、gap、过期 cursor 或服务重启后先 GET REST 权威资源。对 Audit/Job/transaction 只发变更提示，不能以丢失 WS 帧丢掉持久记录。

REST cursor 是不透明 base64url 签名 envelope，绑定 principal、权限 revision、normalized filter、排序、snapshot、generation、到期和最后已返回 key；不暴露数据库 offset。过期或权限变化拒绝 cursor；同一页引用同一个 snapshot，未知或截断集合不返回虚假完整 total。OpenFlow/FDB 的 collector 不能因 API 设计承诺 snapshot 就无界 dump 全表。

## 首切片迁移步骤与验收

1. #31–#37 建正式服务、认证、真实只读库存和持久资源；新进程使用独立目录/端口。旧 lab 数据不导入；用户显式重建或审阅迁移 metadata，真实 OVS 为配置来源。
2. #33 为接受后的 OpenAPI 生成 Go DTO/校验和 Svelte TypeScript；Domain 类型不依赖生成库。发布前按所有新增 Scope ID 核对 endpoint/operation/capability parity。
3. #38–#40 先在测试 OVS 上通过 typed validation、不同字段并发、同字段 Conflict、丢回执、Applied 超时、回滚冲突和重启恢复；生产写入口仍由成熟度和审阅控制。
4. #41 让 Svelte 首切片接入正式 REST 和 WS，复用已接受交互语义；#54 按全部 53 页清单接入其余页面与双语，保留首切片已通过的证据。
5. 首次正式 v1 发布固定 baseline；CI 以后对该发布 baseline 检查 breaking changes。不能用不断重写 baseline 的方式让检查通过。

设计检查只证明 proposed OpenAPI 结构、引用、请求/响应样例与本设计相符；不证明 mgrd、真实 OVS 或 UI 已实现。
