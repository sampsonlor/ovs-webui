# Safe Apply 本地持久化联调 v0.1

在现有 Ports → Candidate → Diff / Validation 上接入本地合成执行器、确认、回滚与证据。
IA、Standard / Expert 语义和 Design System v0.1 保持原有基线。此切片属于 Core 联调，
不表示后续 P1 页面批次已通过审查。

## 可审查流程

运行现有 `pnpm dev:lab --port 3001`。数据库增加事务表与索引，保留原有 Candidate、
会话、验证和请求记录，无需删除或重置 `.ovs-lab/state.sqlite`。

1. 选择 Editor A，在 Ports 保存 VLAN 意图；选择 Safety available (synthetic fixture)，
   在 Diff / Validation 重新验证。默认安全能力仍不可用，缺少能力时不能发起 Safe Apply。
2. 进入 Review Safe Apply，审查 Before / Proposed、填写操作原因，桌面点击 Apply safely。
   服务端先持久化请求接收、节点互斥锁和 Candidate 归属，再执行预检查与检查点。
3. 合成 Port 配置临时应用后，显示 Awaiting confirmation。Candidate 仍保留且不可修改，
   Job 仍未完成。确认测试观察后填写原因并 Confirm connectivity，才会清理已消费的意图。
4. 也可以 Roll back，或关闭页面等待服务器窗口到期。正常回滚只恢复本事务拥有的 VLAN
   字段，保留 Candidate 意图并要求重新审查/验证。重新打开页面可恢复最近事务，包括终态。
5. 使用 VLAN conflict 模拟外部重叠修改。确认被阻止；到期回滚检测到 after-image 不匹配后
   显示 Rollback conflict，保留外部值、证据和节点锁。Restore provider / permissions / node
   不会清除此锁。此切片没有 force、盲目重试或人工修复事务入口。
6. Provider unavailable 显示 OutcomeUnknown，恢复 provider 后服务端继续核对原事务。
   Check server evidence 创建只读观测 Job；Job succeeded 不等于 Applied 或 Confirmed。
7. 顶部 Jobs 与活动事务提示进入同一服务端操作；Event / Audit evidence 显示原事务记录。
   Standard / Expert 使用相同权限和门禁；Expert 额外显示事务、请求、版本和世代标识。
   平板/手机可处理已有事务，新 Safe Apply 只在桌面发起。

## 持久化与并发语义

- `/transactions`、`/{id}/decisions`、`/{id}/reconciliations` 使用现有类型化契约。
  请求账本绑定用户、节点、方法、路径和规范化内容；同 key 同请求返回原接受/拒绝响应，
  改内容或跨操作复用 key 返回 IDEMPOTENCY_MISMATCH。
- SQLite 节点入场表的唯一约束与 `BEGIN IMMEDIATE` 覆盖跨用户、跨连接和重启。
  每用户仍拥有独立 Candidate；节点存在未解决操作时关闭新的配置写入。
- 在入场、检查点和写入前分别核对版本、世代、权限、策略有效期、原生字段归属和安全能力。
  检查点保存 before-image，事务保存所拥有的写集合、after-image 与应用回执。
- 此合成执行器把库存修改和回执写入同一个 SQLite 事务。进程中断后可证明该次写入是否
  提交，不凭当前值恰巧相同推断成功。真实 OVS 是外部系统，不能直接继承这个原子性假设。
- 服务端在写入之前保存绝对确认截止时间。当前开发窗口 90 秒，测试专用进程可用 2 秒；
  两者都是合成开发参数。恢复、重放和浏览器倒计时均不延长该截止时间。
- 确认与超时/手动回滚在同一事务锁内裁决。过期确认返回 DECISION_EXPIRED，旧 sequence
  返回 TRANSACTION_VERSION_CHANGED。只有权威 Confirmed 才消费 Candidate。
- 回滚先比较本事务拥有字段与 after-image，然后只恢复这些字段。无关 Port 的外部变更
  保留；重叠冲突停止回滚且继续锁定节点。只读核对不会绕过冲突或释放未知状态的锁。
- 事务 sequence、Job、Event/Audit 和请求记录持久化；权限变化引起的可用动作也更新版本。
  Workspace 增加可选 `latestTransaction`，现有 `activeTransactions` 只包含当前用户仍锁定
  的事务。其他用户只看到节点被阻塞，不会收到本用户的事务、Job 或证据内容。

## 客户端恢复

写入前先保存按用户/节点隔离的非秘密恢复提示：requestId、nodeId、操作类型，以及已有
事务的 transactionId。浏览器存储不包含原因、Candidate 内容、Cookie、CSRF 或再认证 proof。
无法保存恢复提示时不发送命令。服务端 Candidate 和事务记录始终是权威来源。

应答丢失后保持 OutcomeUnknown；刷新、关闭再打开或重建 controller 后只读取原请求账本、
原事务和原 Job，不重发 POST。不确定的 404、错操作、错事务或错版本不会开启新提交。
读响应按 sequence 接受，服务器时间与本地单调接收时间配对；倒计时归零只关闭确认，
不宣布服务器已回滚。身份变化先清空私有客户端状态。

## 验收与边界

本切片的本地检查包含 81 项回归测试和 3 项独立进程集成测试。新增覆盖：逐阶段持久化、
确认清理、截止时间竞争、跨用户互斥、回滚冲突、保留无关外部修改、provider 丢失恢复、
Apply/Confirm 应答丢失后的页面恢复，以及真实强杀进程后的超时回滚。
同时运行契约生成一致性、全项目类型检查、产品代码 lint 与生产构建。

测试环境每次使用独立临时数据库，不操作当前预览数据。浏览器交互自动验收继续跟踪
在 [Issue #7](https://github.com/sampsonlor/ovs-webui/issues/7)。本次没有把自动 HTTP/状态测试
当作浏览器视觉验收。

本地检查点和 probe 只针对合成库存；未连接真实 OVS、管理网络探测或生产认证/再认证，
也未实现人工解除 Rollback conflict 的修复流程。生产集成需独立验证外部写入回执、原生
compare-before-rollback、守护调度与设备断连恢复。`nextCfg` / `curCfg` 不伪造为真实 OVS 证据。
