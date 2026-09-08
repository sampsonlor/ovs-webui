# Capabilities 审阅 v0.1

日期：2026-09-08（Asia/Shanghai）。前置 Batch 05 已接受；CI 基础 PR #16 已合并至 `main` 的 `fc0f387`，#6、#7 已关闭，接受标签为 `prototype-ci-browser-v0.1`。

**Disposition：Batch 06 已实现并通过本地工程与浏览器验收，等待用户接受。** 本批不创建接受标签，不修改生产架构、部署或站点访问设置。

## 依据与范围

遵循 Architecture Baseline v1.0.1 §11、批准 IA 的 Administration / Capabilities（AD-01）及 P1 Prototype Plan v0.1 Batch 06。P1-12 为单个能力页，包含矩阵、证据详情和受限配置审阅；当前通过共享 View `capabilities` 导航，尚非正式 SPA 路由 `/admin/capabilities`。

入口为 `Administration / Navigation / Overview / System Health → Capabilities → 选定能力及证据 → 相关观察页或原生变更审阅`。四处入口消费同一投影，跨页、搜索、筛选或切换 Standard / Expert 不重新生成能力事实。

9 项声明样本覆盖 Bridge、Port VLAN、Bond/LACP、native protected Port、OpenFlow、DPDK、offload、bounded diagnostics 和 OVS lifecycle。五种可用状态统一为 **Enabled / Available / Missing prerequisites / Unsupported / Unknown**。Observe、Basic Manage、Manage、Manage if native 是操作深度；权限、Authority、风险和证据新鲜度分别显示与检查。

## 证据与原生配置边界

每项证据保留 code、domain、provider、source、observedAt、freshness、generation、管理深度及相关页。Standard 展示判定和责任；Expert 增加权限代码、契约版本、来源引用和 `can_enable`，不扩大权限。前置条件明确列出状态、原因和责任方，没有统一安装或修复主机的按钮。

DPDK / Offload 直接使用已接受的加速快照及其判定函数；OpenFlow 沿用原采集及其 Authority。刷新 registry 不更新这些快照的时间、来源或 generation。它们在 Available 时仍为 Observe。未声明 lifecycle executor 的当前样本为 Unsupported，不能触发安装、重启或升级。

原生演示只允许 `ovs.port.protected`，目标固定为合成本地 NORMAL 交换域：

`Bridge/br-native-demo → Port/isolation-demo → Interface/isolation-demo0`

该目标独立于原有外部 OpenFlow 控制的 br-fabric / br-storage 样本，也不改写既有 Port 库存数量。OVS [Port.protected 文档](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)说明 protected peers 之间的流量限制，以及与 unprotected peers 的通信；本原型进一步将操作限制在声明的本地 NORMAL 路径。它不等同于通用 Port Security 或控制器 ACL 管理。

只有以下四项都通过，且工作区为空、没有未解决事务、处于桌面，才显示受限配置 CTA：

1. Provider 明确支持当前目标、`can_enable` 和 prerequisites；证据未过期，实例与 generation 一致。
2. 字段归本地 OVS 控制，NORMAL 路径已声明；外部或未知 Authority 不准入。
3. 当前具有 `port.isolation.manage`；模式切换不授予权限。
4. Validation、影响评估、checkpoint 和 compare-before-rollback 可用；管理路径已确认不受影响，高风险情况需 step-up evidence。

审阅面展示 `protected: false → true`、流量影响、风险、无需 restart/reboot、checkpoint 及回滚条件。操作员必须明确勾选影响确认，随后进入共享 `Candidate → Diff / Validation → Safe Apply → Event / Audit`。暂存不改变 running；Apply 后只有 provisional 状态，必须通过现有 Job 确认。既有 Candidate、失效证据、权限撤回或未解决的事务均阻止新意图。

