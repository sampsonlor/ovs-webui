# OVSDB 字段级执行 v0.1

对应 #39，依赖已接受的 #36 库存身份、#37 共享证据、#38 Candidate / Validation。本批实现 `port.vlan.set` 执行基础；高风险公开入场、Reachability、确认与回滚由 #40 接续。没有生产绕过开关，HTTP `createTransaction` 经请求/当前权限校验后返回 `SAFE_APPLY_REQUIRED`。测试专用的隔离安全守卫仅存在于 `_test.go`。

## 入场、冻结与当前授权

`Workspace.ReserveExecution` 在 web.db 原子比较完整签名 Envelope 并持久冻结；同一请求可恢复原 reservation，新请求及新草稿修改被拒绝。丢失 mgrd 回执、webd 重启及超时均不解除冻结。旧保存请求仍返回原 receipt。#40 必须通过权威事务结果协调 consumed / release，不能增加客户端自报结果或超时解锁路径。

mgrd 的内部 `Engine.Submit` 要求 workspace/safety lease。执行前及发送前都重新加载当前 credential、epoch、policy、能力、不可变 Validation、期限、完整 Envelope、mgrd 单调 witness 与 provider authority。保护策略只认明确 local VLAN allowlist；未知/外控/漂移/schema 不兼容均阻止派发。认证撤销先于 durable dispatch intent 时必须拒绝；已越过该点的在途事务只能重协调，不能通过注销假装撤销。

SQLite 入场原子保存 receipt、journal、Job、Audit 和 `port.vlan` 字段保护。保护按管理对象及 VLAN 语义组隔离，四个 VLAN 字段共同保持原生模式约束。provider 网络 I/O 不占用 SQLite writer 或 auth gate。实际发送前先持久化 `committing`；之后断链/进程退出均不能自动重发。

## 原生事务与竞争

固定 typed compiler 生成 OVSDB `wait(timeout=0)`、`update`、`mutate`、`select` 和 durable `commit`。只写四个 VLAN 字段，保留未知列。读取约束包括 Port UUID/name/interfaces、VLAN before-image、Bridge 唯一归属/name/datapath_type、Interface name/type/options、root/对象 authority labels。UUID、进程身份、数据库文件见证、generation、schema 与新鲜快照联合检查。

不比较全局 `_version`、全部 Bridge ports 或全局 next_cfg 的相等值。不同 Port 及未依赖的字段可并行；同 VLAN 组或结构/authority 改变明确冲突。为保守识别任意 OVN/Neutron 管控标签，相关 external_ids map 是读依赖；新增此类 map 键也可能要求重新验证。只 mutate 本系统的证据键，绝不替换其他 metadata。

依据 [RFC 7047 §5.2.6–5.2.7](https://www.rfc-editor.org/rfc/rfc7047.html)，wait 不匹配使整笔事务中止，durable commit 请求落盘确认。结构化事务拒绝可证明 NotCommitted；部分发送、丢失回复、无效/不完整结果均为 OutcomeUnknown。不会根据相同 after-image 或 generation 猜测本次提交成功。

## Commit 与 Applied 证据

每笔事务把 HMAC 标记原子写入被修改 Port 的专用 external_ids 键，同时递增 root next_cfg 并在同笔事务读取确切 target。HMAC 绑定私有管理密钥、事务 ID 及 Validation scope；journal 私有计划不经公开 API 返回。任何匹配标记可证明该原子提交曾发生；全部标记、after-image、结构/authority 和身份仍须匹配才能判 Applied。

[OVS schema 文档](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)区分客户端递增的 next_cfg 与 ovs-vswitchd 完成配置后的 cur_cfg。本实现使用精确 int64 十进制字符串，保存返回 target，再从新鲜观察中验证 cur_cfg 达标及 Interface error。Applied 不代表转发健康、管理可达或用户确认。

若提交回复丢失，标记可把 Ambiguous 收敛成 Committed；本批无法恢复被丢失的精确 target，因此 Applied 保持 Unknown / RecoveryRequired。之后的更大 next_cfg 不是本次 target。若未发现标记，不能排除旧请求仍在途、标记被覆盖或删除，因此保持 Ambiguous，保留字段保护。计数器回退、文件替换、generation/schema 不连续及 after-image 漂移同样进入恢复。

## 恢复、API 与预算

启动及两秒维护周期只观察已登记的未完成执行；admitted 可证明未发送，committing 只能重协调。普通 Job 恢复不接管 `field-execution` handler。事件、审计、事务、请求和 Job 使用共享引用；未决数据与字段保护不受时间保留清理影响。公开列表/详情只返回脱敏证据，分页绑定当前 owner、权限、数据库 revision、请求参数和 90 秒期限；显式 POST reconciliation 只观察，不发写入。

固定上限：32 intents、256 KiB native plan、320 KiB 私有 record、1,024 条保留执行、16 条未决、共享 2 个 running Job；preflight 5 秒、Applied 观察预算 15 秒。容量耗尽拒绝新入场；本批不实现压力触发删除。终态字段执行释放其 VLAN 执行保护，web.db reservation 继续冻结；#40 的安全域必须覆盖从入场到确认/回滚完成的完整保护期限。

事务 DTO 中 `field_execution_state=succeeded` 仅说明字段执行完成，整体 phase 停在 `health-check`，health 保持 Unknown。本批不构造 confirmed、healthy 或 rollback 证据。

## 验证

Go 测试包含精确整数/原生计划/结果验证、撤权与 workspace witness、receipt replay、跨进程 SIGKILL 前后边界、冻结重启、未决保护保留。独立 native fixture 使用真实 ovsdb-server / ovs-vswitchd dummy datapath；Unix 代理只在测试中制造提交后断链或丢弃发送，不修改生产 provider 的身份检查。

CI 的 amd64/arm64 各运行三份 schema（3.3.9、3.7.1、4.0.0），验证不同 Port 并发、未依赖字段保留、多对象原子冲突、未知结果、Applied 等待、数据库文件替换及独立进程恢复。运行摘要见本批 review evidence；schema 版本不代表运行了三种 ovs-vswitchd 二进制。
