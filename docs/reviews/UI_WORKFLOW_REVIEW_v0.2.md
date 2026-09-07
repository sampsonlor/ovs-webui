# OVS WebUI 交互修复与浏览器验收 v0.2

日期：2026-09-07（Asia/Shanghai）。起点：`63a629d`。分支：`codex/fix-windows-checks-ui-review`。

Disposition 更新：**2026-09-07 用户确认后，修复随 PR #9 合并至 `f5f6619`，接受基线为 `prototype-navigation-v0.1`。** 下列通过项仅针对本地合成原型，不提升既有 P1 批次、Architecture、Phase 1 或生产验收状态。本轮未开始 Batch 04；以下保留当次审阅范围，后续进度以 [STATUS](../STATUS.md) 为准。

## 修复内容

- 固定契约生成文件的 LF 换行，把生成代码排除规则放入 lint 配置，解决 Windows 下契约误报和脚本单引号兼容问题；保留真实生成内容漂移检查。
- Bridge Authority 选择器参与筛选；搜索无匹配时给出结果和清除入口；Bridge 名称支持键盘打开。
- Diagnostics 实际捕获对象、5/10/15 秒预算与输出格式；运行中锁定输入，后续草稿编辑不会更改已发起 Job 的对象与参数。重试使用原请求。
- 诊断结果依据捕获对象展示，其他对象不再复用 storage Bond 的成员故障事实；没有请求时不伪造已完成 Job。历史示例明确标为 synthetic review examples。
- 导出生成实际 JSON 文件；结构化格式不附加 raw output。取消、无数据、故障、过期与 unavailable 仍区别于健康结果。
- 修复 Ports / VLAN 同时高亮，通知可关闭，Allowed VLANs 输入标签不再包含整段帮助文字。

## 浏览器审阅方法

在本机 Windows 的 Codex 内置浏览器运行开发预览。正常路径通过可见按钮和表单完成；冲突、stale、drift、断线与 OutcomeUnknown 使用页面提供的 WebMCP review fixture 注入后，再检查实际页面和操作。它们不是后端故障注入。

桌面 1440 × 1000；平板 820 × 1180；手机 390 × 844。正常 Ports、平板摘要和手机 Safe Apply 做过截图目视检查。检查范围不包含完整读屏器、所有触屏设备、深色模式或 200% 缩放。

| 场景 | 浏览器证据 | 结果 |
| --- | --- | --- |
| Bridge Authority / 搜索 | External 只留下外部管理 Bridge；无匹配提示、清除过滤和键盘 Enter 打开详情 | 通过 |
| Diagnostic 参数 | 选择 Port/bond-uplink、5 秒、bounded，实际 Job 与 Expert raw 均保留该输入，未混入 storage 成员故障 | 通过 |
| 已提交诊断与草稿隔离 | 完成后改草稿为 Bridge/br-fabric、15 秒、structured；打开 latest Job 仍为原对象与参数 | 通过 |
| 尚未运行诊断 | Open latest job 不可用 | 通过 |
| P0 正常路径 | server-07 → VLAN Trunk 120,240 → Candidate → 验证 → 理由 → Safe Apply | 通过 |
| 验证 / 理由门禁 | 验证前和缺少理由时不能发起 Apply；验证失败时 Standard / Expert 均不能越过 | 通过 |
| 手机已有 Safe Apply | 确认已有事务，终态 Confirmed、Candidate 清空、不能重复确认；无横向溢出 | 通过 |
| 手机新配置 | Ports 显示 incident companion 摘要，不提供桌面配置面 | 通过 |
| 平板职责 | Ports 显示摘要卡片；可打开 VLAN 表单但不能 Add to workspace | 通过 |
| Conflict | Base / Current / Yours 可见；Use my value 保留意图并要求重新验证 | 通过 |
| Stale | 非重叠 rebase 后 VLAN 意图保留，重新验证 | 通过 |
| Drift | 单独显示漂移，读取核对保留 Candidate，不触发 Apply | 通过 |
| Network loss | 已有事务的 Confirm configuration 禁用 | 通过 |
| OutcomeUnknown | 切回 Normal 仍显示未知事务和原 Job，确认入口不出现；重复提交锁另由自动测试覆盖 | 通过 |
| 四种 reconciliation 结果 / rollback conflict | 自动状态及本地服务测试覆盖；此次浏览器未逐项完成 | 不计为浏览器通过 |
| 通知 / 标签 | 通知可以关闭；VLAN 输入标签与帮助说明分离 | 通过 |
| 诊断结构化输出 | 完成 5 秒请求后切换 Expert，仍只展示请求的结构化结果 | 通过 |
| 诊断下载 | Export result 执行后显示文件名、格式、对象反馈，无浏览器控制台错误；内置浏览器未返回下载事件 | 交互通过，文件落盘未确认 |

