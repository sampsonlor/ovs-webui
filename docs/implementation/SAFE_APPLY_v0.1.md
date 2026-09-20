# 正式 Safe Apply v0.1

对应 #40，接续 #39 的字段执行。公开 `POST /transactions` 只接受 `safe-apply`；普通 `apply` 继续返回 `SAFE_APPLY_REQUIRED`。本批支持已审阅 local VLAN allowlist 内的 `port.vlan.set`，不增加其他交换写入能力。

## 入场与双进程恢复

webd 在同一 web.db 写事务中比较 Candidate、保存私有 outbox 并冻结 workspace。类型化 IPC 1.5 只接受固定 `safe.admit / safe.resolve / safe.decide` 消息；不能传入原生 OVSDB 操作、probe URL、执行优先级或调用者权限声明。

mgrd 重查当前 credential、Validation、原始 HMAC envelope、单调 witness、provider policy、generation、原生依赖与安全能力。确认 TCP probe 可用后，原子保存字段执行计划、before/after、checkpoint、Safe Apply journal、管理路径保护、Job、Audit 和 receipt，再返回 202。实际派发在有界后台 worker 中进行，HTTP 退出不取消 watchdog。bearer 不写入 journal；服务通过 credential ID 重新检查撤销、当前权限、会话期限和授权上限。

入场先于网络派发持久化 `committing`。mgrd 重启时，未派发的 admitted 请求可以证明 NotCommitted；越过 dispatch intent 的请求只能读取证据，不能自动重发。确认或回滚成功后，mgrd 签署下一版 Candidate，webd 执行本地 CAS：Confirmed 创建新的空 Candidate 并同步两库中的 Candidate ID 和 witness，RolledBack / NotCommitted 保留意图并使旧 Validation 失效。旧请求的幂等重放仍返回原事务。没有 admission 的失联 outbox 由 mgrd 持久 tombstone 封住迟到请求后才能解除；浏览器和 webd 计时器都不能自报完成。

## 时间、权限与共享保护

持久记录包含 Linux boot ID、CLOCK_BOOTTIME 开始/截止值、辅助 wall-clock、sequence 和上次安全观察时间。准备/Applied 预算为 30 秒，用户确认窗为 120 秒，后者只在真实 Applied 和管理路径 TCP probe 成立后开始。重启不重建 deadline；跨 boot、时钟倒退、截止证据缺失会关闭确认并进入受保护恢复。

确认在 provider 证据及 probe 重查后通过同一 SQLite writer CAS，与截止、撤权和手动回滚裁决。定时健康读取没有确认或回滚权限。权限下降、发起 credential 撤销、确认超时，或连续三次 probe 失败会要求补偿；commit 尚不能证明时保留 OutcomeUnknown。

VLAN 四字段保护从入场持续到整个 Safe Apply 终态。配置同一管理路径的操作另共享该路径恢复域；这不是把所有 OVS 字段升级为全局锁。结构与第三方写入仍由原生 OVSDB CAS 保护。普通读取、诊断、HTTP/IPC control queues 不运行安全循环；mgrd 每秒独立推进恢复，只有推进成功且存储可写才发送 systemd watchdog heartbeat。unit 的 WatchdogSec=10s、TimeoutAbortSec=2s，重启不操作 OVS units。

恢复和确认使用专用 SQLite 只读连接；普通读取占满连接池不会堵住该连接。恢复写入等待时，普通 writer 在开始事务前让出队列；已经开始的写事务仍受五秒预算约束。优先级只由内部代码设置。存在未解决 journal 却缺失安全恢复配置时，mgrd 拒绝正常启动，不发送假健康 heartbeat。

## 回滚与 Last Known Good

回滚计划只从不可变原始计划导出。它要求相同 generation、schema、数据库文件与进程见证，检查本次 commit marker，并把 `wait current == our_after`、结构/authority 依赖检查、四个 touched VLAN 字段恢复、独立 rollback marker、next_cfg 增量与 durable commit 放在同一 OVSDB transaction 中。无关列、其他 Port、第三方 metadata 不被整库覆盖。

重叠变化产生 Rollback Conflict，保留保护与证据。generation 或强身份不连续时不派发旧补偿。rollback 自己也有 dispatch intent、Commit、精确 target、Applied 和管理路径证据；发送过补偿或值已等于 before 都不是 RolledBack。丢失 rollback 回复后，即使 marker 证明提交，无法恢复精确 next_cfg target 时仍为 Recovery Required，不重发。

Confirmed 在同一管理库事务中原子替换 singleton Last Known Good。它保留本批支持的有界字段计划和确认来源，不是整份 OVS 数据库镜像；后续完整备份/恢复由对应任务交付。未确认、回滚、冲突或未知结果都不会覆盖这份 checkpoint。manager.db 异常会拒绝配置操作，不从不完整状态猜测恢复计划。

## 运行配置与证据边界

默认没有 probe，公开 Safe Apply 保持关闭。root 在 mgrd 启动参数中配置已审阅的 `--local-vlan-ports`、`--safe-apply-probe-address=IP:TCP-port` 和 `--safe-apply-probe-interface=接口名`。生产安装可用受控 systemd ExecStart override 固定这些值；当前交付不自动选择管理端点或修改部署配置。

probe 只接受数值单播 IP 与明确接口，拒绝 DNS、loopback、link-local、URL 和重定向；socket 使用 SO_BINDTODEVICE，接口身份变化会停止确认。探测只建立 TCP 连接，不发送业务 payload。它证明管理员指定路径的 TCP 可达性，不代表每个客户端或全部转发业务健康；仍须用户显式确认。unit 仅为此增加 IP socket、netlink 接口查询及 CAP_NET_RAW，未授予 CAP_NET_ADMIN。

Native OVS schema matrix 验证真实原生写入、补偿 CAS、回复丢失、强杀与 generation 拒绝。独立 Ubuntu VM 场景使用 kernel OVS、veth 和 network namespace，让 VLAN 实际切断管理 TCP/HTTPS，并停止 webd、强杀/挂起 mgrd、等待真实 120 秒 deadline，最后验证管理连接恢复。合成 probe 和时钟仅在 Go 测试中出现，不进入生产二进制配置。

未知提交、丢失 Applied target、身份断层及第三方重叠修改可能需要管理员处理。没有 force、整库覆盖、压力删除 journal 或自动解除冲突入口。
