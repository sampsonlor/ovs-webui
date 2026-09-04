# OVS WebUI Design System v0.1

状态：Review draft。应用起点：Switching → Ports。沿用 P0 页面与现有冷蓝色基础，不变更批准 IA。

## 视觉原则

紧凑、安静、精确。端口、VLAN、authority、generation 和事务状态优先于装饰。状态始终同时给出文字与图标；颜色不承担唯一含义。元信息与主任务层级清晰，避免把 mock 数据或等待核对的结果绘成实时健康。

## Foundation

| 项目 | v0.1 规范 |
| --- | --- |
| 字体 | 现有 Geist；名称、UUID、generation、计时使用 Geist Mono |
| 字号 | 页面标题 28px；常用界面/表格/动作 14px；说明正文按密度使用 14–16px；次要元信息 12px |
| 间距 | 4 / 8 / 12 / 16 / 24 / 32px，对应 `--ovs-space-*` |
| 控件 | 默认高度 40px；紧凑 header 控件遵循现有 Button size 变体 |
| 表格 | 桌面行高 72px；双行 Port / Interface；单行字段与范围列对齐；数值等宽 |
| 布局 | Sidebar 224px；内容最大 1480px；页面按 16–32px 边距调整 |
| 边框与圆角 | 1px 结构边框；6px 基础圆角；仅轻微层级阴影 |
| 焦点 | 2px ring + 3px offset；保留键盘进入对象与原生控件行为 |
| 动效 | 短状态转换；尊重 reduced-motion；计时不逐秒播报给读屏器 |

### 语义颜色（浅色）

| Token family | 文字 / 背景 / 边框 | 含义 |
| --- | --- | --- |
| info | `#185879` / `#edf6fb` / `#a9cbdc` | 可检查的信息、候选变更、等待确认 |
| success | `#166342` / `#edf8f1` / `#abd5bb` | 已验证的成功结果 |
| warning | `#854516` / `#fff7e8` / `#dfbe7d` | 风险、Stale、Drift、部分数据 |
| danger | `#a42d3b` / `#fff1f2` / `#e9adb5` | 确定的阻塞、字段冲突、回滚冲突 |
| uncertain | `#60468b` / `#f5f1fb` / `#c5b4de` | 缺少权威证据、OutcomeUnknown；不等同于失败 |
| neutral | `#4b5e71` / `#f1f4f7` / `#c1cbd6` | 尚未检查、Observe、一般元信息 |

深色对应值在 `app/globals.css`。新增组件通过 token 使用颜色；不再各写一份红/黄 banner。遗留页面的浅色硬编码是后续迁移项，整站深色模式未验收。

## 本轮组件

| 文件 | 产品组件与职责 |
| --- | --- |
| `components/ovs/foundation.tsx` | PageHeader、ScopeBadge、StateDot、StatusBadge、Notice |
| `components/ovs/ports-page.tsx` | PortsPage：搜索、状态/范围筛选、列显示、桌面表格、平板摘要、资源异常 |
| `components/ovs/vlan-editor.tsx` | VLAN intent form、类型相关字段、范围反馈、Standard/Expert 预览 |
| `components/ovs/transaction-states.tsx` | CandidateChangeCard、ConflictViewer、GenerationWarning、OutcomeUnknownPanel、RollbackTimer、TransactionBanner |
| `components/ovs/change-pages.tsx` | 原 Workspace / Diff / Safe Apply / Evidence 视图的组件组合 |
| `lib/change-control.ts` | UI/WebMCP 共用的演示资源与状态转换；只在这里决定提交、确认、回滚与核对的合法性 |
| `lib/ovs-model.ts` | 既有 Port fixtures、单/多 Interface 关系、VLAN 可读标签与规范化原生字段预览 |

复用现成 Button、Badge、Input、NativeSelect、Table、Checkbox、Popover、Dialog、Alert、Skeleton。没有修改 `components/ui/` 或增加 UI 依赖。

## Ports 规范

第一屏直接呈现端口清单：页名 → 库存摘要 → 搜索/筛选 → 表格。库存分 Up / Down / Unknown；Unknown 不转换为 Down。筛选会影响实际列表；没有结果时区分“权威读取返回零行”和“没有匹配筛选的行”。Refresh 只改变资源 fixture，不解除已存在的事务锁。

Standard 固定呈现 Port 身份、Link、VLAN、管理范围，默认带 Speed 与 Bridge。Columns 可调整 Speed、Bridge。Expert 在相同清单和相同行上增加 authority/provider 与 Port UUID；不改变写入 gate。平板使用摘要行，避免强迫用户用横向大表完成核心任务。

对象入口是可聚焦 button。Observe 对象仍可打开；详情不显示合法性不足的 Edit 动作。Bond 行显示 member Interfaces，详情保持 Bridge → Port(Bond) → Interfaces。

## Change Control 状态语义

### Candidate

