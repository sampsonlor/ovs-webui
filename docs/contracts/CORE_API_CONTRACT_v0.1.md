# OVS WebUI 核心流程接口契约 v0.1

日期：2026-09-05（Asia/Shanghai）。状态：**实现提案 / 可用于联调准备，尚未批准为架构基线**。

本轮把已有 Ports → VLAN → Candidate → Validate → Safe Apply → Evidence 链路转成可验证的接口约定。页面布局、按钮、图标和 IA 沿用 Design System v0.1；后端返回的状态仍使用现有 Conflict、OutcomeUnknown、Stale、Drift 和 Safe Apply 组件表达。

当前应用使用合成数据。显式本地联调模式已接入 Ports、持久化 Candidate、Diff/Validation、合成 Safe Apply、确认/回滚、Job 和请求账本；普通原型模式保持原有演示。生产鉴权与真实 OVS 操作尚未接入，详见 [本地持久化](LOCAL_PERSISTENCE_v0.1.md)、[验证联调](LOCAL_VALIDATION_v0.1.md) 和 [Safe Apply 联调](LOCAL_SAFE_APPLY_v0.1.md)。

## 范围与依据

以代码基线 `f154d60`、仓库 `AGENTS.md`、批准的 IA/Page Inventory、P1 计划及用户确认的事务约束为依据。Architecture v1.0.1 和 Phase 1 Scope v1.0 原始批准文件尚未在仓库提供，因此 URL、字段名、错误码、鉴权绑定和并发策略均标明为提案。没有改变 P1 六批顺序，也没有宣告 P1 gate 通过。

首条联调切片限于单节点 Port 库存与一条 VLAN 意图。Bond 继续作为具有多个 Interface 的 Port。既有更广的 Phase 1 能力不由这个切片重新定义。

| 可直接使用的交付物                       | 位置                                                             |
| ---------------------------------------- | ---------------------------------------------------------------- |
| 接口、请求、响应、错误结构               | `contracts/openapi.v0.1.json`，固定采用 OpenAPI 3.1.1            |
| 契约唯一编辑源                           | `contracts/core-v0.1.mjs`                                        |
| 自动生成的前端类型                       | `lib/api/types.generated.ts`                                     |
| 提交结果恢复、服务器状态映射、乱序读保护 | `lib/api/change-control.ts`                                      |
| 合成响应样例                             | `contracts/examples/core-fixtures.mjs`                           |
| 契约与行为检查                           | `tests/api-schema.test.mjs`、`tests/api-change-control.test.mjs` |

OpenAPI 采用 3.1 系列的 JSON Schema 语义；TypeScript 类型不能替代接收 JSON 时的运行时验证。新 HTTP adapter 启用前必须复用 schema 校验，不直接把任意响应强制转换为类型。[OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)

## 三种版本，以及一种进度

| 字段                                   | 代表什么                                                   | 前端处理                                                                    |
| -------------------------------------- | ---------------------------------------------------------- | --------------------------------------------------------------------------- |
| Candidate `revision` / HTTP ETag       | 当前候选的表示版本，包括需要用户看到的 freshness 变化      | 只比较相等；编辑使用原 ETag；不自己加一                                     |
| `baseGeneration` / `currentGeneration` | 候选起点与当前配置快照的身份，包含 OVS 实例/重建的区分能力 | 当作不透明字符串，不排序，不用显示名称替代稳定对象 ID                       |
| Transaction `sequence`                 | 同一事务的状态更新序号，重启后仍单调递增                   | 忽略更旧或同序号的状态覆盖；不同事务单独管理                                |
| `nextCfg` / `curCfg`                   | OVS 配置应用进度的原生证据                                 | 使用十进制字符串避免大整数损失；不当成 Candidate revision 或全局 generation |

OVS 的 `cur_cfg` 跟进 `next_cfg` 表示其完成一组配置应用；这里进一步要求健康和确认各自有证据，不能由这两个数相等推导管理连接或业务流量正常。[OVS 数据库手册](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)

