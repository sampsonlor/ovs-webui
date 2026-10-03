# Interface 清单与原生观察审阅 v0.1

2026-10-03 · #42 E1 / #21。实现边界见[实现说明](../implementation/INTERFACE_INVENTORY_v0.1.md)。接受以 PR 和 main 完整 CI、原生报告、浏览器截图及 annotated tag phase1-interface-inventory-v0.1 为准。

| 项目 | 验证与保留证据 |
| --- | --- |
| 正常清单与详情 | 实际 Go/IPC/OVS；全库存名称过滤、分页、精确管理身份、唯一 Bridge/Port 关系；Interfaces 页仅观察，不提交配置写入 |
| 快照变化 | 第二页后外部 mtu_request 变化；保存游标返回 CURSOR_EXPIRED，明确从第一页恢复；旧过滤响应隔离 |
| 原生字段 | 三份实际 schema 的 MTU/请求 MTU 来源、只读性；可选空集合、ofport 负值、未知 type 和精确整数由 Go/前端测试补充 |
| 设备和权限 | status 安全键子集、不凭名称/type 推断 PCI；inventory/state token 的配置 withheld；Standard/Expert 权限相同，退出后的旧回复不能恢复 Interface 数据 |
| 异常状态 | 观察到的空过滤结果、实际 OVSDB 断连后的 stale、同名删除重建后旧 ID 的 NOT_FOUND，以及新的 UUID/管理 ID |
| 深度和响应式 | Standard / Expert 清单与详情、Expert 深色、900px 平板、390px 手机，无页面横向溢出；键盘进入所属 Port |
| 回归 | 既有原生写入三 schema、安全确认/补偿、授权、TLS、持久化、管理连接 VM、完整领域和原型浏览器矩阵继续通过 |

正式浏览器证据保留在 frontend-evidence：interfaces-standard.png、interfaces-expert.png、interfaces-cursor-expired.png、interface-standard.png、interface-expert-dark.png、interface-tablet.png、interface-mobile.png、interfaces-empty.png、interface-withheld.png、interface-provider-stale.png、interface-retired.png。原生字段/权限断言位于 go-inventory-{3.3.9,3.7.1,4.0.0}.json 对应验收路径。

目标完整结果：每架构 197 个唯一 Go race 顶层测试、14 项正式浏览器流程、10 次额外 Safe Apply 准入重复和所有既有原生矩阵；共享 228 项单元、3 项隔离集成、31 项原型浏览器测试。CI 的失败、跳过或截图缺失不构成接受；Windows 本地通过不能代替 Linux 原生证据。

## Review disposition

本批实现后提交 PR，核验双架构报告和截图后合并 exact tested head；复验 main 的相同代码树和完整 CI，然后标记接受基线并删除本地/远程特性分支。#21 和 #42 保留 Open / In Progress：Interface 受控编辑、创建语义与更完整图生命周期仍需独立批次交付。#71 既有 arm64 一次性登录问题继续开放，本批通过不代表已定位其根因。
