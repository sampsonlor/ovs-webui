# Interface 共享证据审阅 v0.1

2026-10-07 · #21 / #42 E6。交付候选；接受以最终 PR/main 完整 CI、报告、截图和同树核对为准。

| 范围 | 所需证据 |
| --- | --- |
| 精确对象与快照 | `TestObjectEvidenceKeepsTransactionIdentityAndSnapshotScope`：主要事务引用兼容、关联目标、快照上界、跨 ID cursor 拒绝、替代 ID 空结果、权限 |
| 原子性与预算 | `TestRelatedObjectJobEventsRemainAtomicBoundedAndDeduplicated`：Job 后续事件、回滚、去重冲突、无效/重复/超预算引用 |
| 升级与保留 | `TestObjectIndexMigrationPreservesLegacyCoverageAndCascadesPruning`：旧主要对象保持、删除记录级联清理；不补猜旧关联 |
| 前端响应边界 | `tests/frontend-client.test.mjs` 三项新增用例：scope/query 深链、详情读取、延迟跨范围、到期 cursor、权限刷新不清 scope |
| 原生 MTU | 三 schema `TestNativeInterfaceMTU` 中确认与补偿均读取真实 object-scoped Events/Audit，核对 admission、terminal、decision、目标和事务关联 |
| 正常浏览器 | 既有真实 MTU 流程追加回滚与确认后的完整追溯，事件/审计、精确资源链接、分页/刷新/返回和 cursor 例外 |
| 模式/响应式 | `interface-evidence-entry-standard/expert`、`interface-evidence-audit-standard/expert/dark/tablet/mobile`：相同权限，键盘入口，窄屏无页面横向溢出 |
| 记录/例外 | `interface-evidence-events/record/expired/empty/retired/unavailable`、`interface-evidence-withheld-standard/expert`；授权拒绝不呈现空成功，manager 恢复保持旧 scope |

本地要求：245 项单元/契约、Go 包、amd64/arm64 Linux 静态检查、类型、lint 和完整 `pnpm build`。正式 Gate：PR/main 六项 CI；每架构 228 项唯一 Go race 顶层通过及 25 项完整正式浏览器；共享 3 项集成、31 项原型浏览器。既有三 schema 的 QinQ、MTU/default、Bridge/Port/Bond/Safe Apply、admission 重复回归和真实 daemon/存储/auth/TLS/库存矩阵全部保留。Go 常规套件跳过的独立 root/native 场景必须由对应专用 Gate 成立，不能计作已经执行。

截图文件存在或 CI 绿色不足以代替视觉复核。需要查看最终 PR 的所有新增状态，以及 main 的代表模式、窄屏和例外。旧 #71 根因不因本批绿色重跑而关闭。全任务 #21/#42 仍需余下字段、一般图生命周期和其他批准范围的独立验收。
