# #39 字段执行审阅记录

状态：实现完成，等待本分支最终 CI 与双架构 native OVS 验证。测试通过后按用户授权合并 main，并删除当前特性分支。

范围与取舍见[实现说明](../implementation/OVSDB_EXECUTION_v0.1.md)。这是一项正式后端基础验收；公开高风险 Apply、Safe Apply 确认、回滚与页面接入分别由 #40/#41/#54 完成。

| 场景 | 预期证据 |
| --- | --- |
| 已验证本地 Port VLAN 提交 | 原子 native wait/update/mutate；durable receipt/Job/transaction/Audit |
| 独立 Port 并发、其他字段改变 | 两笔提交成功且 next_cfg target 不同；未依赖字段保留 |
| 同 VLAN 字段竞争、多对象之一冲突 | 原生 wait 拒绝整笔提交；另一对象未被部分更新 |
| 撤权、Validation scope、草稿 witness 改变 | 发送前拒绝，无 OVS 配置写入 |
| 已提交但回复断开 | OutcomeUnknown；重协调只证明 Committed，丢失 target 时 Applied Unknown |
| 未收到请求、外部恰好写成目标值 | Ambiguous，保留字段保护，不自动重发 |
| ovs-vswitchd 暂停 | Commit 已知，Applied 等待；恢复并观察 cur_cfg 后才能 Applied |
| admitted / committing 后 SIGKILL | 前者 NotCommitted，后者 Ambiguous；两个边界均不自动重发 |
| 数据库同 UUID 复制替换 | 拒绝旧计划；身份重新确认前不能证明 Applied |
| 普通页面高风险入口 | SAFE_APPLY_REQUIRED，无绕过开关 |

本地为 Windows：可执行的 Go 单元测试与 Linux 交叉编译先行检查；Linux 的 SQLite 安全权限、SIGKILL、原生 OVS 和双架构测试以 CI 为准。原型页面未改变，Standard/Expert、响应式职责沿用已接受基线；本批不新增移动端写事务。
