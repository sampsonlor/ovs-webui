# DPDK / Offload Observe 审阅 v0.1

审阅：2026-09-07；提交整理：2026-09-08（Asia/Shanghai）。起点：`1db5be6`；分支：`codex/feat-p1-batch-04-acceleration-observe`。OpenFlow PR #13 已获用户接受并合并，注释标签 `prototype-openflow-v0.1` 已保留该基线。

**Disposition：Batch 04 已实现并完成本地检查，等待用户审阅。System Health 和全局 Capabilities 尚未开始。** 本记录覆盖 P1-10 单个 DPDK / Offload 总览及页内只读能力证据；不是生产 Provider、硬件兼容性或启用流程验收。

## 范围和依据

按 Architecture Baseline v1.0.1 §11 的 Observe 边界和 P1 Prototype Plan v0.1 §9 实现。计划的 `/visibility/acceleration` 在当前交互原型中映射为 `acceleration-overview`；本原型使用共享 View 状态，未承诺正式 SPA URL。批准 IA 的 DPDK / Hardware Offload 观察需求集中在本批总览，完整独立资源页和 Batch 06 全局能力矩阵仍待后续审阅。

入口为 `Visibility → DPDK / Offload → 选择能力 → Readiness / Sources / Capability evidence`。观察可以导航至既有 Bridge 详情或诊断目录；没有 Enable、Tune、switchdev、驱动安装或直接保存配置的动作。所有配置继续经过 Candidate → Diff / Validation → Apply / Safe Apply → Event / Audit。

字段语义参考以下官方材料：

