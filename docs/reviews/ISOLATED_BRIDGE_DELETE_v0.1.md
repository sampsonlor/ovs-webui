# #42 C1：隔离 Bridge 删除审阅 v0.1

日期：2026-09-28。[实现范围与边界](../implementation/ISOLATED_BRIDGE_DELETE_v0.1.md)。本记录定义验收要求；最终结果以本提交对应 PR/main CI 和接受标签 `phase1-isolated-bridge-delete-v0.1` 为准，未通过前不视为接受。

| 责任 | 验收证据 |
| --- | --- |
| 正常删除 | 已接受创建图 → 独立删除权限/root gate → Candidate/Diff → Safe Apply → Applied/probe → 显式确认；无暂存写入 |
| 原生身份 | 原图三行 GC/tombstone；补偿三行在删除前预留，确认后退休；回滚全新身份激活，原身份不复用 |
| 配置和依赖 | 私有来源证明；原生全配置、未监控 policing、Mirror weak reference、晚到成员变化均被拦截 |
| 补偿竞争 | 同名对象在原生派发前抢占，整体拒绝并保留外部对象与保护 |
| OutcomeUnknown | 删除/重建丢失回复；新执行器读取 journal，无重复派发，不虚构 target/Applied/成功导航 |
| 权限和容量 | 创建与删除权限/root gate 分离；撤权补偿；旧 credential ceiling 不扩大；身份和库存容量门控 |
| Linux 内核 | 删除 Applied 需主机接口消失；回滚重建新图及 local Interface；主机地址阻止删除 |
| 正式浏览器 | 真实 HTTPS/IPC/OVS；Standard/Expert 显示一致操作；平板/手机禁用新事务；事务列表/详情新身份映射一致，旧链接 404，新链接只在 restored 后出现 |
| 回归 | VLAN/Bond/创建、管理网络恢复、认证、HTTPS、存储、全部 Go race、契约、前端及原型构建/浏览器 |

新增 `TestNativeIsolatedBridgeDeletion` 每 schema 13 个叶子场景；报告 `go-isolated-bridge-delete-{schema}.json` 随两个 runtime artifacts 保留。正式浏览器共 9 个场景，新增截图 `frontend-evidence/bridge-delete-*.png`；不保留密码、cookie、CSRF 或网络 trace。

本批只覆盖本服务创建且仍然隔离的图。#42/#54 继续开放；#71 的一次性 arm64 登录问题仍单独跟踪，不因本批 CI 通过宣布根因已修复。原始根目录三份 DOCX 保留，部署配置和站点访问不变。

新增原生场景发现共享 Safe Apply 的保存时序缺口：watchdog 可以先观察到 Applied 并允许确认，而原执行记录仍保留 pending；后续恢复扫描会尝试重新更新终态 Job。修正后先持久保存观察结果再开放确认，确认时把新鲜证明与决策/Job 原子保存，已终结的安全事务不再占用未完成执行容量或重开 Job。补充确定性测试覆盖 watchdog 先于字段恢复、旧 awaiting-confirmation 记录，以及原始 target 未知但补偿已完成的情况；不增加重试、不放宽权限或安全超时。
