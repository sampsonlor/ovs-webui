# OVS WebUI 代码基线盘点与设计核对 v0.1

日期：2026-09-05（Asia/Shanghai）

状态：Review draft；不是 Architecture、Phase 1 或 P1 批次的新批准记录。

## 结论

GitHub 当前代码是可运行的 P0 Golden Path 原型，适合作为 Design System v0.1 的实现基线。它不是完整 Phase 1 实现；P1 计划文件的状态仍为 Draft for Review。此次沿用现有 IA 位置和 P0 页面链路，从 Ports 与共享 Change Control 开始提取组件，没有创建新的业务导航体系或提前实现后续 P1 批次。

当前实现以单节点、Bridge → Port → Interface、Candidate 后提交、Standard/Expert 只改变信息深度为方向。原代码的硬编码候选、伪完成证据、无活动事务也可确认、前端计时器声称完成自动回滚等问题，会影响低保真审查结论；本轮已在原型层修正。

## 核对证据和边界

| 来源 | 实际取得的内容 | 用途与限制 |
| --- | --- | --- |
| `sampsonlor/ovs-webui` / `main` | commit `e87109bec03dcbf724f057d186be329caf5b7e87`，2026-09-03，Prepare public project repository | 本次唯一产品代码基线 |
| GitHub 官方源码 ZIP | 下载内容建立的 Git tree 为 `c60b33098f1398ed40ae4117c1789d1889168541`，与上述提交完全相同 | Git HTTPS 克隆连接失败后使用官方 archive；本地导入提交 `a921a6f` 不是伪称原始上游提交 |
| 根 `AGENTS.md`、`README.md` | P0 冻结、对象/事务约束、pnpm、串行 P1 gate | 沿用，不标记任何 P1 gate Accepted |
| `docs/baselines/OVS_WebUI_UI_Information_Architecture_Page_Inventory_v1.0.docx` | Approved Baseline；通过 DOCX XML 提取正文 | IA、信息深度、全局状态、响应式职责 |
| `docs/plans/OVS_WebUI_P1_Low_Fidelity_Prototype_Plan_v0.1.docx` | Draft for Review | P1 六批顺序、异常与恢复范围；不是已完成页面的证据 |
| 用户引用的“继续信息架构”对话 | 完整最近回合 | Ports 优先、三方冲突、OutcomeUnknown 四种收敛结果、服务端 per-user Candidate 预期 |
| Architecture v1.0.1、Phase 1 Scope v1.0 | 仓库只有文档名称与引用，没有原始批准文件；本地 `sources/` 为空 | 只能核对被 IA/AGENTS 重述的约束，不能声称完整架构/API 逐条验收 |

现有 Sites 项目名称为“OVS WebUI · P1 Batch 03 Review”，GitHub 则是 P0 快照。名称不证明线上实现内容，但提示两者可能不同步。本轮按用户明确指定的 GitHub 基线工作；覆盖既有线上版本前需要明确该版本关系。没有修改 hosting manifest 或访问策略。

## 实际路由与页面

只有 `app/page.tsx` 对应的 `/` 一个实际路由；九个页面是 `View` 状态切换，没有独立 URL、history 或刷新恢复。本轮保留此路由方式，默认落点改为 Ports，便于审查第一批设计组件。

