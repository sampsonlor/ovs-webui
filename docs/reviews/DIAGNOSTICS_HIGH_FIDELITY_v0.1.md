# Diagnostics 高保真收口 v0.1

日期：2026-09-07（Asia/Shanghai）。起点：`5218641`，分支：`codex/feat-p1-diagnostics-polish`。上轮 Bridge / Bond 已获用户确认，PR #11 合并并以注释标签 `prototype-bridge-bond-v0.1` 保留。

Disposition：**实现与下列检查完成，待用户审阅本轮高保真结果。** 范围是已接受的 P1 Batch 02 两页组件统一，不新增 P1 批次，不提升正式后端或生产验收状态。

## 页面与交互

覆盖 Diagnostics hub 和 Diagnostic Job / Result。原 `app/p1-diagnostics.tsx` 保留兼容导出，页面移入 `components/ovs/diagnostics-pages.tsx`，模板与结果语义集中于 `lib/diagnostics-model.ts`。复用现有 PageHeader、Notice、StatusBadge、表格、表单及 Progress，采用相对字号和语义色。

- 目录提供搜索、分类、选中反馈、可用性和执行预算。桌面显示 5 个模板；平板保留 3 个推荐模板，空筛选给出可用的清除入口。
- 网络快照允许单个 Port 或 Bridge；预定义 packet trace 与 OpenFlow collection 要求 Bridge。输出范围为结构化结果及模板允许的有界文本，没有任意命令或自定义脚本入口。
- 模板切换不静默改写已有参数。OpenFlow 的 10 秒 timeout 只允许 5/10 秒预算和结构化结果；其他模板的 5/10/15 秒预算受 20 秒 timeout 约束。网络与 trace 的文本上限分别为 200 / 300 行，输出字节上限为 64 KiB。
- 提交捕获原始对象和参数；运行中锁定输入，取消要等待 safe checkpoint。重试使用已捕获请求，独立于下一份草稿的对象、预算或模拟输入错误。原子 metadata 模板没有取消操作。
- Request、Execution、Result、Evidence 分别呈现。执行中使用不确定进度；失败、取消、无数据、过期和结果不可读不会显示绿色完成进度或产生健康结论。Complete 与对象健康分开。
- storage Bond 的已知 carrier down 与缺失 counters 分开；其他对象不复用 storage 故障。结构化请求不展示或导出 raw text；文本示例标记实际节选行数，不伪造“已输出上限行数”。
- 合成结果预览有明确标识，暂停自动演示，不创建 Event / Audit，也不能取消一个并未提交的 Job。普通诊断的请求、运行和结束仍进入共享证据；打开证据仅导航。执行期间的 completion Event 标为 pending，Audit 可独立查看。
- 提交、取消和预览检查共享资源的当前状态，避免紧接状态切换时读取上一帧；Expert 不改变权限、可用性或校验。未知关联对象给出缺少详情样例的反馈，不跳到其他对象。

所有配置仍通过 Candidate → Diff / Validation → Apply / Safe Apply → Event / Audit。Diagnostics 不修改配置意图，不解除已有 OutcomeUnknown 事务锁。

## 浏览器审阅

在 Windows 的 Codex 内置浏览器通过本地合成预览、可见表单和页面公开的 WebMCP review 工具检查。异常结果是明确的 review fixture，不是对真实 OVS provider 的故障注入。

