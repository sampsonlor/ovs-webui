# Bridge / Bond 高保真收口 v0.1

日期：2026-09-07（Asia/Shanghai）。起点：`f5f6619`，分支：`codex/feat-p1-bridge-bond-polish`。

Disposition 更新：**2026-09-07 用户确认继续 Diagnostics 后，PR #11 合并至 `5218641`，注释标签 `prototype-bridge-bond-v0.1` 保留接受基线。** 这是已接受的 P1 Batch 01 六页的组件统一，不是新的批次验收，也不提升 Architecture、Phase 1 或生产验收状态。以下保留当次审阅范围，后续进度以 [STATUS](../STATUS.md) 为准。

## 范围与变更

覆盖 Switching overview、Bridge list/detail、Bond list/detail、Bond create/edit。页面使用既有 PageHeader、Notice、StatusBadge、表格和表单组件及浅色/深色语义色；主要文字使用 14 px 起的相对字号，次要说明为 12 px。原 `app/p1-switching.tsx` 保留兼容导出，页面与合成观测模型分别移入 `components/ovs/switching-pages.tsx`、`lib/switching-model.ts`。

- 显式展示 Bridge → Port → Interface；Bond 是一个含成员 Interface 的 Port。对象名称用可键盘操作的按钮打开，父 Bridge、Bond 和诊断范围同步选择。
- 列表、详情和成员卡片使用同一份观测推导。外部控制的 Bond 在正常、provider degraded 场景均不伪造链路、角色、LACP 或流量；失效证据另标为 stale。
- active-backup 使用活动成员容量，备用成员流量为零；成员 down 时不改写配置成员。LACP mismatch 保留已知 Link Up，但容量为 Unverified，流量为 Unknown，角色显示 Collecting only / Detached。
- Bond 表单检查本地样例中的名称、父 Bridge、成员占用、成员数量、LACP 组合与 minimum links。错误关联到输入；无实际差异不能提交。未知 minimum links 不变成 0，高级原生字段保留在 before/after 中。
- 共享事务门禁同时驱动按钮状态和 staging reducer；已有事务锁、失去权限/连接及尚未接入 lab 的 P1 写入提前显示原因。Standard / Expert 不改变权限或验证门禁。
- Diff 的验证标题按 VLAN、Bridge、Bond 显示正确对象，不再对 Bond 展示 VLAN schema 文案。

没有增加直接保存运行配置的入口。Bridge / Bond 继续进入 Candidate → Diff / Validation → Safe Apply → 关联证据；正式 payload、provider、Go / Svelte 管理平面不在此切片中。

## 浏览器审阅

使用 Windows 本地合成开发预览与 Codex 内置浏览器。下面的正常与异常场景通过可见按钮、表单和 Review state 选择器操作，不是后端故障注入，也未增加浏览器 CI。

