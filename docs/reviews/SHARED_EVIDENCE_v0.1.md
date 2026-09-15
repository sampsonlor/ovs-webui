# 共享持久证据服务审阅 v0.1

日期：2026-09-15。任务 [#37](https://github.com/sampsonlor/ovs-webui/issues/37) / [PR #63](https://github.com/sampsonlor/ovs-webui/pull/63)。实现已提交，原生 CI 验证中；本批保持 Open / In Progress，待用户接受。

实现与边界见 [共享 Job、请求、Event 与 Audit](../implementation/SHARED_EVIDENCE_v0.1.md)。

| 验收要求 | 对应场景 |
| --- | --- |
| 持久 Job 与结果分离 | 版本 CAS、终态回执、queued 取消、运行中取消、Applied/commit/确认独立；TLS 专用恢复继续执行 |
| 原请求与原子证据 | 安全变更、Job、Event/Audit 和 receipt 同事务；错误注入后无部分角色、Job 或 receipt |
| 来源与脱敏 | 真实 OVSDB 变化为 External；恢复操作者 Unknown；敏感自由文本丢弃；同 key 去重/冲突 |
| 当前权限 | 跨用户 Job 读取/取消拒绝、撤销后拒绝重放、原操作 capability 与 WS 引用、受限 Token 拒绝 Audit/Job |
| 列表与导出 | REST 列表/详情、过滤、快照上界、权限与 cursor 绑定、版本/TTL/回收 410、分页 JSON 导出 |
| 保留与升级 | 活跃引用保护、终态及过期记录回收、边界可见、时钟倒退停止清理、旧 ID/correlation/sequence 导入与重启幂等 |
| 故障与容量 | Job 队列和证据容量拒绝、SQL 回滚、既有真实磁盘满、writer 压力和双 daemon 恢复测试 |

本地 typecheck、全仓 lint、208 项回归、契约生成与不可变基线兼容检查、生产构建及可移植 SQLite Evidence 测试已通过。Linux vet 也已通过；原生 race、真实 OVS、认证与 TLS 恢复结果待 CI 完成后记录。

Standard/Expert 的权限一致、桌面/平板/移动职责与键盘交互继续沿用已接受原型。本批正式服务不修改页面，不新增直接 live-save；#20/#27/#28 的完整功能页面及 #54 迁移保持独立验收。配置/诊断执行器和事务专用检查分别在 #38/#39/#46 接入，不能把本批共享生命周期视为这些业务功能已完成。
