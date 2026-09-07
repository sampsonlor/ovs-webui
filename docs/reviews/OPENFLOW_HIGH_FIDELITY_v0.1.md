# OpenFlow Viewer 高保真收口 v0.1

日期：2026-09-07（Asia/Shanghai）。起点：`ea48136`，分支：`codex/feat-p1-openflow-polish`。Diagnostics 已获用户接受，PR #12 已合并，注释标签 `prototype-diagnostics-v0.1` 保留接受基线。

**Disposition：实现与本轮验证完成，等待用户审阅。** 范围是已接受 P1 Batch 03 的单个 Observe Viewer，不新增批次，不扩大 OpenFlow 写权限，也不代表真实 provider 或生产验收完成。后续进度以 [STATUS](../STATUS.md) 为准。

## 页面与快照语义

原 `app/p1-openflow.tsx` 保留兼容导出。页面移入 `components/ovs/openflow-page.tsx`，采集状态放入随共享 P1 控制器挂载的 `openflow-controller.ts`，查询、样例、限额和导出规则集中于 `lib/openflow-model.ts`。复用 PageHeader、Notice、StatusBadge、表格、表单及可访问 Tabs，使用语义色和相对字号。

- 初始状态为 Not collected。查询草稿与采集结果分开；修改 Bridge 或过滤条件不会改写旧快照、详情和导出身份。刷新使用快照捕获的条件，Run 使用当前草稿。跨页返回保留采集结果。
- br-fabric 与 br-storage 各有独立流表样例、端口关系和协议元数据；br-offload 声明 netdev，采集始终返回 Provider unavailable，协议与 OpenFlow authority 不伪造为 fabric 的值。OVSDB 对象归属与 OpenFlow 控制权分别说明。
- 采集仍是合成演示：约 450 ms 的响应和 5 s 超时门限。提交与完成都检查当前共享服务状态和设备尺寸，重复请求被锁定；权限撤销会清除快照，旧回调不会把数据填回页面。恢复权限后需要重新采集。
- 每次结果最多 500 行，完整 JSON 导出最多 256 KiB，并为动态新鲜度和当前服务状态预留空间。截断 fixture 有 768 条独立匹配；限定 Table 10 后返回 192 条完整结果。行数和截断原因由实际保留结果计算。
- 每份快照都有真实采集时间，30 s 后自动显示 Stale。新鲜度与覆盖范围分开；provider degraded 或当前采集不可用时，历史结果和导出明确标识不完整或当前覆盖未确认，不能据此断言缺失流或系统健康。
- 成功的 0 行结果保留查询与元数据，可以导出；权限拒绝、provider 失败和超时没有成功快照、详情或导出。分页按新结果归一，详情只关联当前可见页，不回退到固定首条样例。
- 标准视图显示转发匹配、动作、计数和关联对象；Expert 增加 cookie、duration、原始行及 snapshot ID。原始行由同一组精确字段生成，导出保留原文；大字段测试验证未知扩展不被改写。
- 关联目标保留 Bridge 或 Port 类型；NORMAL / drop 行关联 Bridge，storage 输入关联 Port/bond-storage。未知详情样例沿用明确反馈，不导航到其他对象。
- 导出按钮创建 JSON 文件内容与下载请求，包含合成标识、原查询、源信息、观察时间、覆盖状态、预算及原始行。点击后的反馈为 Export prepared，不声称已经确认本机文件落盘。
- 可折叠 review cases 明示合成响应。它们不能清除共享权限和服务门禁；再次运行保持所选异常，只有显式更换响应 fixture 才改变演示结果。