| 场景 | 观察结果 | 结论 |
| --- | --- | --- |
| 桌面 1440 × 1000 | 六页的摘要、对象层级、详情侧栏、表单、成员表格与已有设计系统一致 | 本轮通过 |
| 搜索 / Authority | Bridge 无匹配时给出清除入口；External 只显示 br-offload；Bond 的 br-mgmt 过滤明确无结果 | 通过 |
| 键盘 / 表单标签 | Enter 打开 Bridge / Bond；Space 改变成员选择；名称、LACP、minimum links 的错误有 aria-invalid 和关联说明 | 通过 |
| 新建 Bond | br-fabric 的已占用成员禁用，可选择空闲成员；重复名称、少于两成员、无效 LACP/minimum links 均阻断提交 | 通过 |
| 现有 Bond 无变更 | 初始值显示 No changes to stage，提交禁用 | 通过 |
| Advanced / 模式比较 | Standard 提示先审阅高级原生字段；Expert 保留 bond-rebalance-interval=10000，修改 minimum links 1→2 后 before/after 均保留该字段 | 通过 |
| Bond 正常事务 | 经表单进入 Candidate，验证、填写理由、复核并 Safe Apply；确认后 Candidate 清空，证据保留 Port/bond-uplink 与 corr-demo-1 | 通过 |
| Bridge 正常 staging | br-storage 的 rstp_enable false→true 进入 Candidate，未直接修改运行配置 | 通过 |
| OutcomeUnknown / 跨页锁 | Bridge 事务 job-2048 进入未知结果；切回 Normal 和 Bridge 列表/详情后仍锁定新建、RSTP、Add bond | 通过 |
| 平板已有事务 | 820 px 可打开并读取原 job-2048；Applied reconciliation 使用原相关 ID，无重复提交 | 通过 |
| 成员 down | br-storage 降级；活动成员 25 Gbps，备用成员 Down / Inactive / 0 bps，配置仍为两成员 | 通过 |
| LACP mismatch | 状态 Degraded / Partner mismatch；Expert 可准备纠正意图，但 Diff 中 Validate 和 Apply 仍禁用 | 通过 |
| Provider / Unknown | 外部 Bridge 的 RSTP 为 Unknown；外部 Bond 的成员链路、角色、LACP、流量均 Unknown，provider degraded 额外显示 stale；Expert 无编辑入口 | 通过 |
| Loading / Empty / Error / Permission / Unavailable | 加载骨架、成功空数据、读取失败、权限不足和 provider 不可用分别呈现；权限不足时不展示对象详情 | 通过 |
| 平板 820 × 1180 | 列表/成员改为卡片；已打开的表单保留内容且禁止新配置；Bond 详情保留诊断入口 | 通过 |
| 手机 390 × 844 | Switching 只保留 incident companion、对象范围和证据入口，无新配置入口、无页面横向溢出 | 通过 |
| 深色样式 | 临时在本地布局启用已有 dark 样式，目视检查 Bond / LACP 异常、信息层级和状态标识 | 抽样通过 |
| 200% 文字 | 临时将根字号从 16 调为 32 px，检查 Bond 详情和编辑页；修复属性/检查栏换行，页面无横向溢出，二维表格与原生代码块可在各自容器横向滚动 | 本切片抽样通过 |

深色和放大文字使用临时源文件审阅设置，已原样恢复 `app/layout.tsx`，没有新增主题开关。根字号测试不等同于浏览器 200% 缩放；既有固定宽度侧栏在放大文字时仍会将导航词拆行，整站缩放、所有浅/深色页面组合、读屏器和跨浏览器矩阵仍待完成。

## 工程验证与限制

类型检查、产品范围 lint、91 项回归、3 项独立 HTTP / 进程恢复集成及 `pnpm build` 通过。构建保留 Vinext 既有的路由静态分类 Unknown 与插件耗时提示。新增 8 项回归覆盖未知证据、容量/角色语义、成员占用、字段限制与共享 staging 门禁。既有测试继续覆盖拓扑事务、drift/rebase、四种 reconciliation 与受保护回滚，本轮不把所有这些组合计为浏览器通过。

合成库存依然是局部页面样例，详情只列代表性 children；尚未统一为一份跨 P0/P1 的完整、唯一拓扑库存。静态观测样例不会由合成 Apply 自动刷新。P1 Bridge / Bond 未接入持久化 lab；这些限制不能由高保真呈现或本地字段检查替代。

无新增依赖，`pnpm-lock.yaml` 保持不变。未修改托管、站点权限或 `.openai/hosting.json`。自动浏览器 CI 继续由 [#7](https://github.com/sampsonlor/ovs-webui/issues/7) 跟踪，通用模板 lint 继续由 [#6](https://github.com/sampsonlor/ovs-webui/issues/6) 跟踪。

本轮审阅后，下一步是既有 Diagnostics 页的高保真组件统一；新增 P1 Batch 04–06 继续遵守既定顺序和独立 review gate。