| 场景 | 实际观察 | 结论 |
| --- | --- | --- |
| 桌面 1440 × 1000 | 目录、选中操作、执行限制、生命周期、结果覆盖与证据侧栏使用一致组件 | 本轮通过 |
| 正常运行 / 取消 / 重试 | Port/bond-storage、15 秒、bounded；取消先显示 Cancel requested，随后 Cancelled；后续草稿改成 Bridge、5 秒、structured 并模拟 Validation error，重试仍保留原请求 | 通过 |
| 运行中的跨页输入 | 目录 target 和其他模板按钮禁用，不能替换活动 Job 的输入 | 通过 |
| OpenFlow 参数限制 | Port、15 秒和 bounded 分别显示错误；改为 Bridge/br-fabric、5 秒、structured 后可运行，无取消按钮，完成后无 raw 面板 | 通过 |
| Standard / Expert | 结构化结论与门禁一致；Expert 可看已请求的文本；Host permission-denied 模板在两种模式均不可运行 | 通过 |
| 搜索 / 键盘 / 焦点 | Enter 选择模板，无匹配有 Clear filters；Tab 到 Run 后有可见焦点，字段有有意义的标签和错误关联 | 抽样通过 |
| 结果状态矩阵 | 检查 not-started、queued、running、cancel-requested、cancelled、complete、failed、partial、truncated、expired、unavailable、no-finding、no-data、provider-unavailable、command-failed、evidence-unavailable | 通过；输出和进度仅出现在适用状态 |
| 部分结果 / 证据隔离 | partial 保留 carrier down，raw 的 counter_sample 为 unknown；正常 Event 导航前后证据均为 7 条，多次预览也未增加；预览的 Event / Audit 禁用，快速预览后取消被阻断 | 通过 |
| 服务资源异常 | Loading、Empty、Error、Permission denied、Provider unavailable 分别呈现；切换状态后立即通过程序入口提交，全部被当前门禁阻断；断线取消也被阻断 | 通过 |
| 缓存 / Provider degraded | 断线保留带保存快照说明的原 Job；降级状态允许有界采样，演示结束显示 Partial result | 通过 |
| OutcomeUnknown 隔离 | 合成 Bond 事务 job-2047 进入未知状态后运行诊断；诊断完成前后 Candidate 和 transaction 完全一致，切回 Normal 后事务仍未知 | 通过 |
| 对象深链接 | 结果返回 bond-storage；Bond 的 Run diagnostic 传入 Port/bond-storage 并保留来源提示 | 通过 |
| 平板 820 × 1180 | 3 个推荐模板卡片，保留有界参数和运行能力；既有配置事务提示仍可见 | 通过 |
| 手机 390 × 844 | 只显示 Diagnostic Job 的 incident companion、范围及证据入口；UI 无启动操作，程序入口拒绝新诊断，页面无横向溢出 | 通过 |
| 深色与 200% 文字 | 临时启用已有 dark 样式并将根字号从 16 调为 32 px，检查目录、表单及 partial 结果；修复多行 Run / Retry 按钮高度，页面无横向溢出 | 本切片抽样通过 |
| 导出 | 自动回归核对 JSON 内容、原请求副本、结构化/文本限制与 preview 标记；点击后呈现 Export prepared | 下载完成未确认；内置浏览器未返回 download 事件 |

深色 / 根字号设置已原样恢复 `app/layout.tsx`。根字号测试不等同于浏览器缩放。放大文字时二维目录表格保留容器内横向滚动，原生选择器可截短长值，完整请求在结果中换行展示；既有固定宽侧栏仍会拆分导航单词。整站缩放、完整读屏器和跨浏览器矩阵继续待办。

## 工程验证与边界

类型检查、产品范围 lint、99 项回归、3 项独立 HTTP / 进程恢复集成及 `pnpm build` 通过。新增 8 项回归覆盖模板限制、参数拒绝、结果完整性、输出契约、证据可用性和服务/设备门禁。构建保留 Vinext 既有路由静态分类 Unknown 和插件耗时提示。

Job 与 correlation ID 沿用已接受的固定 review fixture，刷新重置会话；不是正式持久化 Job 服务。采样、timeout、输出 ceiling 和 safe checkpoint 目前是模板契约与合成演示，正式执行器必须再次执行限额和授权。未接入真实 OVS / OpenFlow 采集、manager Audit 或 Go 双进程管理平面。导出按钮的本机下载完成、完整拓扑库存与正式 provider 故障恢复仍需后续验证。

无新增依赖，`pnpm-lock.yaml` 与 `.openai/hosting.json` 未修改，未部署或改变站点访问。浏览器 CI [#7](https://github.com/sampsonlor/ovs-webui/issues/7) 和通用模板 lint [#6](https://github.com/sampsonlor/ovs-webui/issues/6) 继续开放。接受本轮后，下一步是现有 OpenFlow Viewer 的组件统一；新增 Batch 04–06 仍遵守既定顺序与 review gate。
