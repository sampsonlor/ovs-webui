# P1 六批整合审阅 v0.2

日期：2026-09-08。**Disposition：本轮整合审阅待用户接受。** P0 与 P1 六批各自已接受；本记录不表示整个批准 IA、正式架构实现或生产 Release Gate 已完成。

Batch 06 已获用户“很好，没有问题，继续下一步”确认。PR #17 在四项 CI 检查通过的 `da36029` 上合并，`main` 接受提交为 `862b19a`，注释标签 `prototype-capabilities-v0.1` 已推送。早期三批整合的 [v0.1](INTEGRATION_v0.1.md) 保留原日期与范围。

## 本轮修复

Bond 程序化创建此前允许使用已存在的 Port 名，并在 storage/management Bridge 上自动挑选已占用 Interface。页面与 WebMCP 现在共用 `bondDraftErrors` / `bondChangeIntent`：名称检查包含 P0 与 P1 库存；显式成员必须未分配，默认只选择最多两个空闲成员；不足两个即拒绝，不能接管别的 Port。可选 `minLinks` 与 UI 使用相同范围和 native diff，成员排序保持稳定。

编辑路径也使用该生成器；拒绝改名、迁移 parent Bridge、无变更或 Observe 写入，并保留已有 advanced 字段。模式、桌面限制、全局 Candidate/事务锁、provider/权限及后续 validation/Safe Apply 沿用原共享门禁。WebMCP 仍是合成原型入口，不是正式写 API。

## 整合 Gate 结论

| Gate | 本轮结论 | 证据与限制 |
| --- | --- | --- |
| A 对象语义 | 有限通过；库存统一待交付 | Bond 意图保持 Port 身份、Bridge 和 Interface 成员；UI/工具 diff 一致。不同批次固定库存仍存在同身份属性差异，详见盘点 |
| B 状态完整 | 沿用六批异常矩阵，新增跨域回归 | Unknown 配置事务与独立诊断 Job 的 Complete 可以并存；Healthy 观察不得清除 Recovery Required。正常/异常 native Gate 仍由 Capabilities 测试覆盖 |
| C 能力一致 | 本轮入口差异已修复 | 重名、已占用成员、最小链路与 LACP 约束一致；Observe 刷新不写配置；Standard/Expert 不增加权限 |
| D 跨页闭环 | 会话内共享通过；稳定路由/完整库存待交付 | Candidate、transaction ID、correlation 和 Audit 保留；诊断 Event 使用自己的对象/关联。页面仍是会话 View，不能宣称 URL 深链、统一 Jobs 或全部 P1 持久化已完成 |
| E 响应式职责 | 检查桌面新建与窄屏恢复 | 820/390 拒绝新建 Bond；已有 Safe Apply 保留同一事务与确认/回滚；新增截图验证窄屏无横向溢出 |

局部 Gate 的限制不是 Architecture Baseline 变更请求；它们是正式实现前须完成的库存、路由与服务集成工作。批准 IA 53 个 Page ID 的逐项状态见 [覆盖盘点](P1_IA_COVERAGE_v0.1.md)。

## 验证记录

本轮新增四项 Bond 状态回归和三项隔离 Chromium 跨域回归。浏览器通过捕获公开 WebMCP 注册的真实回调检查入口，UI 路径使用可见控件；不直接注入 Candidate 或事务状态。

- `pnpm test:ci`：165 项通过，生成契约检查通过。
- `pnpm test:integration`：3 项通过，覆盖隔离 HTTP、进程重启和无浏览器回滚恢复。
- `pnpm test:browser tests/browser/p1-integration.spec.ts`：本轮 3 项通过。已有 23 项浏览器基线在接受的 Capabilities 提交与合并后 main CI 中通过；PR CI 会运行全部 26 项。
- `pnpm typecheck`、`pnpm lint:ci`、`pnpm build` 通过。构建仍有原型既有的 Vinext 静态 route 分类、插件耗时和超过 500 kB 的客户端 chunk 提示。
- 4 张截图和 JUnit 报告保存在本机忽略目录 `outputs/reviews/p1-integration/`；已目视核对桌面 Candidate、Unknown/Health、820 平板与 390 手机。窄屏截图保留拒绝新建的反馈，同时显示原事务的确认/回滚控件。
- 测试使用隔离 Chromium 和临时服务，沿用 CI 的 trace 采集限制；未使用或重置用户 `.ovs-lab` 数据。

当前保留合成预览 `http://localhost:3001/`；prototype 模式刷新重置会话。持久化 lab 只覆盖原 Ports/VLAN 切片。没有修改部署、站点访问、批准基线或用户根目录文档。

## 后续建议

本轮接受后，优先处理统一 Bridge/Port/Interface 库存和稳定对象跳转，再准备 Go/Svelte 正式 Ports/VLAN 切片设计。先明确可复用的契约、状态规则和设计规格，避免继续把代表样例数量当作整站覆盖进度。