四项 Gate 在 reducer 的暂存、验证、提交及确认边界再次检查，证据读取会使已有 validation 失效。手工回滚还要求当前授权、目标字段与 provisional Applied 值相符；deadline recovery 属于 manager 责任，不能用过期或其他对象的证据覆盖配置。60 秒证据 TTL 独立于 90 秒确认窗；缺少新鲜恢复证据时停在 rollback conflict，不宣称已恢复。

Applied / confirmed / rollback 的能力证据引用原 Job。OutcomeUnknown 的 reconciliation 保留原 ID、correlation 和提交序号，不重复写入；明确结果之后还需重新读取 provider。当前这些资源均为合成会话状态，刷新页面会重置。Ports/VLAN 持久化 lab 不开放这个原生配置入口。

## 审阅矩阵

| 范围 | 已验证结果 |
| --- | --- |
| 正常路径 | 可解释的 Available → 影响确认 → Candidate → Validation → Safe Apply → Enabled / Audit；暂存无 live-save |
| 原生异常 | Missing、Unsupported、Unknown、external authority、permission withdrawn、恢复资源缺失、管理路径受影响、step-up 缺失、provider degraded/unavailable、stale、generation mismatch 均无配置 CTA |
| 读取与缓存 | Empty、Failed、无筛选结果分开；权限撤回隐藏并清除 provider 详情；延迟读取不得恢复已撤权数据 |
| 共享状态 | Navigation、Overview、Health、矩阵的五状态计数一致；Provider 健康与功能可用性、转发健康区分 |
| 已有事务 | 新 Candidate 被共享锁阻止；权限撤回禁用 confirm / 手工 rollback；恢复后处理原 Job；OutcomeUnknown 不重发 |
| 观察边界 | DPDK / Offload 的所有五状态仍为 Observe，相关页返回同一 source；刷新 registry 不续期 |
| 模式与键盘 | Standard / Expert 同一 Gate；原生按钮 Enter、带 label 的输入、明确焦点与影响复选框 |
| 响应式 | 桌面完整配置；平板 Standard 审阅、读取及既有 Safe Apply；手机只保留 incident context 和原事务 |
| 视觉 | 浅色、深色、1440 / 820 / 390 px 与 200% 根字号；浏览器自动化不替代完整读屏器或多浏览器缩放验收 |

## 工程检查

- `pnpm test:ci`：161 项通过，其中新增 16 项能力与原生事务测试。
- `pnpm test:integration`：3 项通过，保留隔离服务及进程重启恢复验证。
- `pnpm test:browser`：23 项通过，包含原有 12 项和新增 11 项 Capabilities 浏览器检查；覆盖完整事务、异常 Gate、延迟读取撤权、模式、共享来源及 lab 边界。
- `pnpm typecheck`、`pnpm lint:ci`、`pnpm build` 通过。构建保留 Vinext 路由静态分类、插件耗时和大于 500 kB 的客户端 chunk 提示；当前交互原型没有在本批进行整站拆包。
- 浏览器使用隔离 Playwright Chromium；prototype 与 lab 使用不同 worker 配置，不触碰用户 `.ovs-lab` 数据。失败 trace 沿用不收集 DOM/network 的 CI 基线策略。
- 正常路径、三个 Expert 异常组、Safe Apply、浅/深色、平板、手机及 200% 根字号的 9 张截图与 JUnit 报告保存在本机忽略目录 `outputs/reviews/capabilities/`。设置 `OVS_BROWSER_CAPTURE=1` 运行浏览器测试可重新生成截图。200% 字号下矩阵自动单列，侧栏按字号扩宽；视口无横向溢出。根字号检查不等同于完整浏览器缩放测试。

正式 Go webd/mgrd、Svelte 5 SPA、真实 provider / 权限 / step-up、持久化能力及恢复资源、统一真实库存和生产 OVS 验收仍属于后续工程工作。当前批次完成后先接受 P1 整合审阅，再确定符合批准架构的正式纵向切片。