Candidate 的 strong ETag 对应 `/candidate` 本身，不包含每次变化的服务器当前时间；读响应的时间可使用 HTTP Date。错误的 If-Match 返回 412，缺少必要前置条件返回 428。验证/提交请求体中的其他对象版本是业务 CAS，失败用具体 409 错误码，不冒充 HTTP ETag。[HTTP 条件请求](https://www.rfc-editor.org/rfc/rfc9110.html#section-13.1.1)、[428 Precondition Required](https://www.rfc-editor.org/rfc/rfc6585.html#section-3)

## API 资源与当前组件

前缀暂定 `/api/v1`，同源、单节点，由已认证身份决定 Candidate 所有者。请求体不接收 `userId`、`expert`、`force` 或任意 OVSDB 操作。

| 资源           | 方法和路径                                                | 消费位置                                |
| -------------- | --------------------------------------------------------- | --------------------------------------- |
| 会话恢复       | GET `/workspace`                                          | 全局 Health / Jobs / Changes 与初始门禁 |
| 端口读取       | GET `/ports`、`/ports/{portId}`                           | PortsPage / Port Detail                 |
| 持久化候选     | GET、PATCH `/candidate`                                   | VLAN Editor / CandidateChangeCard       |
| 验证任务       | POST `/validations`、GET `/validations/{validationId}`    | Diff / Validation                       |
| 原始配置事务   | POST `/transactions`、GET `/transactions/{transactionId}` | Safe Apply / TransactionBanner          |
| 确认或回滚命令 | POST `/transactions/{transactionId}/decisions`            | Safe Apply 操作区                       |
| 结果核对任务   | POST `/transactions/{transactionId}/reconciliations`      | OutcomeUnknownPanel                     |
| 请求接收账本   | GET `/requests/{requestId}`                               | 提交应答丢失后的恢复                    |
| 后台工作       | GET `/jobs/{jobId}`                                       | Jobs / Evidence                         |
| Drift 观测     | GET `/drift`、POST `/drift-observations`                  | GenerationWarning / Drift               |
| 关联证据       | GET `/evidence?transactionId=…`                           | Evidence                                |

POST 返回 202 表示接受异步工作，前端随后读对应资源。验证 Job 完成不等于验证通过；Reconciliation Job 完成不等于配置 Applied；Safe Apply Job 的进展也不等于用户已确认。

## Candidate、VLAN 和三方冲突

1. 服务端持久化每用户 Candidate；关闭标签页不会丢失。空 Candidate 仍有身份和 revision。当前切片最多一条意图，第二个对象不能静默替换第一条。
2. VLAN 编辑只提交稳定 `portId` 和规范化窄字段：`mode`、`tag`、整数数组 `trunks`。显示名、缩写 UUID、范围字符串都不作为对象身份或 wire payload。
3. 此编辑器接受 Access、显式 VLAN 列表的 Trunk / Native tagged，VLAN 为 1–4094。范围展开、去重、排序在送出前完成。这个限制是当前产品切片的输入策略，不是对 OVS 支持范围的声明。
4. OVS 的空 trunks 表示允许全部 VLAN；已有这种配置通过 `availability=unsupported` 加原生字段保留可读，不能转换成“没有 VLAN”或未经用户审查扩大范围。原生模式、VLAN 0/4095 等超出当前编辑器的值同样保留观测，不默默丢弃。[OVS VLAN 语义](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)
5. Conflict 返回 Base / Current / Mine、意图 ID、当前 generation 和冲突快照 ID。VLAN 的 mode/tag/trunks 作为一个有内部约束的字段组处理。Keep current 移除该意图；Use mine 只更新候选起点并保留用户意图；Cancel 不写入。
6. Stale 表示候选起点已旧。若只有非重叠字段变更，`rebase` 可携带空 resolutions；若 VLAN 重叠冲突，必须带所展示快照对应的明确选择。后端在一次原子检查中确认当前快照仍匹配，不能复用已经过期的三方选择。
7. Rebase 不触碰运行配置，并使 Validation 失效。后端 Apply 时仍必须对实际拥有的写入字段执行 CAS，不能用 UI 三方视图代替数据库并发保护。

## Validation 与 Apply 入场检查

Validation 绑定 Candidate ID/revision、配置 generation、策略 revision 和过期时间；返回规范化 diff、逐项检查与 SafetyPlan。SafetyPlan 的 available 表示资源准备条件可满足，不表示真实 checkpoint 已经创建或探测已通过。

Validation 的 `nodeId` / `requestId` 必须与原请求相符；`WorkspaceSnapshot.latestValidation` 为当前用户最新验证或 null，供刷新/重新登录恢复。已失效的历史 diff 保留并显示 Expired，不替换为新 Candidate 的内容。验证 Job 的完成与验证是否通过分别呈现。

提交时由服务端重新检查：身份和对象权限、provider/ownership、候选版本、当前 generation、未过期的对应验证、审计原因、必要的再认证、真实 checkpoint/probe/compare-before-rollback 能力。任何过期验证或 unknown/block 检查都不能产生可执行的通过令牌。期间 generation 变化需要重新核对，不能仅靠客户端缓存的绿色状态启动事务。

当前提案采用节点级配置事务入场锁，覆盖其他用户同时提交；每用户 Candidate 仍可独立持久化。锁与请求接收记录必须先持久化，再进行 OVS 写入。精确数据库、事务隔离和调度机制由后端实现设计完成；不是以浏览器互斥变量代替服务端锁。

Standard / Expert 不进入授权判断。桌面发起配置、平板/手机处理已有事务属于交互职责；浏览器宽度不是安全边界，服务端必须独立执行相同权限和验证约束。

## 超时、幂等和找回原事务

每次新命令生成一次 `requestId`；`Idempotency-Key` 必须等于该值。服务端将 key 与认证主体、节点、方法、路径、规范化 payload 绑定。同 key 同请求返回原操作；同 key 不同内容返回 `IDEMPOTENCY_MISMATCH`，不产生第二次写入。再认证 proof 校验与保密另行处理，不进入可导出的请求内容或客户端恢复提示。

请求被接受前，账本必须持久化其身份和入场状态。尚未解决的记录、配置快照、活动截止时间和锁在服务重启后都必须可恢复，不能由 TTL 删除。终态的保留策略与备份要求待后端设计确定；已超出可证明范围的 key 不能被当作安全的新请求自动重放。

```text
点击 Apply safely
  → 保存 requestId + nodeId 这两个非秘密恢复提示
  → 仅发送一次 POST /transactions
  → 收到响应：跟踪 transactionId
  → 超时 / 断线 / 应答不合法：标记 OutcomeUnknown
      → GET /requests/{原 requestId}
      → 已接受：GET /transactions/{原 transactionId}
      → 明确拒绝且 commandEffect=not-started：显示阻塞原因
      → 404 / 仍接收中 / 不可达：保持未知与新提交门禁
```

没有收到应答不证明没有提交。HTTP 也不允许客户端在无法证明幂等或未执行时随意自动重试非幂等请求。[HTTP 重试语义](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.2)

`submitSafeApplyOnce` / `recoverSafeApply` 的恢复协议已接入本地 HTTP 联调：恢复只读取原账本和原事务，不自动重发、不把找不到记录说成 Not Applied。WorkspaceController 在发送 Apply、Decision 或 Reconciliation 前保存按用户/节点隔离的非秘密恢复提示，页面重新打开后先核对原请求。Workspace 的可选 `latestTransaction` 保留最近终态的恢复入口；生产身份与真实设备仍需独立集成。

## Safe Apply 状态映射

事务同时返回 `phase`、`knowledge`、`outcome`、`safeApply` 和分阶段 evidence，避免用一个布尔成功值压扁不同事实。

| 后端证据                                                | UI 表达                    | 新提交 / 决策                                                  |
| ------------------------------------------------------- | -------------------------- | -------------------------------------------------------------- |
| queued / preflight / committing / applying              | 进行中                     | 保持锁，按后端允许动作展示                                     |
| knowledge=outcome-unknown，或当前读已过期/断线          | OutcomeUnknown，保留原身份 | 读取和核对；不据旧状态确认                                     |
| outcome=applied + awaiting-confirmation                 | 已临时应用，仍待确认       | 保留 Candidate；确认还须健康、checkpoint、权限和服务器窗口有效 |
| phase=settled + safeApply=confirmed + 已提交/已应用证据 | Confirmed                  | 服务端清理已消费的候选意图；仍检查节点是否有其他阻塞           |
| phase=settled + not-started + not-applied               | Not Applied                | 保留意图，重新读取并验证                                       |
| phase=settled + rolled-back + not-applied               | Rolled back                | 已恢复本事务拥有的字段；保留意图并重新验证                     |
| degraded / needs-attention / rollback-conflict          | 明确警告及证据入口         | 继续阻塞，不能 force/retry                                     |

“Applied”只说明已应用；若仍处于确认窗口，就必须继续呈现确认流程。原型的 `reconcile(result='applied')` 演示的是一个最终成功 fixture，不能在接线时原样用于任意后端 Applied 响应。

服务器决定确认窗口、绝对截止时间和回滚调度。原型 90 秒仅是 fixture，不冻结成部署默认。前端用服务器读取时间加本地单调经过时间估算倒计时；归零只禁用决策并读取权威结果，不宣布回滚完成。浏览器重载、休眠或关闭都不能取消服务器保护。

每次采纳的 `serverTime` 必须与该次读取的本地单调接收时间成对保存。相同 sequence 的新读取可以更新时钟锚点，但不能覆盖事务状态；被忽略的旧响应也不能刷新接收时间，否则轮询会错误地延长倒计时。

确认、超时调度和手动回滚竞争同一个事务版本，后端原子裁决。到期确认返回 `DECISION_EXPIRED`，版本已变返回 `TRANSACTION_VERSION_CHANGED`。回滚只对本事务写集合比较 after-image，再恢复 before-image；不匹配则停止并记录 Rollback conflict。重连不解除这个锁。

Reconciliation 请求只带 `requestId`，返回关联原事务的观测 Job，禁止 `result=applied` 等客户端选择。服务端核对连接、配置意图/世代、OVS 应用与恢复证据，分别得出 Applied / Not Applied / Degraded / Needs Attention；不能凭当前值恰巧相同就认定此请求成功，也不能以一个失败探测推导从未提交。

## Drift 与资源读取

Drift 是 desired 与 observed 的差异；Candidate Stale 是起点与当前配置版本的差异。`/drift-observations` 只触发读取，可更新观测元数据，不执行配置覆写、不自动 rebase。Read Job 完成后重新读取 DriftReport；仍为 drift 或 unknown 就保留告警。

Ports `availability=complete` 且 items 为空才表示空库存；degraded 保留已知行和 warnings；provider 不可用返回错误，已有缓存若展示必须标为过期。linkState unknown、speedMbps null 与 Down/0 分开。分页 cursor 固定快照，过期则重读，不拼接不同 generation 的两页。

## 错误与恢复约定

响应采用 `application/problem+json`，人读字段与稳定机器码分开，提供 request/correlation/原事务关联。`commandEffect=unknown` 时无论 HTTP 状态是什么，都不能把原命令当作未执行。[RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html)

| HTTP / code                                        | 界面与后续                                         |
| -------------------------------------------------- | -------------------------------------------------- |
| 401 UNAUTHENTICATED / REAUTH_REQUIRED              | 请求登录或再认证；重新读上下文；不自动重放原写命令 |
| 403 FORBIDDEN                                      | 无权限状态；Expert 不改变结果                      |
| 412 ETAG_MISMATCH / 428 PRECONDITION_REQUIRED      | 重读 Candidate；不覆盖其他标签页的新版本           |
| 409 CANDIDATE_STALE / FIELD_CONFLICT               | 展示 Stale 或三方冲突，重新审查并验证              |
| 409 VALIDATION_EXPIRED / VALIDATION_BLOCKED        | 新验证，不沿用旧结果                               |
| 409 TRANSACTION_ACTIVE                             | 跳转已有事务；等待其权威收敛                       |
| 409 DECISION_EXPIRED / TRANSACTION_VERSION_CHANGED | 读原事务，显示服务器裁决                           |
| 409 IDEMPOTENCY_MISMATCH                           | 禁止把同 key 改内容后重发；核查原请求              |
| 422 INVALID_INTENT / UNSUPPORTED_CONFIGURATION     | 定位输入问题或保持观察模式                         |
| 503 PROVIDER_UNAVAILABLE                           | 资源不可用；若原写结果未知，仍走原事务核对         |
| 404 NOT_FOUND（查询原 request）                    | 账本未给出证据；不是 Not Applied                   |

## 接线顺序与完成条件

1. **读取和恢复**：接 `/workspace`、Ports、持久化 Candidate；关闭/重新打开浏览器、多标签页编辑、换用户和节点身份变化均能正确恢复或隔离；恢复完成前不启用写按钮。
2. **验证和审查**：接 Diff/Validation，加入过期、非重叠 Stale、重叠 Conflict、权限变化、provider 不可用的后端集成测试；保留三方选择后重新核对的 race 测试。
3. **Safe Apply 纵向联调**：接 checkpoint、真实 probe、账本、事务调度、再认证、确认/回滚/证据；覆盖“已提交但应答丢失”、服务重启、浏览器关停、deadline race、外部并发写入和 rollback conflict。
4. **整体验收与视觉收口**：真实数据驱动正常与异常路径，再做桌面/平板/手机、键盘、缩放与视觉细节。此时集中调整按钮、图标、间距和深色模式。

接入前需要与上游批准架构对齐的事项是：认证/再认证提供方及 cookie/CSRF 名称、配置 generation 的生成与 OVS 重建规则、状态持久化与恢复调度方案、节点锁粒度、默认确认窗口、健康探测策略、终态证据保留和多意图扩展。这些已记录为后端设计输入，不以未经批准的值伪装已冻结。
