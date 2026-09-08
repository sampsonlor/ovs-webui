# OVS WebUI 当前进度

更新：2026-09-08。P1 六批均已接受。Capabilities 通过 [PR #17](https://github.com/sampsonlor/ovs-webui/pull/17) 合并至 `main` 的 `862b19a`，接受标签为 `prototype-capabilities-v0.1`。此前 System Health 为 `b08e0a3` / `prototype-system-health-v0.1`，CI 基础 [PR #16](https://github.com/sampsonlor/ovs-webui/pull/16) 为 `fc0f387` / `prototype-ci-browser-v0.1`，#6、#7 已关闭。本轮六批整合修复与覆盖盘点等待审阅；历史记录保留各自日期和范围。

**P0 与 P1 六批已接受，整站高保真与正式管理平面尚未完成。** 本轮统一 Bond 页面与程序化入口的名称、成员和 native diff，并验证跨域共享事务。当前审阅入口是[六批整合 v0.2](reviews/INTEGRATION_v0.2.md)与[批准 IA 的 53 项覆盖盘点](reviews/P1_IA_COVERAGE_v0.1.md)。各批证据见[Capabilities](reviews/CAPABILITIES_v0.1.md)、[System Health](reviews/SYSTEM_HEALTH_v0.1.md)、[加速观察](reviews/ACCELERATION_OBSERVE_v0.1.md)、[OpenFlow](reviews/OPENFLOW_HIGH_FIDELITY_v0.1.md)、[Diagnostics](reviews/DIAGNOSTICS_HIGH_FIDELITY_v0.1.md)和[Bridge / Bond](reviews/BRIDGE_BOND_HIGH_FIDELITY_v0.1.md)。

## 基线和来源

| 文档 | 当前依据 |
| --- | --- |
| [Architecture Baseline v1.0.1](baselines/OVS_WebUI_Architecture_Baseline_v1.0.1.docx) | 2026-09-07 收到原文，正文标记 Baseline Approved，日期 2026-09-02；已原样归档 |
| Phase 1 Scope v1.0 | 仍缺独立批准原文；Architecture 第 11 节提供功能边界，但不替代独立范围文档 |
| [UI Information Architecture / Page Inventory v1.0](baselines/OVS_WebUI_UI_Information_Architecture_Page_Inventory_v1.0.docx) | 仓库已有 Approved Baseline；本次根目录同名文件正文为 Draft for Review，保留较新的批准版作为依据 |
| [P1 Prototype Plan v0.1](plans/OVS_WebUI_P1_Low_Fidelity_Prototype_Plan_v0.1.docx) | Draft for Review；实现及单批接受情况参见集成记录 |

## 实现范围

| 范围 | 进度 | 仍需完成 |
| --- | --- | --- |
| P0 黄金路径 | 冻结；Ports → VLAN → Candidate → Diff/Validation → Safe Apply → Evidence 可演示 | 真实数据、真实权限和正式后端接入 |
| P1 Batch 01 Bridge + Bond/LACP | 原批次与六页高保真均已接受；PR #11 已合并 | 统一完整库存、正式原生事务 payload / provider |
| P1 Batch 02 Diagnostics | 原批次与两页高保真均已接受；模板限制和异常语义已统一 | 正式持久化 Job 和受控诊断执行器 |
| P1 Batch 03 OpenFlow Viewer | 原批次与高保真均已接受；共享快照与 Observe 边界已统一 | 正式 OpenFlow 采集 |
| P1 Batch 04 DPDK/Offload Observe | 单个总览与只读能力证据已获用户接受；127 项回归、3 项集成及浏览器局部矩阵通过 | 真实 Provider、完整硬件库存与持久化观察 |
| P1 Batch 05 System Health | 六域共享观察、组件责任、事件时间线及恢复入口已实现；145 项回归与 3 项集成通过，已获用户接受 | 正式健康 Provider、统一库存和持久化状态 / Event 服务 |
| P1 Batch 06 Capabilities | 9 项能力矩阵、五状态、四 Gate 及 native Candidate / Safe Apply 已接受；PR #17 已合并 | 正式 provider、字段级权限、step-up、持久化能力与恢复证据 |
| P1 六批整合 v0.2 | Bond 入口一致性修复、跨域事务回归及 53 项 IA 盘点已准备 | 本轮接受；统一库存、稳定对象/Job/证据路由 |
| CI 工程基础 #6 / #7 | 已获用户接受并合并；#6、#7 已关闭；接受时 145 项回归、3 项集成、12 项浏览器测试及构建通过 | 详见 [CI 审阅](reviews/CI_BROWSER_BASELINE_v0.1.md)；Capabilities 新增覆盖见本批记录，真实 OVS 与正式管理面另行验收 |
| Design System / 高保真 | 核心 P0 与 P1 六批使用统一组件；各批保留浅/深色、窄屏及放大文字局部证据 | 整站深色、浏览器缩放矩阵及其余 IA 页面 |
| 批准 IA 导航 | 五域映射已接受并合并；桌面、窄屏共用定义；未实现入口明确 Planned | 完整 Page Inventory 与独立资源页仍未全部实现 |
| Ports/VLAN 本地 lab | SQLite 持久化 Candidate、验证、合成事务、证据及原请求恢复已实现 | 不能替代正式双进程管理平面或真实 OVS 测试 |

## 架构原文对后续实现的约束

1. **正式技术栈不同于当前原型。** Architecture §4、§16 规定 Go 的 `ovs-webd` / `ovs-mgrd`、Svelte 5 + TypeScript + Vite 静态 SPA、生产环境无 Node runtime。当前 React / Vinext 与 Node SQLite lab 是交互和契约验证工具。正式实现应沿批准栈迁移可复用的类型、状态语义和设计组件规格；如改变选型，需按基线 §1 形成 ADR。
2. **生产授权与持久化边界尚未建立。** §6、§9 要求 web.db 保存用户意图，manager.db / mgrd 掌握最终授权、事务及 Audit。lab 的本地角色选择和单库合成执行不构成这项架构实现。
3. **事务原生语义仍待真实 provider 验证。** §7 要求 touched-field OCC、OutcomeUnknown reconciliation、Commit 与 Applied 分离、确认窗依赖 Applied evidence、回滚前比较和跨 instance generation 禁止旧事务执行。现有自动检查验证的是合成服务和客户端契约；原型的递增 generation fixture 不应直接用作正式实例生命周期算法。
4. **原生功能范围不能从现有表单反推。** §11 包括 native-tagged / native-untagged、空 trunks 的原生语义及字段级 Authority。当前 VLAN 集成切片只开放受限输入，对不支持的原生配置保留 Observe；未覆盖的能力需要后续明确交付，不能静默归一化。
5. **正式 Release Gate 比现有 CI 更广。** §15 要求 amd64 / arm64、发行版矩阵、真实 OVSDB / ovs-vswitchd、恢复和安全测试。当前 Ubuntu x86_64 的原型 CI 与本机验证只覆盖其中一部分。

## 当前运行方式

- `pnpm dev`：完整合成原型；浏览器刷新重置会话状态。
- `pnpm dev:lab --port 3001`：Ports/VLAN 核心页面连接本地 SQLite；每用户 Candidate、Job、事务和证据可恢复。P1 配置暂未接入此持久化 API。
- lab 数据位于忽略目录 `.ovs-lab/`；测试数据均为合成样例，无真实 OVS 写入。

## 下一步

审阅 `codex/chore-p1-integration-review` 的六批整合结果。后续优先统一 Bridge/Port/Interface 库存、对象身份与稳定资源跳转，再准备符合架构原文的 Go / Svelte Ports/VLAN 正式切片设计。盘点已记录同 UUID 的 Bond 成员差异与 Interface 复用；当前独立 `br-native-demo`、外部 OpenFlow 样本与 Ports/VLAN lab 的有限边界仍保留。

历史记录：[集成接受 v0.1](reviews/INTEGRATION_v0.1.md)、[核心状态验收 v0.1](reviews/CORE_WORKFLOW_ACCEPTANCE_v0.1.md)、[本地持久化](contracts/LOCAL_PERSISTENCE_v0.1.md)、[本地验证](contracts/LOCAL_VALIDATION_v0.1.md)、[本地 Safe Apply](contracts/LOCAL_SAFE_APPLY_v0.1.md)。
