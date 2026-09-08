# 批准 IA 页面覆盖盘点 v0.2

日期：2026-09-08。依据为归档的 [Approved IA v1.0](../baselines/OVS_WebUI_UI_Information_Architecture_Page_Inventory_v1.0.docx) §6、[Architecture v1.0.1](../baselines/OVS_WebUI_Architecture_Baseline_v1.0.1.docx) 及当前原型代码。根目录同名 Draft 不替换批准版；独立 Phase 1 Scope 原文仍缺失。

**P1 六批接受不等于批准 IA 全部完成。** 下表逐项覆盖 53 个 Page ID：14 项独立、17 项局部、22 项未实现。“独立”表示已有独立原型 View；“局部”表示职责位于合并页、对象详情或共享流程；“未实现”表示没有可交付的对应工作流。独立 View 也只覆盖下列有限原型范围，不表示该 IA 页全部验收或已实现正式服务。已与批准原文的 53 个唯一 Page ID 逐一核对，无遗漏或额外编号。

本轮共享库存切片尚待接受；SW-07 增加独立只读关系上下文，因此独立 View 计数增加 1，完整原生字段编辑仍缺失。原生对象已支持按实例、UUID 与 generation 的可刷新 fragment URL，其他资源仍主要使用会话 View；表中 route 是批准 IA 建议路径，正式 SPA path 尚未实现。P1-01 至 P1-12 是批次演示步骤，不能与 IA Page ID 一对一计数。

