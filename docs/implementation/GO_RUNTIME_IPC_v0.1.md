# Go 双进程与受控 IPC v0.1

日期：2026-09-09。对应 [#31](https://github.com/sampsonlor/ovs-webui/issues/31)，状态：[PR #57 实现待审阅](https://github.com/sampsonlor/ovs-webui/pull/57)。

前置设计 [PR #56](https://github.com/sampsonlor/ovs-webui/pull/56) 已按用户的下一步指令接受，并合并为 `e2e649e`，接受标签为 `phase1-implementation-design-v0.1`。Scope DOCX 仍保留 Draft for Review；PR #19 的共享库存原型独立待审。本次交付正式 Go 运行时的基础，不代表用户认证、持久化、OVS 配置或整站 Svelte 已完成。

## 实现边界

| 部件 | 已实现行为 | 后续责任 |
| --- | --- | --- |
| `ovs-webd` | 拒绝 root 运行；TLS 1.3 / HTTP 1.1、嵌入的启动说明、REST 运行状态及独立 liveness | #33 完整 REST；#35 SecretStore/证书激活；#41 Svelte 首切片 |
| `ovs-mgrd` | root 进程，固定非 root webd UID/GID；仅 Unix listener；默认拒绝业务授权 | #32 双库；#34 mgrd Auth Grant；#36 真实库存 |
| IPC | 相同协议/软件/manifest digest 握手、逐连接身份校验、显式 `runtime.inspect` 操作、独立 capability 插入点 | 后续按编译期 registry 扩展 typed operation，不能引入通用命令代理 |
| 监督 | 两个独立 systemd units，异常退出后重启，关闭管理面不传播到 OVS | #40 journal/safety scheduler/watchdog；#50 安装包、升级与卸载 |

`/healthz` 只表明 webd 存活；`/readyz` 和 `/api/v1/runtime` 重新连接 mgrd、握手并读取 bootstrap 状态。相容时响应仍明确 `authentication_ready=false`、`configuration_ready=false`。mgrd 失联/版本不符返回 503，webd 的 liveness 与启动页继续可用。当前没有登录/配置端点，也没有把 lab 的合成角色作为正式身份。嵌入的启动说明不是完成的 Svelte 产品页面。

基础运行时仅依赖 Go 标准库；所有正式二进制使用 `CGO_ENABLED=0` 构建。Go 版本来自 `go.mod`，当前为 1.27.1。SQLite 驱动在 #32 引入，当前不会创建空 manager.db 或接触 OVS。现有 React/lab/Sites 原型的运行和托管配置维持原有流程；这两个 Linux daemon 不运行在 Sites 的 Worker 环境中。

## 通信与资源约束

socket 目录必须预先存在，拒绝 symlink、非可信所有者和其他主体可写的路径；root 拥有的 sticky 临时祖先只用于安全创建的测试目录。生产入口固定要求 root 所有者，socket 为 root:service-group / 0660，客户端和服务器都核对 Linux `SO_PEERCRED`。请求中的 peer/role 字段不能替代内核身份。

独占文件锁覆盖整个进程生命周期，包括 HTTP drain；锁文件 inode 保留。仅在取得锁且确认旧 socket 的所有者、组、模式和 ECONNREFUSED 后清理陈旧 socket。遇到普通文件、symlink、活动 socket 或不确定错误即拒绝启动。释放时仅清理自身 socket，不删除其他资源。

IPC client 每次建立一条连接，先握手再执行至多一个操作；响应丢失、取消、重定向或断连均不会自动重放。请求限 1 MiB、JSON 深度 16、数组/对象成员 4096、值 token 16384、单字符串 64 KiB；拒绝重复 key、大小写别名、未知字段、额外 JSON value、无效 UTF-8 和不成对 Unicode surrogate。协议 manifest 与代码预算有漂移检查，digest 对换行/空白归一化。

HTTP header 配置为 16 KiB，读 header 2 秒、请求/响应/idle 5 秒；Go HTTP parser 保留自身有限的 framing 缓冲。客户端另限制响应 headers 与 64 KiB body。最多 96 条已认证 IPC 连接、64 条 HTTPS 连接，启动状态查询最多 16 个。Auth、Control、Read、Heavy 各有独立 active/waiting 上限（4/8、4/8、8/16、2/16）；满队列返回明确的 429，排队与执行继承 context deadline/cancellation。

唯一业务操作 `runtime.inspect` 在固定 Read 队列上解码空 struct，再调用 `Authorize(ctx, grant, operation)`，要求 `state.read`。默认 authorizer 返回 `IPC_AUTH_UNAVAILABLE`；测试中的允许/撤销实现只作为依赖注入，未暴露成 daemon 开关。握手/最小健康查询只建立传输证据，不发 grant 或扩大权限。未来 authorizer/provider 必须遵守 context；本轮不假装实现已持久授权的安全回滚。

日志仅记录 service、静态 event/code 和 bootstrap 状态，不记录 body、Authorization、任意 URL 或错误原文。systemd 设置日志速率、内存、线程和文件描述符上限。没有第三个 daemon，也没有伪造安全循环存活的 watchdog 心跳。

## 构建与启动

```text
CGO_ENABLED=0 go build -o ovs-webd ./cmd/ovs-webd
CGO_ENABLED=0 go build -o ovs-mgrd ./cmd/ovs-mgrd
```

运行需要 Linux、一个固定非 root 服务账户、root 创建且不可被其他主体改写的运行目录，以及该账户可读的 TLS certificate/private key。私钥必须为普通文件，禁止 group write 和任何 other 权限；不提供明文 HTTP 降级或自动生成正式凭据。服务账户、TLS 策略和安装器仍分别由 #34/#35/#50 交付。

[systemd templates](../../packaging/systemd/ovs-mgrd.service) 与 [webd unit](../../packaging/systemd/ovs-webd.service) 默认从 `/etc/ovs-webui/runtime.env` 读取固定配置；[example](../../packaging/systemd/runtime.env.example) 的零 UID/GID 故意不可运行，部署时必须解析实际账户。当前 units 的 mgrd 地址族仅 AF_UNIX、capability 仅 CHOWN；后续 provider 引入所需系统能力时必须带相应测试和审阅。没有 OVS unit stop/restart 关系或 OVS 安装行为。

## 验证证据与审阅

本地 Windows 已通过通用 Go 测试和 vet；Linux amd64/arm64 可交叉构建。Windows 不作为 Unix credentials、systemd 或真实 OVS 的执行证据。新增 [CI job](../../.github/workflows/ci.yml) 在 `ubuntu-24.04` 和 `ubuntu-24.04-arm` 原生运行，CI Gate 要求两者都成功。

实现提交 `5284735` 的 [Linux CI](https://github.com/sampsonlor/ovs-webui/actions/runs/34337698896) 已在两个原生架构通过 Go vet/race、协议 fuzz、CGO-free 构建，以及 systemd/真实 OVS dummy datapath 故障验收；OVS 实测版本为 3.3.9。[保留的结果与来源](../reviews/evidence/GO_RUNTIME_IPC_v0.1.json)记录测试 merge SHA、job、artifact 校验和、架构与六个进程场景，不依赖临时 artifact 长期存在。最终 PR 的完整 CI Gate 仍以其最新提交为准。

| 正常与异常路径 | 可复核证据 |
| --- | --- |
| 身份正确、握手成功、逐操作允许与权限撤销 | [Linux IPC tests](../../internal/ipc/unix_linux_test.go)；每次操作重新调用 authorizer |
| 错误客户端/服务端 UID、版本/digest 不匹配、未握手 | 内核 Unix socket 上拒绝；header 不能冒充 peer |
| 未知操作、参数歧义、超限、慢 header、取消、队列耗尽 | typed/framing、[JSON/fuzz](../../internal/ipc/json_test.go)、[预算测试](../../internal/ipc/budget_test.go)；不重放、不泄漏容量或 secret |
| 双实例、陈旧 socket、symlink、drain 期间 singleton | Linux socket 生命周期测试及进程强杀后的重连 |
| mgrd 不可用、独立 web liveness、TLS-only | [bootstrap tests](../../internal/web/bootstrap_test.go)及真实 HTTPS 进程验收 |
| systemd 强杀/重启任一进程及关闭管理面 | [隔离 smoke test](../../tests/daemon/smoke.py)：保留 OVS 进程 PID、原生配置 fingerprint，并在 dummy datapath 中注入数据包核对输出计数 |

CI 只在一次性 runner 中安装 OVS 测试前提，使用独立 OVSDB、dummy datapath、唯一 systemd units 和合成文档地址。它不修改业务网络；实际 OVS 版本、架构、场景结果和服务日志分别保留为 `go-runtime-amd64` / `go-runtime-arm64` artifacts。以对应 PR 的实际 CI 结果为准；这证明基础进程隔离，不等于 #36/#39 的真实 provider 功能或整个支持矩阵资格验收。

本批没有改变 Standard/Expert、桌面/平板/手机的交互职责；现有原型浏览器 CI 继续执行。#31 在本实现与上述 CI 证据获接受后关闭，再推进 #32 双库。#29 仍为 In Progress。
