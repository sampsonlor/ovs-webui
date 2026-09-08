# 共享对象库存与导航审阅 v0.1

日期：2026-09-08。**Disposition：本轮实现等待用户审阅接受。** 前置六批整合 PR #18 已获接受并合并至 `176bca1`，注释标签为 `prototype-p1-integration-v0.2`。本轮不改变批准架构、Phase 1 能力范围或配置事务流程。

## 交付范围

`lib/inventory-model.ts` 提供一个有明确边界的关系快照：实例 `ovs-synthetic-01`、库存 generation `1842`、来源 `synthetic-switching-inventory-v1`，包含 4 个 Bridge、10 个 Port 和 13 个挂接的原生 Interface。Bridge 计数、Port 子项、Ports 列表、Bond 配置及成员、诊断故障样例均从这份来源投影。Bond 仍然是 Port；主机挂接候选不计入现有 OVS Interface 行。

范围是已包含的合成对象，不能推断完整主机、local/internal Port、硬件或生产库存。Interface type 是显式样例事实；缺少原生 type 的 SmartNIC Interface 保持 Unknown，不能由名称、representor 角色或 authority 反推。OVSDB 配置控制权与外部 OpenFlow pipeline ownership 仍为不同字段域。

## 合成样例纠正记录

| 对象 | 原有差异 | 共享快照中的选择 |
| --- | --- | --- |
| `Port/bond-storage` | P0 成员为 enp130s0f0/1，P1 和诊断为 enp129s0f0/1 | 保留 P0 的 enp130s0f0/1，P1 与诊断统一读取；active-backup 显示 25 Gbps active，不把两个成员速率合计为转发容量 |
| `Port/bond-uplink` / `Port/uplink-01` | 同时占用 enp65s0f0 | 保留 P0 uplink-01 → enp65s0f0；Bond 样例显式使用 enp66s0f0/1 |
| `Port/storage-node-01` / `Port/server-08` | 同时占用 enp129s0f2 | 保留 P0 server-08；storage-node-01 使用 enp130s0f2 |
| `Port/rep0` / `Port/bond-provider` | rep0 跨页指向 pf0hpf 或 pf2hpf；Bond 又占用 pf0hpf | 保留 P0 rep0 → pf0hpf；Bond provider 使用 pf1hpf、pf2hpf。三者原生 type 均 Unknown |
| 对象 UUID | 旧页只保留截断显示值 | 为同名 Bridge/Port 补齐固定合成 UUID，保留原显示前缀/尾缀；Interface UUID 显式声明。不是从真实 OVS 读取，也不是生产迁移 |

这些修正是版本化 fixture 决策，不代表设备重命名、重新接线或生产网络变更。P0 六个对象的顺序和配置流程保留；新增的四个已有 P1 Port 现在也可从统一 Ports 列表进入。

## 对象导航和只读上下文

- 对象 URL 使用 `#object/{instance}/{kind}/{uuid}?generation=1842`，Port 可携带 `facet=port|bond|vlan|bond-edit`。UUID 按 kind/instance/generation 严格解析；页面内部的 `Kind/name` 旧引用只在当前具名快照内查找。
- Bridge 子项可打开普通 Port、Bond 和 Interface；Port 可回到 Bridge、Interface 或 Bond 配置；Bond 成员可进入 Interface 关系上下文；Diagnostics、OpenFlow、Health 及 Event/Audit 的原生对象链接使用同一解析器。
- Interface 上下文是 SW-07 的只读有限切片，显示所属 Port、Bridge、显式 native type 和来源。Expert 增加 UUID/instance/generation；没有 Interface 配置入口。手机和平板可读此事故上下文，既有高风险配置门禁不变。
- 浏览器刷新和前进/后退恢复原对象身份；未观察到的新对象、错误 UUID/kind、跨实例、旧 generation 或畸形 URL 显示 Object unavailable。去掉 Port/Bridge/Bond 详情的默认对象回退。缺失目标不会变成 `server-07` 或 `bond-uplink`，未知 Bond 也不会变成创建表单。
- 跳过导航链接只移动键盘焦点，不覆盖对象 URL，并给固定页头保留滚动空间。Port 页的 Bond 配置标签从相同的 Bond scope 投影，不继续显示旧的 Observe 标签。显示值经过 React 转义，URL 内容不能执行配置操作。
- 已确认的合成 VLAN 值由共享 `control.live` 投影到 Port 和 Bridge 子项。关系快照仍是固定来源；库存 generation 与配置/事务场景的计数分开解释，不声称真实实例 epoch 算法已实现。