字段语义对照 [OVS 官方 ovs-ofctl 手册](https://www.openvswitch.org/support/dist-docs/ovs-ofctl.8.html)：priority 范围为 0–65535，duration 为秒，n_packets / n_bytes 是计数。最小优先级和 contains 是 Viewer 的过滤规则，不当成 `dump-flows` 的原生命令参数；in_port 选择器只列当前 Bridge 样例支持的输入值，不声明覆盖完整协议语法。

所有配置继续经过 Candidate → Diff / Validation → Apply / Safe Apply → Event / Audit。OpenFlow 查询不创建配置意图，不添加伪造操作证据，也不解除 OutcomeUnknown。

## 浏览器审阅

在 Windows 的 Codex 内置浏览器，通过本地合成预览、可见控件及页面公开 WebMCP review 工具检查。以下结果均属于当前 prototype，不是实际 OVS 故障注入。

| 场景 | 实际观察 | 结论 |
| --- | --- | --- |
| 桌面 1440 × 1000 | 查询、来源、覆盖、新鲜度、分页和详情使用统一组件，页面无横向溢出 | 本轮通过 |
| 正常查询与草稿隔离 | br-fabric 返回 8 行；把草稿切到 br-storage 后，旧结果和刷新仍为 fabric；提交后才变为 storage 的 3 行 / OpenFlow 1.3 | 通过 |
| 跨页与关联对象 | storage 结果导航到 bond-storage 后返回仍保留快照；NORMAL 行显示 Bridge/br-fabric 关联 | 通过 |
| Standard / Expert | Standard 无 raw 标签；Expert 原始行含精确 cookie、720 s、18400 packets、9420800 bytes 与 storage match；权限拒绝两种模式均无详情 | 通过 |
| 输入与键盘 | 65536 显示字段错误并禁用 Run；键盘清空恢复；Enter 选择第二页流后关联对象正确；Tab 焦点可见 | 抽样通过 |
| 响应状态矩阵 | external-authority、stale、truncated、empty、provider-unavailable、permission-denied、query-timeout 逐项核验；前三者保留行，empty 无详情，后三者无快照/详情/导出 | 通过 |
| 截断与缩小查询 | 大结果保留 500 / 768 行，标记 Partial；Table 10 重查为 192 行、完整查询结果 | 通过 |
| 自动过期 | 实际等待后的快照从 Fresh 变为 Stale；采集时间和行身份不变 | 通过 |
| 共享服务门禁 | Loading、Empty inventory、Error、Provider unavailable、Network loss、Permission denied 后立即调用采集入口均被当前门禁阻断 | 通过 |
| 采集竞争 / 权限撤销 | 紧邻的第二次请求被 Busy 拒绝；运行中切 Permission denied 后结果保持空，恢复 Normal 也不复活旧数据 | 通过 |
| 历史快照 / 权限 | 服务不可用可查看明确标记的历史快照；权限拒绝清除数据，手机同样显示 No readable snapshot | 通过 |
| Provider degraded / br-offload | 全局降级下采集返回 8 行并标为 Partial；br-offload 在正常共享状态下仍返回 Provider unavailable，不借用 fabric 数据 | 通过 |
| OutcomeUnknown 隔离 | 已有合成 Bond 事务进入未知状态后完成只读查询，Candidate、transaction、evidence 前后完全相同；切 Normal 后事务仍未知 | 通过 |
| 平板 820 × 1180 | Standard 卡片、有界采集和共享事务提醒可用，页面无横向溢出 | 通过 |
| 手机 390 × 844 | 仅有 incident companion、正确的捕获 Bridge / 行数及证据入口；采集程序入口拒绝，权限拒绝无缓存范围，页面无横向溢出 | 通过 |
| 深色 / 200% 文字 | 临时设置 dark 和 32 px 根字号；检查查询表单、陈旧结果、分页和详情焦点，页面无横向溢出 | 本切片抽样通过 |
| 导出 | 回归核对完整 JSON、原请求副本、raw、时间与覆盖标识；点击显示 192 行 Export prepared | 文件内容与请求通过；本机下载完成未确认 |

深色与根字号改动已按 SHA-256 验证原样恢复 `app/layout.tsx`。文字放大不等同浏览器缩放；二维流表在自身容器内滚动，原生选择器会截短长值，完整捕获条件在结果中换行。既有固定宽侧栏放大后会拆分单词。完整缩放、读屏器及跨浏览器矩阵继续待办。

## 工程验证与边界

类型检查、产品范围 lint、111 项回归、3 项独立 HTTP / 进程恢复集成及 `pnpm build` 通过。新增 12 项回归覆盖查询副本、Bridge 范围、非法输入、过滤组合、行/字节上限、原始扩展保留、失败无回退数据、分页、TTL 和设备/服务门禁。构建仍有 Vinext 既有路由静态分类 Unknown 和插件耗时提示。

本轮未接入真实 OpenFlow provider、正式持久化快照或 Go 管理平面；刷新浏览器重置合成会话。限额、权限、时钟与取消语义在正式采集器仍需重新落实，生产架构继续为 Go 双进程与 Svelte 5 静态 SPA。Groups、Meters、Controllers 独立页面和任何 flow 写操作均不在本切片。

独立 Phase 1 Scope 批准原文仍缺失，范围沿 Architecture §11 和已接受的 Batch 03 Observe 切片。完整原生拓扑库存仍待接入；隔离审阅也观察到既有 Bond WebMCP 创建入口使用固定的 absent / member fixture，重名输入与表单校验未完全统一，需要后续修正，本轮不把该入口算作生产创建能力。

无新增依赖，`pnpm-lock.yaml`、`.openai/hosting.json` 与站点访问不变，未部署。浏览器 CI [#7](https://github.com/sampsonlor/ovs-webui/issues/7) 和通用模板 lint [#6](https://github.com/sampsonlor/ovs-webui/issues/6) 继续开放。当前高保真审阅接受后再记录新基线；新增页面仍按 DPDK/Offload Observe → System Health → Capabilities 逐批推进。