| Page ID | 批准页面 / 建议 route | 覆盖 | 现有入口和关键缺口 |
| --- | --- | --- | --- |
| OV-01 | Dashboard `/` | 独立 | `dashboard`；共享 Health、Capabilities、Candidate 和 Safe Apply 摘要；库存仍为合成子集 |
| OV-02 | Search Results `/search` | 未实现 | 当前列表筛选不是跨对象、权限过滤的统一搜索 |
| SW-01 | Switching Overview `/switching` | 独立 | `switching-overview`；Bridge → Port → Interface 共享关系快照；完整主机库存/provider 未接入 |
| SW-02 | Bridges `/switching/bridges` | 独立 | `bridges`；筛选及创建意图；删除、高风险依赖尚未覆盖 |
| SW-03 | Bridge Detail `/switching/bridges/{id}` | 独立 | `bridge-detail`；关系、RSTP 摘要和 Candidate；完整 multicast/controller 字段和删除缺失 |
| SW-04 | Ports `/switching/ports` | 独立 | `ports`；原型 10 个共享 Port，lab 保持 6 个；未实现受控批量变更、完整主机库存 |
| SW-05 | Port Detail `/switching/ports/{id}` | 独立 | `port-detail` / `vlan-edit`；核心 VLAN 流程；完整 Interface/Statistics/Event/Audit 子资源缺失 |
| SW-06 | Interfaces `/switching/interfaces` | 局部 | 对象详情显示成员与关系；一级清单、来源与字段权限待实现 |
| SW-07 | Interface Detail `/switching/interfaces/{id}` | 独立 | `interface-detail` 只读关系上下文，稳定对象 URL；仍缺完整原生属性、正式资源及受控编辑 |
| SW-08 | VLANs `/switching/vlans` | 局部 | Port 内编辑和受限 lab；跨 Port membership、native-untagged、空 trunks 完整语义待实现 |
| SW-09 | Bonds & LACP `/switching/bonds` | 独立 | `bonds` / `bond-detail` / `bond-edit`；创建、编辑、成员与异常；正式 fallback/SLB/原生组合校验待接入 |
| SW-10 | STP / RSTP `/switching/spanning-tree` | 局部 | Bridge RSTP 摘要及 staged toggle；缺专页、互斥与 Port 类型限制完整工作流 |
| SW-11 | Multicast `/switching/multicast` | 未实现 | 缺能力证据、原生字段配置及未知映射保留 |
| SW-12 | Tunnels `/switching/tunnels` | 未实现 | 缺 Tunnel Port 语义操作、受控 options 与外部 underlay 说明 |
| SW-13 | Mirrors `/switching/mirrors` | 未实现 | 缺 source/destination、引用/循环校验与影响审阅 |
| SW-14 | QoS & Queues `/switching/qos` | 未实现 | 缺 Queue 模型、Port 绑定和 provider 门禁 |
| SW-15 | Isolation `/switching/isolation` | 局部 | Capabilities 中 `br-native-demo` protected Port 演示；无独立页或真实目标 provider |
| SW-16 | OpenFlow Overview `/switching/openflow` | 局部 | `openflow-viewer` 合并观察；bounded 查询、来源、authority；完整 pipeline 汇总待补 |
| SW-17 | Flows `/switching/openflow/flows` | 局部 | Viewer 内过滤/分页/导出；当前接受边界为 Observe，无条件写入口 |
| SW-18 | Groups & Meters `/switching/openflow/groups-meters` | 未实现 | 尚无完整分页集合和关联 flow 视图；不提供通用写入 |
| SW-19 | Controllers `/switching/openflow/controllers` | 局部 | Viewer 保留 ownership/context；缺专门 endpoint/role/connection 清单 |
| VI-01 | Endpoints `/visibility/endpoints` | 未实现 | 缺可靠 MAC/IP/LLDP 来源关联 |
| VI-02 | FDB `/visibility/fdb` | 未实现 | 缺按对象查询、分页、截断与导出 |
| VI-03 | Neighbors `/visibility/neighbors` | 未实现 | 缺 Linux ARP/NDP provider 和 freshness 状态 |
| VI-04 | LLDP `/visibility/lldp` | 未实现 | 缺可选 provider 及缺失原因；不部署外部 daemon |
| VI-05 | Statistics `/visibility/statistics` | 局部 | 对象和加速页有限运行摘要；缺统一 counters、reset/coalesced 与短时趋势 |
| VI-06 | DPDK `/visibility/dpdk` | 局部 | `acceleration-overview` 合并只读证据；无正式采集、专页或 enable executor |
| VI-07 | Hardware Offload `/visibility/offload` | 局部 | 同一加速页；硬件/driver/provider 证据样例，保持 Observe |
| VI-08 | Telemetry `/visibility/telemetry` | 未实现 | 缺 sFlow/IPFIX 原生 exporter 配置 |
| OP-01 | Health `/operations/health` | 独立 | `system-health`；六域共享观察和恢复入口；需正式健康服务及持久化 |
| OP-02 | Events `/operations/events` | 局部 | `evidence` 和 Health 时间线；Event 类型独立，缺独立查询、对象过滤和持久化流 |
| OP-03 | Diagnostics `/operations/diagnostics` | 独立 | `diagnostics-hub` / `diagnostic-run`；受限模板、参数与异常；缺正式受控执行器 |
| OP-04 | Jobs `/operations/jobs` | 局部 | 诊断和 Safe Apply 各有 Job 上下文；缺统一列表和真实 owner/权限 |
| OP-05 | Job Detail `/operations/jobs/{id}` | 局部 | 诊断结果与事务恢复；缺统一稳定 ID 路由和跨刷新资源 |
| OP-06 | Audit `/operations/audit` | 局部 | `evidence` 中独立 Audit 类型；lab 保存核心事务证据，未覆盖所有 P1 资源/权限/retention |
| OP-07 | Support Bundle `/operations/support-bundle` | 未实现 | 单次诊断 JSON 导出不是结构化、脱敏的 Support Bundle |
| CH-01 | Candidate `/changes/workspace` | 独立 | `workspace`；共享单对象 Candidate；多对象分组、协作和完整草稿服务待实现 |
| CH-02 | Diff & Validation `/changes/workspace/diff` | 独立 | `diff`；revision、原因、冲突与安全门禁；正式 typed policy/未知字段保留待接入 |
| CH-03 | Apply / Safe Apply `/changes/apply/{id}` | 独立 | `safe-apply`；确认/回滚/Unknown 与 lab 恢复；正式 mgrd watchdog 和 Applied evidence 未接入 |
| CH-04 | Drift & Conflicts `/changes/drift` | 局部 | Diff 中三方 VLAN、受限 topology rebase；缺独立共享 drift 清单/真实观察 |
| CH-05 | Checkpoints `/changes/checkpoints` | 未实现 | 事务内部恢复前提不等于可创建、比较、导出、恢复的 checkpoint 资源 |
| AD-01 | Capabilities `/admin/capabilities` | 独立 | `capabilities`；五状态、来源、四 Gate；真实 provider/权限/step-up 和持久化缺失 |
| AD-02 | Management Network `/admin/management-network` | 未实现 | `mgmt0` 样例不等于 internal interface/IP/route 的完整管理路径迁移 |
| AD-03 | OVS Lifecycle `/admin/ovs-lifecycle` | 局部 | Capabilities 解释 executor 缺失；无 restart/upgrade Job 工作流 |
| AD-04 | Backup & Restore `/admin/backup-restore` | 未实现 | 缺双库与 SecretStore 结构化备份、恢复和 instance reconciliation |
| AD-05 | Configuration Export / Import `/admin/configuration` | 未实现 | 缺 OVS 配置导入 → Candidate；不能用诊断导出替代 |
| AD-06 | Users & Roles `/admin/access/users` | 未实现 | lab 本地角色切换只是测试身份，无正式 principal/capability 管理 |
| AD-07 | AAA `/admin/access/aaa` | 未实现 | 缺认证链、测试、fallback 与 SecretStore |
| AD-08 | API Tokens `/admin/access/api-tokens` | 未实现 | lab 会话 token 不是 scoped API Token 管理 |
| AD-09 | TLS & Certificates `/admin/access/tls` | 未实现 | 缺证书链、write-only private key 和连接预检/恢复 |
| AD-10 | System Settings `/admin/settings` | 未实现 | Standard/Expert 是信息深度，不替代系统策略或 retention |
| AD-11 | Management Plane `/admin/management-plane` | 局部 | Health 组件责任与降级说明；缺独立页、受控 Debug/restart 与正式服务 |
| AD-12 | About & Support `/admin/about` | 未实现 | 缺统一 build/schema/platform tier、许可与支持页 |

## 本轮变化与下一步

共享关系来源、冲突 fixture 的显式纠正、稳定对象 URL 与缺失目标行为详见[共享库存审阅](SHARED_INVENTORY_v0.1.md)。最初的[盘点 v0.1](P1_IA_COVERAGE_v0.1.md)保留 PR #18 接受时的范围和缺口；此处 53 个 Page ID 未增删。

1. 接受共享库存与导航切片后，准备 Go `ovs-webd` / `ovs-mgrd`、Svelte 5 静态 SPA 的正式 Ports/VLAN 纵向切片设计，明确只读身份、权限、双库、Candidate、Applied evidence 与恢复服务。
2. Bridge/Bond 确认后仍需真正的权威观察来更新库存；Job/Event/Audit 稳定资源路由和完整集合不能由对象 fragment 导航代替。
3. 在正式服务依赖明确后补 Interfaces、VLAN 与 Visibility 页面；管理网络、认证、备份、TLS 和 Lifecycle 保留高风险权限与恢复要求。OpenFlow、DPDK/Offload 维持各自已接受的 Observe 边界。