## 保留的边界

`pnpm dev` 仍在刷新时重置 Candidate、Job 和事务；恢复对象 URL 不等于持久化工作会话。合成 Bridge/Bond 确认不会凭空生成权威库存行，点击其证据保留目标并显示尚未观察到。原生 protected Port 的 `br-native-demo` 演示仍属于独立能力来源；不把它或其他外部 provider 数据静默并入此关系快照。

SQLite lab 通过 `corePorts` 保持原六个 Port 的契约切片；不覆盖已有数据库中的库存、ID、generation、Candidate 或事务。新建隔离 lab 从共享定义取样，但 lab 的资源 ID/实例是自己的。合成对象 URL不能套用到 lab 资源；从 P1 指向 lab Port 的跨实例跳转给出明确说明。进入 lab 的 Ports 后按其实际资源选择。

正式的 Interface 字段编辑、完整库存和 provider、稳定 Job/Event/Audit 服务路由，以及 Go webd/mgrd + Svelte 5 正式 SPA 仍未交付。当前 fragment URL 是原型导航，不是新的 REST 合约或批准 SPA path 变更。

## Review Gate 与证据

| Gate | 本轮检查 |
| --- | --- |
| A 对象与身份 | 唯一 UUID、每个 Interface 只有一个 owning Port；Bridge 子项计数与同一集合一致；Bond 三处成员和诊断引用一致 |
| B 状态 | Unknown native type、provider degraded/unavailable、权限拒绝、未观察对象、旧 generation/跨实例/畸形 URL；无默认资源替换 |
| C 能力 | Interface 上下文只读；名称/成员校验、桌面新配置、Candidate/Validation/Safe Apply 门禁沿用既有实现 |
| D 跨页 | 同一 URL 的刷新/前进/后退；从证据跟进缺失目标；导航不解锁 OutcomeUnknown，不替换原 Candidate、transaction 或 correlation |
| E 响应式 | Desktop 正常与 Expert、820/390 只读上下文、深色及 200% 根字号；放大字号不等同于完整浏览器缩放矩阵 |

本轮新增 7 项库存/导航状态检查、5 项浏览器场景。本机使用锁定工具链 Node 24.19 / pnpm 11.19，结果如下：

| 检查 | 结果 |
| --- | --- |
| TypeScript / 全仓 lint / 生成契约一致性 | 通过 |
| 单元与契约回归 | 172 / 172 通过 |
| 隔离 HTTP / 进程恢复集成 | 3 / 3 通过 |
| Chromium 完整回归 | 31 / 31 通过；视觉收尾后另行复跑新增 5 项，全部通过 |
| `pnpm build` | 通过；保留既有 Vinext route classification、plugin timing 与 chunk size 提示 |
| IA 覆盖核对 | 批准原文 53 个唯一 ID 与表格 53 行完全一致；14 独立、17 局部、22 未实现 |

本地证据保留于 `outputs/reviews/shared-inventory/`，不将测试生成物加入源码：`unit.xml`、`integration.xml`、完整 `browser.xml`、复跑 `browser-navigation.xml`，以及 9 张截图。截图包含 Port、Bridge Expert、Interface Expert、深色、200% 根字号、820/390 宽、失效引用与 OutcomeUnknown 原目标。已逐张检查文字、长 UUID、关系链接和窄屏职责；修正了手机上下文被旧摘要遮挡、键盘跳过导航的标题遮挡和 Port 中过时的 Bond scope 标记。

PR 的 Linux CI 将再次对提交版本运行全部检查；本地合成测试不替代真实 OVS provider、完整浏览器矩阵或正式 Release Gate。

## 下一步

本轮接受后，准备批准技术栈上的正式 Ports/VLAN 纵向切片设计：明确只读库存身份、webd/mgrd 权限边界、双库职责、Candidate 契约、Applied evidence 和恢复资源的接入顺序。后续 Interface 完整页面与统一 Jobs/Events/Audit 应使用这些服务资源，避免继续扩张互不相通的 fixture。剩余 53 项页面覆盖见 [更新盘点 v0.2](P1_IA_COVERAGE_v0.2.md)。