- [OVSDB schema 手册](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)：`dpdk-init` 配置与 `dpdk_initialized` 运行结果分开；`hw-offload` 开启不证明某条流实际卸载。页面不执行这些设置或重启。
- [OVS PMD 文档](https://docs.openvswitch.org/en/latest/topics/dpdk/pmd/)：RXQ / PMD 周期采样表达工作量，不能直接代表转发健康或链路利用率。
- [OVS DPDK physical ports / representors](https://docs.openvswitch.org/en/stable/topics/dpdk/phy/)：representor、PF/VF 关系及原生 Interface 类型分别建模，不能仅凭名称推断物理连接器。
- [OVS DPDK offload 文档](https://docs.openvswitch.org/en/stable/howto/dpdk/)：硬件能力和具体匹配 / 动作限制影响卸载，软件回退与丢包是不同证据。

## 状态与共享资源

`lib/acceleration-model.ts` 定义观察、来源和判定规则；控制器随共享 P1 生命周期挂载，跨页保留快照、筛选和选择。页面复用既有 Header、Notice、StatusBadge、表单与键盘可访问 Tabs。

| 状态 | 本批判定 |
| --- | --- |
| Enabled | 当前且同实例的支持、前提条件、配置与运行信号一致；不代表每条流都卸载或每个 Interface 健康 |
| Available | 已知支持、声明的前提条件满足，配置与运行均未启用；不提供启用承诺 |
| Missing prerequisites | 已知支持，但有明确未满足的前提条件；列明来源与原因，不自动修复 |
| Unsupported | 当前能力 Provider 明确报告不支持；不会从操作系统或缺失遥测推断 |
| Unknown | 缺少关键证据、配置与运行不一致、证据过期、实例不一致或当前服务不可用 |

首次在平板或桌面打开总览读取两项合成能力，后续主动刷新。默认 DPDK 为 Enabled，offload 为 Unknown；主机级 DPDK 初始化不推导 br-offload 的 Interface 类型。vhost socket / peer、PF/VF 映射和未提供的原生 Interface 类型保持 Unknown。representor 仅在对应 Provider 证据存在时声明，未确认角色时不会显示成物理端口。

每个字段保留 authority、source、observedAt、instance 和 generation，60 秒后实时显示历史证据，五态随之重新判定。当前共享 generation 改变也使旧关键字段失效；这是原型保守一致性演示，不能替代正式实例生命周期和 field-version 算法。

Provider degraded 单独显示：若核心运行证据仍齐全，可以保持 Enabled，但 PMD / 流 / drop 计数缺失显示 Unknown。降级在捕获时保留，恢复服务不会填回原快照中缺失的计数；读取失败后重试期间，历史状态保留到新读取成功。回退 fixture 含 0 条硬件流、12 条软件回退和独立原因；0 与缺失值分开。默认 vhost 和未返回的硬件映射不会以 0、Down 或 Unsupported 代替。

共享权限 / 服务在读取提交和完成时检查；重复读取被拒绝，权限撤销中止回调并清除快照，恢复权限后需要重读。页面公开 review 工具使用相同门禁；状态读取实时检查当前权限。Provider outage 和失败只允许查看明确标注的历史值，不会使旧快照成为当前成功结果。重试保留当前合成异常，需要显式更换 review fixture 才改变演示结果。

Capability evidence 展示本项能力的判定、权限、来源、前提条件和 Observe 边界。它是当前总览的详情，不冒充尚未实现的全局 Capabilities 页面。进入诊断时选择 `diag.acceleration.provider` 和 `Bridge/br-offload`，保留该模板的 Provider unavailable 状态；存在活动 Job 时打开原 Job，不改写其请求。

## 浏览器审阅证据

Windows Codex 内置浏览器、本地合成预览、可见控件与页面公开 WebMCP 工具。无真实 OVS / NIC 操作。

| 场景 | 实际观察 | 结果 |
| --- | --- | --- |
| 桌面 1440 × 1000 | 能力卡片、配置 / 运行分列、来源和证据入口；页面宽度未溢出 | 通过 |
| 五态和必要异常 | 两个能力依次检查 Enabled、Available、Missing、Unsupported、Unknown、mismatch、degraded、fallback、stale、generation mismatch、unavailable | 通过 |
| 筛选与关联导航 | 无匹配结果可清除；选择不会回退到隐藏卡片；相关 Bridge 打开 br-offload 的 Observe 详情 | 通过 |
| Standard / Expert | Standard 展示主因和必要证据；Expert 增加 PMD、RXQ、NUMA、representor、字段 identity；两者判定与权限相同 | 通过 |
| 能力 / 来源详情 | Provider unavailable 仍可打开本项能力证据；Sources 保留每字段来源、时间和身份；不产生配置动作 | 通过 |
| 诊断跳转 | 保留 Bridge/br-offload 和加速模板，Run 保持禁用并说明 Provider unavailable | 通过 |
| 空结果 / 失败 | 空结果没有伪造能力详情；失败显示 Read failed，不伪造成功快照 | 通过 |
| 服务门禁 | Loading、Error、Provider unavailable、Network loss 均阻断新读取，保留记录重新判定 Unknown | 通过 |
| 权限和竞争 | 读取中撤销权限后数据清空，恢复正常不复活缓存；紧邻第二次读取被 Busy 拒绝 | 通过 |
| OutcomeUnknown 隔离 | server-07 合成 VLAN 事务处于 Unknown 时读取观察，Candidate、事务与 evidence 前后完全相同 | 通过 |
| 平板 820 × 1180 | Standard 观察读取、卡片、详情及共享事务提醒可用；页面无横向溢出 | 通过 |
| 手机 390 × 844 | 缓存摘要、能力证据和事件入口；新读取入口被拒绝，既有 OutcomeUnknown 提醒保留；页面无横向溢出 | 通过 |
| 深色 / 200% 文字 | 临时 dark + 32 px 根字号；1100 与 820 宽度检查详情；修复长 capability id 换行后，页面及字段无溢出 | 抽样通过 |
| 键盘 | Enter 切换来源标签，Tab 进入带可见 outline 的详情面板 | 抽样通过 |

临时视觉设置已按 SHA-256 原样恢复 `app/layout.tsx`。根字号检查不等同浏览器缩放；完整浏览器缩放、读屏器和多浏览器矩阵继续待办。

## 工程验证和限制

类型检查、产品范围 lint、127 项回归（新增 16 项状态测试）、3 项独立 HTTP / 进程恢复集成测试及 `pnpm build` 通过。新增回归覆盖两类能力五态、缺失和负值区分、过期边界、不同实例、降级掩码、零计数、拓扑角色来源、筛选及读取门禁。构建保留 Vinext 既有静态路由分类 Unknown / 插件耗时提示。

这是两个能力的固定小型结构化样例和约 450 ms 合成响应，不包含真实采集器、持久化快照、真实权限或硬件配置。本页字段时间代表本次合成观察；正式 Go 管理平面需要真正的 Provider、预算、授权和实例标识。相关 Bridge 仍使用既有库存样例，review fixtures 不会改写跨域库存；完整拓扑统一和原生 Provider 接入尚未完成。

正式技术栈继续按批准架构使用 Go 双进程与 Svelte 5 静态 SPA。独立 Phase 1 Scope 批准原文仍缺失，当前范围沿 Architecture §11。此前记录的 Bond WebMCP 重名 / 固定成员样例差异仍待独立修复，本轮不扩大该创建入口的能力声明。

未增加依赖；保留 `pnpm-lock.yaml`、`.openai/hosting.json`、站点访问和根目录用户文档。未部署。[浏览器 CI #7](https://github.com/sampsonlor/ovs-webui/issues/7) 与[通用模板 lint #6](https://github.com/sampsonlor/ovs-webui/issues/6) 继续开放。本批接受后才进入 Batch 05 System Health。
