# OVS WebUI 当前进度

更新：2026-09-20。P1 六批及整合已接受；#30–#39 正式基础已依次接受合并。#39 字段执行通过 [PR #65](https://github.com/sampsonlor/ovs-webui/pull/65) 合并至 `7aef52d`，接受标签 `phase1-ovsdb-execution-v0.1`。#40 Safe Apply 交付见 [PR #66](https://github.com/sampsonlor/ovs-webui/pull/66) 与[审阅记录](reviews/SAFE_APPLY_v0.1.md)，以最终 CI、main 合并和接受标签为准。共享库存与正式基础的归集证据见[归集记录](reviews/MAIN_CONSOLIDATION_2026-09-15.md)。

**Phase 1 包括正式后端、完整前端和端到端验收，目前尚未完成。** P0/P1/P2 是原型批次编号，不能把正式后端整体推迟到产品 Phase 2。当前设计入口是[实现设计](implementation/PHASE1_IMPLEMENTATION_DESIGN_v0.1.md)、[58 项 Scope / 53 页映射](implementation/PHASE1_SCOPE_TRACEABILITY_v0.1.md)与[设计审阅记录](reviews/PHASE1_DESIGN_v0.1.md)。已接受的原型证据见[六批整合 v0.2](reviews/INTEGRATION_v0.2.md)、[批准 IA 覆盖盘点 v0.2](reviews/P1_IA_COVERAGE_v0.2.md)和各批记录。

## 基线和来源

| 文档 | 当前依据 |
| --- | --- |
| [Architecture Baseline v1.0.1](baselines/OVS_WebUI_Architecture_Baseline_v1.0.1.docx) | 2026-09-07 收到原文，正文标记 Baseline Approved，日期 2026-09-02；已原样归档 |
| [Phase 1 Scope v1.0](baselines/OVS_WebUI_Phase1_Scope_v1.0.docx) | 2026-09-09 收到并原样归档；文档日期 2026-09-02，正文标记 Draft for Review；不能自动视为批准版 |
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
| P1 六批整合 v0.2 | Bond 入口一致性修复、跨域事务回归及 53 项 IA 盘点已接受；PR #18 已合并 | 原型后续库存与导航见 PR #19；正式前端接线分别验收 |
| 共享库存与对象导航 v0.1 | PR #19 统一 4 个 Bridge、10 个 Port、13 个 Interface 的合成关系来源、严格对象 URL、缺失目标与只读 SW-07 上下文；纳入 main 归集复验 | 正式库存接线、Interface 原生字段和受控编辑由 #21/#54 交付；对象 URL 刷新不持久化原型事务 |
| Phase 1 实现设计 #30 | Go/IPC/双库、身份授权、Safe Apply/恢复、API 草案及全范围映射已接受；PR #56 已合并 | 各正式模块按设计独立实现与验收，Scope 原文保留 Draft |
| Go 运行时与 IPC #31 | PR #57 已接受合并，#31 已关闭；原生双架构及 OVS 3.3.9 进程故障测试通过 | #34/#36 继续 Auth Grant 和真实 provider |
| 双 SQLite Repository #32 | PR #58 已接受合并，#32 已关闭；双库与原生双架构故障测试通过 | 业务授权和配置状态机按后续任务交付 |
| 正式 REST v1 / 请求恢复 #33 | PR #59 已接受合并，#33 已关闭；113 路径 / 130 操作、全 Scope/IA 映射、运行时校验、持久幂等和 WS 提示流已验收 | #34 认证以及各业务 Gateway 继续接线 |
| mgrd Auth Grant / 每操作授权 #34 | PR #60 已接受合并，#34 已关闭，标签 phase1-auth-grants-v0.1；原生双架构各 76 项 Go race 与真实认证/重启恢复通过 | #20/#27/#28 完整页面及后续对象级 provider 仍分别验收 |
| SecretStore / HTTPS #35 | PR #61 已接受合并，#35 已关闭；分区 AEAD、密钥轮换、恢复授权撤销、证书候选与有界激活已验收 | #54 证书页面、#20/#27/#28 各功能主单和 #48 备份编排仍独立验收 |
| 真实 OVSDB Discovery / Inventory #36 | PR #62 已接受合并，#36 已关闭；实际 schema、原子 monitor、共享身份、字段权限和多证据生命周期核对；原生双架构各 104 项 Go race 及三份 schema 的真实 OVS/恢复场景通过 | #21/#22/#54 页面分别验收；写入继续由后续任务交付 |
| 共享 Job / 请求 / Event / Audit #37 | PR #63 已接受合并，#37 已关闭；共享持久服务、取消与恢复、当前授权、脱敏、保留及分页导出；122 路径 / 139 操作 | 原生双架构各 113 项 Go race 及真实服务场景通过；#20/#27/#28/#54 页面和具体执行器分别验收 |
| Candidate / Diff / Validation #38 | PR #64 已接受合并，#38 已关闭；正式持久草稿、三方 rebase、原生 schema / 当前授权校验、不可变 Validation/ChangeSet/Job/Audit 和请求恢复 | 原生双架构各 126 项 Go race 及三份 schema 中真实 HTTPS/IPC/OVS 场景通过；Apply #39、Safe Apply #40、前端 #41/#54 分别验收 |
| OVSDB 字段执行 / OutcomeUnknown / Applied #39 | PR #65 已接受合并，#39 已关闭；原生双架构各 134 项 Go race、每架构 24 个真实字段执行场景与完整 CI 通过 | [实现与门控](implementation/OVSDB_EXECUTION_v0.1.md)、[审阅记录](reviews/OVSDB_EXECUTION_v0.1.md)；公开高风险写入由 #40 的安全协调器准入 |
| 持久 Safe Apply / 独立 Watchdog / 精细回滚 #40 | PR #66 提供双库 outbox/journal、Applied 与真实 probe 门控、服务端确认/截止/撤权、原生字段 CAS 补偿及单个 Last Known Good | [实现说明](implementation/SAFE_APPLY_v0.1.md)、[审阅与原生/VM 验收矩阵](reviews/SAFE_APPLY_v0.1.md)；最终 CI 通过后合并并记录 `phase1-safe-apply-v0.1`；#41/#54 页面独立验收 |
| CI 工程基础 #6 / #7 | 已获用户接受并合并；#6、#7 已关闭；接受时 145 项回归、3 项集成、12 项浏览器测试及构建通过 | 详见 [CI 审阅](reviews/CI_BROWSER_BASELINE_v0.1.md)；Capabilities 新增覆盖见本批记录，真实 OVS 与正式管理面另行验收 |
| Design System / 高保真 | 核心 P0 与 P1 六批使用统一组件；各批保留浅/深色、窄屏及放大文字局部证据 | 整站深色、浏览器缩放矩阵及其余 IA 页面 |
| 批准 IA 导航 | 五域映射已接受并合并；桌面、窄屏共用定义；未实现入口明确 Planned | 完整 Page Inventory 与独立资源页仍未全部实现 |
| Ports/VLAN 本地 lab | SQLite 持久化 Candidate、验证、合成事务、证据及原请求恢复已实现 | 不能替代正式双进程管理平面或真实 OVS 测试 |

