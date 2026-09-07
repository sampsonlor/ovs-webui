# OVS WebUI 当前进度

更新：2026-09-07。[PR #11](https://github.com/sampsonlor/ovs-webui/pull/11) 的 Bridge / Bond 高保真已获用户确认并合并至 `main`（`5218641`），注释标签 `prototype-bridge-bond-v0.1` 保留接受基线。本轮 Diagnostics 高保真位于 `codex/feat-p1-diagnostics-polish`，待审阅。本页是当前入口；历史记录保留各自的日期和范围。

**当前已完成原型的 P0 核心流程与 P1 前三批集成，Ports/VLAN 高保真和本地持久化链路已有实现。整站高保真与正式管理平面尚未完成。** 本轮统一 Diagnostics 目录与 Job / Result 两页，集中模板限制、结果完整性、重试和共享证据门禁。详见[Diagnostics 高保真审阅](reviews/DIAGNOSTICS_HIGH_FIDELITY_v0.1.md)；上轮接受结果见[Bridge / Bond 审阅](reviews/BRIDGE_BOND_HIGH_FIDELITY_v0.1.md)。

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
| P1 Batch 02 Diagnostics | 原批次 review gate 已接受；目录与结果两页高保真、模板限制和异常语义已统一 | 本轮高保真审阅；正式持久化 Job 和受控诊断执行器 |
| P1 Batch 03 OpenFlow Viewer | 已接受集成；Observe 范围 | 正式 OpenFlow 采集和整页视觉收口 |
| P1 Batch 04–06 | DPDK/Offload Observe、System Health、Capabilities 独立批次尚未实现 | 按既定顺序与 review gate 推进 |
| Design System / 高保真 | 核心 P0、Bridge/Bond、Diagnostics 已有统一组件；后两者做过浅/深色及放大文字局部检查 | OpenFlow 组件统一、整站深色与浏览器缩放矩阵 |
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

先审阅本轮 [Diagnostics 高保真结果](reviews/DIAGNOSTICS_HIGH_FIDELITY_v0.1.md)，再继续既有 OpenFlow Viewer 的组件统一。[浏览器 CI #7](https://github.com/sampsonlor/ovs-webui/issues/7)与[通用模板 lint #6](https://github.com/sampsonlor/ovs-webui/issues/6)继续开放，手工验收不等于关闭这两项。新增 P1 页面继续按 Batch 04 → 05 → 06 的顺序逐批审阅。正式工程实现另行建立符合架构原文的 Go / Svelte 纵向切片。

历史记录：[集成接受 v0.1](reviews/INTEGRATION_v0.1.md)、[核心状态验收 v0.1](reviews/CORE_WORKFLOW_ACCEPTANCE_v0.1.md)、[本地持久化](contracts/LOCAL_PERSISTENCE_v0.1.md)、[本地验证](contracts/LOCAL_VALIDATION_v0.1.md)、[本地 Safe Apply](contracts/LOCAL_SAFE_APPLY_v0.1.md)。