## 本地持久化浏览器验收

使用 `pnpm dev:lab --port 3001`，通过页面中的开发用户与 observation fixture 检查。本轮使用新建的合成 lab 数据，保留在忽略目录 `.ovs-lab/`；没有实际网络设备参与。

| 路径 | 证据 | 结果 |
| --- | --- | --- |
| 保存 / 刷新恢复 | Editor A 保存 server-07 的 Trunk 120,240；刷新后 Changes 仍为 1，Candidate 对象和意图一致 | 通过 |
| 用户隔离 | 切换 Editor B，Changes 为 0，不出现 Editor A 的 Candidate；切回 A 可恢复 | 通过 |
| Read-only / Expert | 只读用户的 Edit VLAN 禁用；Expert 不改变该限制 | 通过 |
| 安全能力缺失 | 默认安全能力 unavailable 时服务端验证为 blocked；重新打开页面仍保留阻断结果 | 通过 |
| 安全能力恢复 | 明确选择 synthetic safety available 并重新验证，结果 passed；未直接应用配置 | 通过 |
| 活动事务刷新恢复 | 填写理由并 Apply safely；刷新后仍可打开原服务端操作和确认窗口 | 通过 |
| 确认与证据 | Confirm connectivity 后 Candidate 清空、重复确认入口消失；证据页显示 confirmed、提交、Applied、probe 和审计理由 | 通过 |

开发预览在热更新期间输出过 React 多 renderer 警告；最终构建通过，持久化审阅会话的浏览器控制台未发现 error。该记录不等于生产运行时或跨浏览器验收。

## 自动验证

| 检查 | 结果 |
| --- | --- |
| TypeScript | 通过 |
| 产品范围 lint | 通过 |
| 生成契约一致性与回归 | 83 项通过 |
| 独立 HTTP / 进程恢复集成 | 3 项通过 |
| 生产构建 | 通过；Vinext 保留既有路由静态分类 Unknown 与插件耗时提示 |

测试报告位于忽略目录 `test-results/`。没有新增依赖或改变 `pnpm-lock.yaml`。未更新托管部署或 `.openai/hosting.json`。

## 基线入库和待处理项

Architecture 原文已原样归档到 `docs/baselines/OVS_WebUI_Architecture_Baseline_v1.0.1.docx`，SHA-256 `27638d3bca2bb5f0b657c0fab462c254a19b6136df9c79f148de369de44d6c42`。根目录的 IA 原文是 Draft for Review，与仓库 Approved Baseline 不同，未覆盖批准版；用户提供的根目录文件保持原样。

以下仍开放：批准 IA 的五域导航映射、P1 高保真组件迁移、深色与缩放完整验收、自动浏览器 CI、正式 Go / Svelte 技术栈落地、真实授权与 OVS provider、Architecture Release Gate。独立 Phase 1 Scope 原文仍待补齐；详见[当前进度](../STATUS.md)。现有浏览器 CI 和基础组件 lint 议题不因本轮手工验收自动关闭。

本次没有逐页完成所有 P1 异常、四种 reconciliation 和 rollback conflict 的浏览器矩阵；下载文件落盘也需要后续在支持下载验收的浏览器中确认。因此本记录只接受上述具体检查结果，不声明整站浏览器验收完成。
