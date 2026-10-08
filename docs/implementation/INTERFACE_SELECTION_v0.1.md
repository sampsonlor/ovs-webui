# Interface 清单组合筛选与自然排序 v0.1

2026-10-08 · #21 / #42 E9。接续已接受的 Interface 观察、受控字段编辑和共享证据，补齐清单查找及跨页审阅。本批为只读增量，完整原生字段/type/attachment、一般图生命周期及 #54 全页面继续独立验收。

## 服务端选择与来源

API 1.23.0 为 `GET /interfaces` 增加三个可选参数：`bridge_id`、`native_type`、`link_state`。既有名称包含筛选、页大小、字节预算和响应结构保留；冻结 v1.0 契约保持。选择在完整 confirmed 库存视图上执行，然后排序、分页；不会只过滤当前浏览器页，也不采样 Linux。

Bridge 使用 active 的管理身份和当前唯一 Bridge → Port → Interface 关系。不存在或退休的身份返回 `404 BRIDGE_SCOPE_NOT_FOUND`，同名新 Bridge 需要显式选择新身份。歧义或未绑定的父关系返回不可用，不能变成已知空结果或指向别的对象。

`native_type` 精确匹配已观察的原生字符串，包括空字符串的 system 默认语义；缺失值不能匹配空类型。自定义和未识别的类型保持原值，不从名称、硬件角色或 Linux 关联推断。类型列未监控时返回 `INTERFACE_FILTER_UNSUPPORTED`。参数最长 64 个 Unicode 字符，拒绝控制字符和无效编码。

`link_state=up/down` 只匹配原生 OVS 单值观察；`unknown` 匹配已监控列的缺失、空或无法解释的观察。列未监控为 Unsupported，不把它解释成 Unknown 或 down。OVS 状态与独立 Linux carrier、管理状态、转发或健康各有来源。陈旧库存可供最后观察筛选，但保留 stale/degraded 和观察时间，不声称当前运行状态。

## 权限、排序与分页

类型筛选需要 `configuration.read`；权限在读取类型和生成结果之前检查，空或不存在的类型同样拒绝，不能利用空结果或数量探测隐藏配置。前端也关闭类型选择并显示 Permission denied；用户须明确清除现有条件，不自动扩大范围。Bridge/状态筛选沿用库存与状态读取权限。Standard/Expert 使用相同服务端权限。

Interface 名称按不依赖系统 locale 的自然顺序排列：大小写折叠后的 ASCII 数字串按数值长度和内容比较，相同数字先排较少前导零，再以原始完整名称和管理身份决胜。不解析成机器整数，因此长数字不会溢出。该排序不赋予 Physical/Virtual/Bond/Tunnel 分类。

密封游标新增 selection 摘要和排序规则标识，并继续绑定 principal、权限 revision、名称筛选、页大小、generation、snapshot、过期时间和最后身份。按新顺序定位最后身份；改变任意筛选或排序、库存变化及旧规则的游标均须从第一页显式恢复。前端刷新/First page 只去掉 cursor，保留筛选；不能在旧页追加另一快照的数据。

## 页面与审阅

Interfaces 提供名称、精确原生类型、OVS link state 组合筛选；通过 Bridge 选择清单或已有 Bridge 详情进入稳定身份范围。Bridge 选择支持服务端分页，保留截断和失效提示，不猜测同名替代。类型选择包含原生空类型、常见类型及自定义精确字符串。

筛选、页大小和 cursor 保留在 URL；刷新、浏览器返回、详情及其清单返回入口恢复原范围。后台读取不覆盖尚未提交的表单输入；过期 cursor、权限拒绝、退休 Bridge、已知空结果与 provider stale 分别显示。桌面、平板 Standard 和手机均可只读筛选；键盘焦点和表格滚动保持，没有新增配置发起入口。

## 验证边界

Go/单元覆盖长数字排序、组合选择、跨页唯一身份、每项游标绑定、原生空/缺失/无效状态、权限撤销、未监控列、歧义父关系和旧 Bridge 身份。三份 schema fixture 的真实 HTTPS/IPC/OVS 流程分别验证六组选择场景，包括 dummy 设备实际 OVS link down、原生空类型与同名 Bridge 重建；它们不等于三个 daemon 版本或实物 NIC 验收。

正式浏览器新增两条完整流程及十一类截图，验证 Bridge 选择刷新、全库存筛选自然分页、Standard/Expert、键盘、平板/手机、空类型、权限/空结果、真实 OVSDB 停止恢复和旧范围保持。接受需要最终 PR/main 六项完整 CI、原始报告、artifacts 摘要与视觉复核，见[审阅矩阵](../reviews/INTERFACE_SELECTION_v0.1.md)。既有 #71 根因保持独立开放。
