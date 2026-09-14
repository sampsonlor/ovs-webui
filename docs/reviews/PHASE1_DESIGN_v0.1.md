# Phase 1 实现设计审阅 v0.1

日期：2026-09-09。关联 [#30](https://github.com/sampsonlor/ovs-webui/issues/30)。状态：**Accepted for implementation**。用户审阅设计后要求继续下一步；PR #56 合并于 `e2e649e`，接受标签为 `phase1-implementation-design-v0.1`。Scope 原文仍为 Draft for Review，PR #19 保持独立待审。

本次解决正式管理面尚缺可执行设计与完整范围追踪的问题。交付物为原文归档、设计/ADR、全范围责任映射、首切片 API 与迁移草案及可复用契约样例。正式 Go daemon、Svelte 页面和真实 OVS provider 的实现与验收仍由后续任务承担。

## 审阅入口

- [实现设计](../implementation/PHASE1_IMPLEMENTATION_DESIGN_v0.1.md)：进程/IPC/双库、身份/版本、字段 Authority、授权、事务/恢复、初始预算与平台窗口。
- [Scope / IA 映射](../implementation/PHASE1_SCOPE_TRACEABILITY_v0.1.md)及[机器可读映射](../implementation/phase1-traceability.v0.1.json)：58 个 Scope ID、53 个 Page ID、Architecture 责任域、交付物/退出条件和差异处置。
- [ADR 0001](../adr/0001-phase1-runtime-contracts.md)：Go/SQLite、typed HTTP JSON IPC、并发和版本资格目标。
- [API 迁移](../contracts/PHASE1_API_MIGRATION_v0.1.md)、[OpenAPI 草案](../../contracts/proposals/phase1-v1.openapi.json)、[合成样例](../../contracts/proposals/phase1-v1.examples.json)及[契约检查](../../tests/phase1-proposal.test.mjs)。

## 来源与已接受基线

Architecture v1.0.1 与 IA v1.0 使用仓库中的批准原文。Scope v1.0 于本日收到，59,408 bytes，正文日期 2026-09-02、状态 Draft for Review；归档副本与用户原件逐字节相同，SHA-256 为 `d9fe23b0ee348c8680e8993eac12d0a79d769b5c816a066dcb81c6aecd87df81`。收到文件解决原文缺失，不代表 Scope 批准。

设计基于 main 的 `176bca1` / 已合并 PR #18；P0、P1 六批及整合已接受。PR #19 的共享库存与稳定导航仍另行审阅，不被本设计追认。映射中的原型覆盖采用已接受盘点 v0.1：13 项独立、18 项部分、22 项未实现；不能把这些数字当作正式服务交付进度。

已读取全部源文档的有序段落与表格，检查修订/批注并核对原文状态；源文件均无待处理的插入/删除或批注。Scope DOCX 仅原样归档，未编辑版式。当前 bundled renderer 缺少 `soffice.exe`，未完成页面渲染或声称视觉 QA 通过；本次范围/设计核对依据 OOXML 正文和完整表格。

## 本次验证

| 检查 | 结果与边界 |
| --- | --- |
| 来源与覆盖 | 三份基线 SHA-256 与映射一致；Scope 58 个 ID、IA 53 个 ID 和正文集合精确相同，无遗漏/重复；正式项均保持 not-implemented |
| OpenAPI 文档结构 | 17 paths / 19 operations；以 [OpenAPI 3.1 官方结构 schema](https://spec.openapis.org/oas/3.1/schema/2022-10-07) 和 Python jsonschema 4.25.1 校验通过 |
| Schema 与引用 | Ajv 8.20.0 / ajv-formats 3.0.1 校验 40 个 component schemas、182 个局部引用和 path parameters；无未解析引用 |
| 请求/响应样例 | 35 组样例通过；连同结构/operation 检查共 37 项可复跑测试，纳入现有 unit CI 的测试发现范围 |
| 现有 lab 契约 | 保留原生成源、类型/validator、路由与 fixtures；运行 `pnpm contracts:check` 检查生成物一致性 |
| 修改边界 | 新增设计、Scope 副本、契约提案及其样例/检查，更新当前索引；没有正式运行时、部署、站点访问或 lockfile 变更 |

样例验证正常 access/trunk/native-tagged/native-untagged/QinQ、空 trunks 和原生保留值；拒绝非法 VLAN/重复集合、越界批次、任意操作、伪造 role/outcome、缺少 decision sequence 和错误 identity 类型。响应样例接受未来字段、状态、观察 binding 和 intent，避免兼容性改变导致整页失败。这里验证的是 wire shape，不能据此宣称权限执行、字段 CAS、持久化或网络恢复已实现。

安装锁定依赖后可复跑：

```text
pnpm contracts:check
node --test tests/phase1-proposal.test.mjs
pnpm exec oxlint tests/phase1-proposal.test.mjs
```

PR 的现有 Quality/build、隔离集成及浏览器 CI 继续提供原型回归证据；以该 PR 实际检查结果为准。设计检查不能代替真实 OVS、原生 arm64、systemd/重启恢复、安装包或安全测试。

## 正常、异常与交互验收责任

| 场景 | 设计与后续责任 |
| --- | --- |
| 登录 → Port/VLAN → Candidate → Validation → Applied → Safe Apply → Evidence | #31–#41 首条真实闭环；#20/#22 保留完整功能验收 |
| Drift、同字段冲突、独立字段并发、失效 Validation | #36/#38/#39；强身份、语义读写集和三方 Diff，不能恢复 lab 全节点锁 |
| OVSDB 丢回执、Applied 不明、回滚冲突、跨 generation | #39/#40/#48；原 request 恢复、因果证据和受保护回滚；禁止盲重试 |
| webd/mgrd 重启、manager.db 损坏、deadline/revoke/confirm race | #32/#34/#37/#40；持久 admission、单一裁决与 fail-safe；实际安全恢复需独立证明 |
| Provider unknown/stale/unavailable/degraded、超时/截断 | #44–#47/#52；按字段保留来源、覆盖范围和可用性，不以空结果冒充失败 |
| Standard / Expert | #41/#54；同一状态、权限和验证，仅呈现深度不同；本次未新增或重验 UI |
| Desktop / Tablet / Mobile | #41/#54；桌面完整配置，平板审阅/推荐诊断/现有 Safe Apply，手机事故协作；键盘、焦点、双语和响应式均保留独立证据 |
| OpenFlow / DPDK / Offload | 已接受原型及 #47 仍 Observe；#53 的本地托管条件写入先独立审阅，DPDK/Offload 保持 Observe |

## Disposition 与下一步

- [x] 接受 Scope Draft 状态的处置及与 Architecture/IA/当前交互约束的显式差异；不静默升级源文档状态。
- [x] 接受运行时/driver/typed IPC、双库 handoff、身份算法、权限、SecretStore 与资源预算设计。
- [x] 接受字段并发、OutcomeUnknown、Applied、确认期限和受保护恢复的设计与后续故障证据要求。
- [x] 接受未发布 lab → 正式 API 的迁移、首个发布 baseline 与全站 API/UI parity 责任。
- [x] PR #19 保持独立待审，其原型增量不阻止本次 Go/IPC 基础工程；确认 #31 准入。

#30 设计冻结完成；#29 与 #31 为 In Progress，后者另有[实现审阅](../implementation/GO_RUNTIME_IPC_v0.1.md)。#52–#55 保留 Phase 1 Todo；#51 汇总发布证据。设计接受不等于正式功能、API 发布或产品 Phase 1 完成。