| P0 ID | View | 原代码情况 | 本轮结果 |
| --- | --- | --- | --- |
| P0-01 | dashboard | 汇总卡、端口摘要、候选入口；部分数量和证据为静态展示 | 复用结构，统一节点措辞、候选身份、已确认 VLAN 与会话证据 |
| P0-02 | ports | 6 行 fixture；搜索有效；状态筛选、Saved view、Columns 无行为；仅鼠标行点击 | 提取 PortsPage；状态/能力范围筛选、列显示、空查询与资源状态、键盘对象入口、平板摘要 |
| P0-03 | port-detail | 依据选中行显示；Expert 增加原生信息，但 VLAN raw 值写死；Bond 关系不完整 | 复用结构；原生值来自选中对象；Bond 明确为 Port + 多个成员 Interface |
| P0-04 | vlan-edit | 本地表单；access tag 写死 120；trunk 映射仍保留 tag | 动态 Access/Trunk/Native tagged；范围验证；生成规范化 VLAN 意图 |
| P0-05 | workspace | `staged` 布尔量；对象与 diff 写死 server-07 | 候选保存选中对象、base/mine、generation、revision；统一 Change Card、Conflict、Stale/Drift |
| P0-06 | diff | 写死差异和 Pass 结果；缺乏真实验证版本门禁 | 显式 Validate；绑定候选 revision；修改、重基或场景变化使验证失效；审查弹窗 |
| P0-07 | safe-apply | 页面本地倒计时；闲置/完成也呈现确认操作；跨页状态提示不足 | 共享事务；全局固定提示；活动事务门禁、截止时间、断线结果未知、受保护回滚 |
| P0-08 | evidence | idle 默认时间线也显示成功；Export 无行为 | 只显示本次会话真实触发的合成记录；关联对象/Job；JSON 导出有效 |
| P0-09 | responsive | 职责表与手机示意；手机固定 00:42、Unknown 仍可确认 | 职责位置保留；手机入口共享实际事务状态；确认/回滚复用同一事务页 |

侧栏原有 Bridges、VLAN、Bond/LACP、STP/RSTP 全部指向 Ports。现在保留这些 IA 位置并明确为 P1 占位，点击给出可见说明，避免伪装成已实现的独立页面。Health 暂时进入已有 Overview；Jobs 进入活动事务或 Evidence。独立 Health/Jobs 资源页面仍待后续批次。

## 组件与样式

- 技术栈：Vinext 1.0.0-beta.5 / React 19.2.6 / TypeScript 5.9 / Tailwind 4.2；pnpm 锁文件保持不变。
- 原代码：产品页面、fixture、状态与 WebMCP 集中在 `app/page.tsx`；`components/ui/` 有 60 个现成 primitive，产品只直接使用 Badge、Button、Input、NativeSelect、Table 等少数组件。
- 原样式：Geist / Geist Mono、冷蓝基础色；globals 中存在重复 light/dark token 定义；业务 JSX 大量直接写颜色、10–11px 元信息和自制 panel。
- 本轮：保留依赖与基础控件；提取 `components/ovs/` 产品组件、`lib/ovs-model.ts` fixture、`lib/change-control.ts` 共享状态；整理 token，增加成功/警告/错误/未知语义。
- 遗留页面仍有固定浅色类；新组件提供双主题 token，但整站 Dark Mode 尚未完成视觉验收。本轮没有新增主题切换器。

## 异常覆盖：基线 → 本轮

| 状态 | 原基线 | 本轮可审查的行为 |
| --- | --- | --- |
| Loading | 没有资源加载视图 | Ports skeleton、busy/status、手动 fixture refresh |
| Empty | Workspace 空态；Ports 无搜索结果几乎无解释 | 区分真实 0 对象与筛选无结果；清除筛选 |
| Request error | 无独立视图 | 保留错误说明，刷新读取；不冒充空列表 |
| Permission denied | 外部对象仅无 Edit；无整页权限态 | inventory/detail 隐去对象详情；stage handler 拒绝 |
| Provider unavailable/degraded | Unknown 行、Observe 提示 | Unavailable 与 Empty 区分；Degraded 保留已知 inventory，明确缺失域 |
| Conflict | 漂移被标作 conflict，没有 base/current/mine | 独立三方视图；Keep current / Use mine / Cancel；新基线后重新验证 |
| Stale | 无独立状态 | generation 更新与不重叠修改；保留 VLAN 意图、重基、验证失效 |
| Drift | banner + 一键清场景 | 与 Candidate Stale 区分；通过只读 reconciliation fixture 返回新观测 |
| Validation failure | fixture 文案；UI 与 WebMCP gate 不一致 | 共享版本/场景/原因/设备 gate；Expert 不参与权限判断 |
| OutcomeUnknown | banner/页面，但没有实际核对结果；可由工具重复提交 | 锁住原事务；只读核对为 Applied / Not Applied / Degraded / Needs Attention |
| Network loss | 前端 timer 继续后直接声称已回滚 | 禁止确认/发送回滚；本地到期后结果未知；重连保留未知结果 |
| Rollback conflict | 没有独立状态 | compare-before-rollback 不匹配即停止；不暴露 Force rollback |
| Terminal Safe Apply | 完成后可重复操作；候选没有清除 | 确认后清候选；回滚后保留待复审意图；完成态不再出现确认按钮 |

