# #39 字段执行审阅记录

交付入口：[PR #65](https://github.com/sampsonlor/ovs-webui/pull/65)。最终 CI 必须全部通过才生效；按用户授权合并 main 后删除本特性分支，不提前开放 #40 的安全门控。

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
| 实际 OVS commit 后、manager.db 保存回复前 SIGKILL | 独立恢复进程从提交标记证明 Committed；丢失 target 保持 Applied Unknown，原生写入仅一次 |
| 数据库同 UUID 复制替换 | 拒绝旧计划；身份重新确认前不能证明 Applied |
| 普通页面高风险入口 | SAFE_APPLY_REQUIRED，无绕过开关 |

本地为 Windows：可执行的 Go 单元测试与 Linux 交叉编译先行检查；Linux 的 SQLite 安全权限、SIGKILL、原生 OVS 和双架构测试以 CI 为准。原型页面未改变，Standard/Expert、响应式职责沿用已接受基线；本批不新增移动端写事务。

本地验收：215 项回归、3 项隔离集成、lint、typecheck、build、可运行 Go 测试及 Linux vet/编译通过。首轮 [CI 34954825617](https://github.com/sampsonlor/ovs-webui/actions/runs/34954825617) 的 amd64 全部检查通过（134 项顶层 Go race 与当时每 schema 七个 native 场景）；arm64 的新增执行测试通过，整包认证测试在原有密码哈希用例达到累计 180 秒限制。最终配置把认证核心与业务服务测试分为互补两组，每个测试仍执行一次，保留生产 Argon2 参数、race 检查及 180 秒组内限制。

最终 native 矩阵为每架构三份 schema、每份八个场景，包含补充的真实提交后 SIGKILL。未设置专用环境时跳过的测试入口均由显式 native / 子进程步骤执行；文件系统耗尽同样由独立 tmpfs 步骤执行。最终检查及提交落点在 PR Checks、合并记录和 `phase1-ovsdb-execution-v0.1` 标签中追溯。
