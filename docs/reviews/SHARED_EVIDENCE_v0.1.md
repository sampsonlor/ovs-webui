# 共享持久证据服务审阅 v0.1

日期：2026-09-15。任务 [#37](https://github.com/sampsonlor/ovs-webui/issues/37) / [PR #63](https://github.com/sampsonlor/ovs-webui/pull/63)。实现与技术验收完成；用户已接受，PR #63 合并至 `ba8a4eed5fdf2d3470d3670b6be38c1ba6b8ec97`，接受标签 `phase1-shared-evidence-v0.1`；#37 已关闭并在看板标为 Done，#38 已开始。

实现与边界见 [共享 Job、请求、Event 与 Audit](../implementation/SHARED_EVIDENCE_v0.1.md)。

| 验收要求 | 对应场景 |
| --- | --- |
| 持久 Job 与结果分离 | 版本 CAS、终态回执、queued 取消、运行中取消、Applied/commit/确认独立；TLS 专用恢复继续执行 |
| 原请求与原子证据 | 安全变更、Job、Event/Audit 和 receipt 同事务；错误注入后无部分角色、Job 或 receipt |
| 来源与脱敏 | 真实 OVSDB 变化为 External；恢复操作者 Unknown；敏感自由文本丢弃；同 key 去重/冲突 |
| 当前权限 | 跨用户 Job 读取/取消拒绝、撤销后拒绝重放、原操作 capability 与 WS 引用、受限 Token 拒绝 Audit/Job |
| 列表与导出 | REST 列表/详情、过滤、快照上界、权限与 cursor 绑定、版本/TTL/回收 410、分页 JSON 导出 |
| 保留与升级 | 活跃引用保护、终态及过期记录回收、边界可见、时钟倒退停止清理、旧 ID/correlation/sequence 导入与重启幂等 |
| 故障与容量 | Job 队列和证据容量拒绝、普通容量耗尽时已接受任务仍能写入终态证据、SQL 回滚、既有真实磁盘满、writer 压力和双 daemon 恢复测试 |

功能提交 `2f77f868407a81fef0e504b5d0ceb45f66093c17` 的 [原生 CI](https://github.com/sampsonlor/ovs-webui/actions/runs/34927583750) 六项全部成功。长期证据见 [SHARED_EVIDENCE_v0.1.json](evidence/SHARED_EVIDENCE_v0.1.json)，保留实际测试的合并 SHA、架构、测试名称、测量结果和 artifact 校验和。后续审阅文档提交的 CI 状态见 PR #63，分别记录功能证据与最终提交。

| 检查 | 结果 |
| --- | --- |
| Ubuntu 原生 amd64 / arm64 | 各 113 项 Go race 测试、vet、依赖校验、IPC fuzz、CGO-free daemon build 通过 |
| 本批 Job / Evidence 回归 | 两架构各覆盖 9 项测试，包含跨用户/原 capability、取消重放、重启、导入、回收、容量预留及原子失败 |
| 真实 HTTPS → IPC → mgrd → OVS | 两架构各加载 3.3.9、3.7.1、4.0.0 三份 schema；每份 7 组库存场景及共享 Job/receipt/Event/Audit/导出/脱敏检查通过 |
| 既有正式服务回归 | 真实磁盘满与恢复、systemd/Unix peer/OVS 转发、认证撤销、加密 TLS 激活与 120 秒恢复通过 |
| 前端与契约 | 208 项回归、3 项集成、26 项浏览器测试、typecheck、全仓 lint、契约与不可变基线兼容、生产构建通过 |

本地另完成可移植 SQLite Evidence 测试、Linux vet 及 pnpm build。真实 schema 矩阵由 CI 安装的原生 OVS binary 加载；artifact 分别记录 binary 和 schema 版本，不将其表述为三个产品版本或全发行版资格认证。发行版/内核/升级矩阵仍由 #51 验收。

Standard/Expert 的权限一致、桌面/平板/移动职责与键盘交互继续沿用已接受原型。本批正式服务不修改页面，不新增直接 live-save；#20/#27/#28 的完整功能页面及 #54 迁移保持独立验收。配置/诊断执行器和事务专用检查分别在 #38/#39/#46 接入，不能把本批共享生命周期视为这些业务功能已完成。

时钟回拨回归直接将模拟时间设置为数据库持久 high watermark 前 1 ms，继续要求 AUTH_CLOCK_UNSAFE。原测试使用实际 time.Now()-1 秒，在较慢的 race runner 上可能没有真正越过数据库边界；此次修正测试时间基准，不放宽产品时钟检查。