上述均是显式合成 fixture 和共享前端状态，不是对真实 OVS 服务的测试。

## 与冻结设计的一致性

| 约束 | 结论 |
| --- | --- |
| 单节点；非 Controller/NMS | 不引入多节点选择或新控制器能力；修正 Fabric 标题为单节点措辞 |
| Bridge → Port → Interface；Bond 是 Port | 在 Ports/Detail fixture 中表达多成员关系；没有建立伪 Bond Interface |
| Candidate → Diff/Validation → Safe Apply → Evidence | 原型链路成立；修正候选身份、验证版本和重复提交门禁 |
| Standard/Expert | 共用视图与 handler；增加 authority、UUID、native field，不增加权限 |
| 服务端 per-user Candidate | **未实现**。当前一条候选、会话内状态；明确刷新重置，不用 localStorage 冒充服务端持久化 |
| OCC/CAS、field-level merge | **演示层实现**。三方选择与非重叠重基可审查；实际 CAS/重试/recheck 必须由后端承担 |
| Safe Apply 共享资源、服务器 timer、checkpoint、reconnect | **部分**。跨页共享/断线不误报已实现；服务端权威、刷新恢复、真实健康探测与 checkpoint 未实现 |
| Audit/Event/Job/Health 分责 | 结构化类型与 correlation 已有；持久化、权限、完整审计证据仍需后端 |
| Phase 1 / P1 capability 边界 | 没有新增 OpenFlow、DPDK、Offload 或主机写入；P1 计划的六批 gate 保持未决 |
| IA 五域 | 正式 IA 写明 Overview / Switching / Visibility / Operations / Administration；现有 shell 是 Overview / Switching / Operations / Evidence / System。记录为存量差异；本轮不重排 IA |
| 全局 Search / Health / Jobs / Changes | 本轮接好 Health/Jobs/Changes 固定入口；全局搜索和专门资源页未实现 |
| Reauthentication / 服务端 RBAC | **未实现**。当前权限/设备门禁为 prototype contract，不是生产安全边界；精确 capability code 未冻结 |

## 验证与审查结论

- `pnpm build`：Vinext 完成 client / SSR / RSC 构建；唯一实际 route 仍为 `/`。框架报告 route classification 为 Unknown 是其静态分类提示，不是构建失败。
- TypeScript：`node node_modules/typescript/bin/tsc --noEmit` 通过。
- 定向 lint：修改的产品页面、组件与模型通过；没有把整个 vendored primitive 目录纳入无关改造。
- 状态回归：`pnpm test:state`，14 项通过；覆盖空候选/未验证/无原因/窄屏、idle/terminal gate、只读 authority、无效 VLAN、身份保留、重复提交、冲突/非重叠重基、Unknown 锁、四种核对结果、断线超时、回滚冲突与验证失效。
- 本地 HTTP `/` 返回 200，预览已请求在 Codex 中打开。
- 未执行浏览器点击、截图或逐尺寸视觉 QA；当前任务没有明确要求浏览器测试，Sites 技能对此单独设限。焦点、语义控件和响应式结构已实现，但不能据此宣称浏览器视觉验收通过。

建议 disposition：**Detail Decision / Design System v0.1 review draft**。原型代码与基础状态组件可进入设计审查；Architecture/Phase 1 全量合规、P1 批次完成及后端事务安全仍不应标记 Accepted。