## 架构原文对后续实现的约束

1. **正式技术栈不同于当前原型。** Architecture §4、§16 规定 Go 的 `ovs-webd` / `ovs-mgrd`、Svelte 5 + TypeScript + Vite 静态 SPA、生产环境无 Node runtime。当前 React / Vinext 与 Node SQLite lab 是交互和契约验证工具。正式实现应沿批准栈迁移可复用的类型、状态语义和设计组件规格；如改变选型，需按基线 §1 形成 ADR。
2. **身份授权按独立批次验收，双库与正式 API 已接受。** §6、§9 要求 web.db 保存用户意图，manager.db / mgrd 掌握最终授权、事务及 Audit。#34 将 Local 身份、当前权限与安全管理服务接入 Go 双进程；只有显式初始化密钥与管理员并配置公共 Origin 后才启用，缺失依赖时拒绝认证。尚未接入的业务 provider 保持 unavailable。lab 的本地角色选择和单库合成执行不构成这项架构实现。
3. **事务以原生与恢复证据为准。** §7 要求 touched-field OCC、OutcomeUnknown reconciliation、Commit 与 Applied 分离、确认窗依赖 Applied evidence、回滚前比较和跨 instance generation 禁止旧事务执行。#39 已验证字段执行与 Applied；#40 将安全协调与原生补偿接入真实 provider，并用独立 VM 验证管理连接恢复。原型的递增 generation fixture 不能用作正式实例生命周期算法，未知结果不能由本地倒计时推成成功。
4. **原生功能范围不能从现有表单反推。** §11 包括 native-tagged / native-untagged、空 trunks 的原生语义及字段级 Authority。当前 VLAN 集成切片只开放受限输入，对不支持的原生配置保留 Observe；未覆盖的能力需要后续明确交付，不能静默归一化。
5. **正式 Release Gate 比现有 CI 更广。** §15 要求 amd64 / arm64、发行版矩阵、真实 OVSDB / ovs-vswitchd、恢复和安全测试。当前原生 Ubuntu amd64/arm64 的进程与存储测试补充了原型 CI；其他发行版及完整业务功能仍待各工程任务验收。

## 当前运行方式

- `pnpm dev`：完整合成原型；浏览器刷新重置会话状态。
- `pnpm dev:lab --port 3001`：Ports/VLAN 核心页面连接本地 SQLite；每用户 Candidate、Job、事务和证据可恢复。P1 配置暂未接入此持久化 API。
- lab 数据位于忽略目录 `.ovs-lab/`；测试数据均为合成样例，无真实 OVS 写入。

## 下一步

#39 已接受；#40 通过[本批审阅门槛](reviews/SAFE_APPLY_v0.1.md)后，下一项为 #41：Svelte 5 静态前端与 Ports/VLAN 正式服务端到端首切片。#54 继续负责完整页面迁移，#20–#28 均属于 Phase 1，按其完整功能与页面要求独立验收。开发仅保留 main 与一个活动特性分支；测试及 CI 通过后合并并删除该分支。

[Phase 1 看板](https://github.com/users/sampsonlor/projects/2)以 #29 为正式后端与集成总览，#30–#55 为 26 个工程任务。#30–#39 已完成，#40 随 PR #66 的验收状态推进，#29 仍为 In Progress。#20–#28 已统一加入 Phase 1 milestone，继续保留独立功能验收。#52–#55 跟踪搜索/拓扑、OpenFlow 条件门禁、完整 Svelte/双语迁移和管理员/API 文档。

历史记录：[集成接受 v0.1](reviews/INTEGRATION_v0.1.md)、[核心状态验收 v0.1](reviews/CORE_WORKFLOW_ACCEPTANCE_v0.1.md)、[本地持久化](contracts/LOCAL_PERSISTENCE_v0.1.md)、[本地验证](contracts/LOCAL_VALIDATION_v0.1.md)、[本地 Safe Apply](contracts/LOCAL_SAFE_APPLY_v0.1.md)。