本轮保持 P0 一条意图的边界。Candidate 包含对象、base/mine、baseGeneration、revision；切换浏览对象不会改掉已暂存意图。写入不同对象时不静默覆盖已有候选。原生映射是候选窄字段的展示，不是完整 OVSDB transaction 或 API payload。

Validate 必须针对当前 revision。修改、重基、观测场景变化会让之前的验证失效。Apply 同时要求：存在候选、无活动/未知事务、authority 可写、VLAN 输入合法、generation 当前、当前 revision 验证通过、Audit reason 非空、desktop 允许。

### Conflict / Stale / Drift

- Conflict：相同 VLAN 字段发生并发变化。三列必须是 Base / Current system / Your change。Keep current 去掉本条意图；Use mine 仅保留候选值并改用新的 base；Cancel 保持未解决状态。没有 Force Apply。
- Stale：candidate base 已旧，但样例里的 MTU 外部变化不与 VLAN 意图重叠。允许合并后重新验证，不制造字段冲突。当前前端只保存 VLAN 窄意图与 generation；完整 field-level merge 是后端责任。
- Drift：desired 与 observed 不一致。默认动作是读取并 reconciliation，不能将它简单当作 candidate revision 过期。

### OutcomeUnknown

未收到完成证据不等于失败，也不等于没有提交。保留原 Job/transaction/correlation，只允许读取后核对。页面展示连接、generation、提交意图、ovs-vswitchd applied state 四个检查域。

核对只收敛到：Applied、Not Applied、Partially Applied / Degraded、Needs Attention。前两类给出明确后续；后两类继续锁住新提交。没有 Retry transaction。面板内折叠的 fixture 选择器用于演示不同读响应，不是给真实操作员选择实际结果的控件。

### Safe Apply

审查使用现有 Dialog primitive：受影响 Port/Bridge、管理链路风险、checkpoint、connectivity probe、compare-before-rollback、90 秒窗口。90 秒继承 P0，只是 fixture 值，不作为架构冻结参数。

启动后在所有页面的固定区域显示事务状态。处理页显示 Committed / Applied / Health checked / Awaiting confirmation；成功图标只属于当前 fixture 已提供的阶段。断线时这些阶段转为未验证，按钮停用，时间是本地估算。浏览器显示的截止时间不能证明服务端已经完成回滚。

确认、立即回滚、超时回滚均要求活动事务。完成后不再显示确认按钮。compare-before-rollback 遇到外部写入必须停止，保留明确的 Rollback conflict 和证据入口。

## 响应式与可访问性

| 宽度 | 本轮职责 |
| --- | --- |
| ≥1024px | 完整 Ports / VLAN / Candidate / Diff / 新 Safe Apply |
| 768–1023px | Standard/Expert 读取、摘要清单、候选审查、已有 Safe Apply |
| <768px | incident companion、Evidence、已有 Safe Apply；不创建新的配置事务 |

设备限制同时应用于 UI 与 WebMCP handler，不只用 CSS 隐藏按钮。它仍然是原型职责约束，不能替代生产 RBAC。Modal 焦点由既有 Base UI Dialog 管理；table caption、输入 label、focus-visible、Skip to content 和非颜色状态提示均明确提供。具体浏览器/读屏器与 200% 缩放验收仍待执行。

## 审查路径

1. **正常路径**：Ports → server-07 → Edit VLAN → 选择 Trunk、填 `120, 240` → Add to workspace → Review diff → Validate candidate → 填原因 → Review & apply → Apply safely → Confirm configuration → Evidence。
2. **字段冲突**：先暂存；Review state 选 Candidate conflict → Changes → 检查三个值 → 分别尝试 Keep current 或 Use mine；后者必须重新 Validate。
3. **Stale**：暂存后选 Stale candidate → Changes → Review & rebase non-overlapping change → 验证失效但 VLAN 意图不丢失。
4. **Drift**：暂存后选 Configuration drift → Changes → Reconcile observed state → 返回新观测后重新验证。
5. **OutcomeUnknown**：先按正常路径启动 Safe Apply，再切换 Outcome unknown → 原事务处理页；核对面板的折叠 fixture 可选择四个读响应。结果未明确时切回 Normal 也不能解除事务锁。
6. **断线/超时**：活动 Safe Apply 中选 Network loss → 确认/回滚停用；等到本地到期显示结果未知 → 重连后核对原事务。
7. **回滚冲突**：活动 Safe Apply 中选 Rollback conflict → Roll back now → 停止回滚，转证据。新提交保持阻塞。
8. **资源状态**：Ports 中切换 Loading / Empty / Error / Permission denied / Provider unavailable / Provider degraded；检查无数据、读失败、未知字段没有混为一谈。
9. **模式一致性**：切换 Expert，检查原 Standard 信息仍在、UUID/authority 增加、所有写入 gate 结果相同。

所有状态属于合成的会话内 prototype，刷新会重置。Candidate 服务端持久化、Job 恢复、实际 checkpoint/probe、OCC/CAS、reauth 和后端接口接入是后续工作，不用浏览器本地存储模拟成已完成。
