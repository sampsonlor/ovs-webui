# Interface 清单组合筛选与自然排序审阅 v0.1

2026-10-08 · #21 / #42 E9，范围见[实现说明](../implementation/INTERFACE_SELECTION_v0.1.md)。

| 要求 | 验证证据 |
| --- | --- |
| 全库存选择与跨页顺序 | `TestInterfaceSelectionNaturalOrderAcrossBoundedPages`：组合 Bridge/type/OVS link、长数字、大小写和前导零、唯一身份、无 Linux 采样 |
| 游标与权限 | `TestInterfaceSelectionBindsEveryFilterAndPermission`：三项新选择、名称、页大小、配置读取门禁；既有 principal/revision/snapshot/generation 回归保留 |
| 空、未知、陈旧及身份 | `TestInterfaceSelectionNativeDefaultsUnknownAndRetiredBridge`：原生空与缺失类型、自定义类型、up/down/unknown、stale、旧 Bridge 拒绝、缺列和无效/重复参数 |
| 契约与关系 | `TestInterfaceSelectionContractAndAmbiguousParents`：可选查询、OVS enum、歧义关系失败；SPA `/bridges` 深链及额外路径拒绝回归 |
| 客户端隔离 | 四项 Interface selection 单元测试：URL 精确编码、空类型、重载与分页、延迟跨范围回复、cursor 失效和权限变化后清空且不扩大选择 |
| 真实原生三 schema | `go-inventory-*.json` 的 `metrics.interface_selection`：每 schema 六组 checks；配置读取 token 403，包括空/不存在的类型；实际 OVS dummy down、自然分页和 Bridge 新身份 |
| 正式浏览器 | 两项新增 `Interface selections…` / `Interface selection exceptions…`：真实 Go/OVS 入场、Bridge 选择页刷新、组合自然分页、详情返回、空类型、权限拒绝、空结果、真实断连及同名重建 |
| 模式与设备 | Standard/Expert 权限不变，键盘进入/返回详情；平板 Standard 和手机只读筛选、无页面横向溢出；Bridge 选择、两模式、平板、手机、原生默认、窄屏自定义类型、withheld、empty、stale、retired 共十一类截图 |

本地要求：完整类型、lint、258 项单元/契约、隔离集成、scoped Go 包、amd64/arm64 Linux 静态检查及完整 `pnpm build`。Windows 不能代替真实 Linux/OVS 验收。

正式 Gate：最终 PR 与合并 main 各六项 CI，五组 artifacts 下载并与官方 SHA-256 比对；每架构 253 项唯一 Go race 顶层、32 项正式浏览器及每份三 schema 选择报告。既有 native policing/MTU/default/QinQ/Bridge/Port/Bond、安全恢复、认证/TLS/存储、10 次额外准入与 15 次暂停恢复、3 项集成和 31 项原型浏览器均保留。每个正式测试的脱敏认证诊断继续核验。

截图存在不等于视觉接受。需要逐张复核最终 PR 的十一类 × 双架构新截图，并核对 main 的代表模式、窄屏及异常。正式结果、同树 main、accepted annotated tag 和分支清理须共同成立后才接受本批。完整 #21/#42 不因本批只读清单改进关闭；Profile/Label 筛选、分类及其他管理范围继续在原责任任务中验收，#71 历史根因未在本批宣称修复。

候选 CI `37731184002` 的原始报告与五组 artifacts 已单独保留。双架构正式浏览器各 29 项通过、两项断言失败和一项等待错误：后台 Model 更新重置尚未提交的表单，Bridge 跨页测试比较了独立请求随机封装的不同 cursor，既有详情链接断言尚未包含保留的筛选参数。修正实际 query 变化门禁、核对页面实际收到的 cursor 和精确新身份/URL 范围，并增加真实后台刷新后四项输入保持检查；现有 timeout、零 retry、全部用例及生产安全门禁保持。该失败执行不计为本批验收，接受仍取决于修正后完整 PR 和独立 main。

## Review disposition

最终 disposition 与准确 head/run/artifact/视觉证据记录于 `phase1-interface-selection-v0.1` annotated tag 和 #21/#42 的 E9 接受记录；该标签创建前为交付候选。
