# 批准 IA 导航对齐 v0.1

日期：2026-09-07（Asia/Shanghai）。关联：[PR #9](https://github.com/sampsonlor/ovs-webui/pull/9)、[议题 #10](https://github.com/sampsonlor/ovs-webui/issues/10)。

Disposition 更新：**2026-09-07 用户确认后，PR #9 已合并至 `f5f6619`，合并后 CI 通过，接受基线标记为 `prototype-navigation-v0.1`。** 本次整理既有原型入口，不接受新的 P1 review gate，也不声明完整 Page Inventory 已实现。以下保留本次实现时的检查范围和余项；后续进度以 [STATUS](../STATUS.md) 为准。

## 页面归属

依据仓库内 Approved Baseline 的 UI Information Architecture v1.0 §4.1–4.2。桌面侧栏与窄屏菜单共用 `PrototypeNavigation`，模式、候选和事务继续由既有共享状态管理。

| 入口 | 当前原型映射 |
| --- | --- |
| Overview | Dashboard |
| Switching | Switching overview、Bridges、Ports、Bonds/LACP、OpenFlow；对象详情与编辑页高亮所属资源 |
| Visibility | Planned，尚无该域的独立页面 |
| Operations | Diagnostics；Events / Audit 指向现有合并证据页，不冒充两个完整资源列表 |
| Administration | Planned，尚无该域的独立页面 |
| Global Change Control | Changes 徽标进入 Candidate；Workspace、Diff/Validation、Safe Apply 使用独立变更上下文；活动事务仍有全局横幅 |

VLAN-wide inventory 和 STP/RSTP 专页明确标为 Planned、不可点击。Port 详情中的 VLAN 编辑和 Bridge 详情中的 STP/RSTP 摘要保留。Interfaces 作为独立资源入口只在 Expert 出现，当前仍为 Planned；Standard 的对象关系展示不受影响。响应式职责说明保留在 Device responsibility 辅助入口。

这只是已实现页面的导航映射。IA 中其他未实现页面没有被此次菜单覆盖自动补齐；特别是 DPDK/Offload、System Health、Capabilities 仍按后续批次处理。

## 浏览器检查

使用本机开发预览与 Codex 内置浏览器，通过可见按钮和表单操作。截图做过目视检查；本轮未新增浏览器 CI。

| 场景 | 观察结果 |
| --- | --- |
| 桌面 1440 × 1000 | 五个一级域顺序正确；Planned 标识可读；OpenFlow 属于 Switching |
| 键盘 | Operations 按钮用 Enter 打开 Diagnostics，保留焦点、2 px 实线焦点框和活动状态 |
| Standard / Expert | Standard 不显示独立 Interfaces 入口；Expert 显示且禁用；未改变权限或验证逻辑 |
| Operations | Diagnostics 和 Events / Audit 使用同一域的二级导航，证据仍来自原共享资源 |
| Port / VLAN | server-07 详情可打开 Edit VLAN；编辑页只高亮 Ports，不把 VLAN-wide 页面标为已实现 |
| 跨域 Candidate | Access VLAN 120 → 240 加入 Candidate；往返 Operations 和 Changes 后对象、revision 1 和意图保持一致 |
| 全局变更上下文 | 从独立 Diff/Validation 入口验证并填写理由，经确认对话框发起合成 Safe Apply；不会直接保存运行配置 |
| 跨域事务 | 切到 Operations 后 Awaiting confirmation 横幅及 Open transaction 入口仍可见 |
| 手机 390 × 844 | 五域及 Operations 二级入口显示正常；诊断保持 incident companion；可从全局入口回到已有事务；无横向溢出 |
| 到期保护 | 上述事务在检查期间到期，确认操作已不可用；显示 Rolled back，原 VLAN 恢复，Candidate 保留供重新验证 |
| 平板 820 × 1180 | 使用同一导航；Ports 为摘要卡片；VLAN 表单说明新配置需桌面，未提供 Add to workspace |
| 手机 320 × 740 | 改为单列菜单，避免 Administration 被拆成孤立字符；页面及导航无横向溢出 |
| 浏览器错误 | 导航审阅会话未观察到控制台 error |

本轮到期后没有把未完成的确认点击记为通过；手机确认成功的既有证据见[交互验收 v0.2](UI_WORKFLOW_REVIEW_v0.2.md)。冲突、drift、未知结果和 lab 刷新恢复沿用该记录的具体范围，此次没有重复宣称完整异常矩阵通过。

## 工程检查与余项

`pnpm typecheck`、`pnpm lint:ci`、`pnpm build` 本地通过。构建仍有 Vinext 的既有路由分类 Unknown 与插件耗时提示。PR 的完整回归和隔离集成结果以其 Checks 为准。

[议题 #7](https://github.com/sampsonlor/ovs-webui/issues/7) 继续跟踪浏览器 CI；深色、200% 缩放、下载完成及剩余异常组合仍未完成。[议题 #6](https://github.com/sampsonlor/ovs-webui/issues/6) 的通用模板 lint 清理仍开放。下一步审阅本次导航映射，然后继续既有 P1 页面高保真组件统一；新增 P1 批次遵守既定顺序。
