# Interface 高级原生配置观察审阅 v0.1

2026-10-05 · #21 / #42 E4。仅本地通过或 CI 页面绿色不能代替完整报告与视觉复核。

| 门禁 | 必需证据 |
| --- | --- |
| 实际 schema 与监控 | 三份 actual schema 的五列/type/cardinality；增量更新保留 empty/zero/int64；版本伪装、缺列和类型变更不猜能力，不兼容列不进入 monitor cache |
| 权限与来源 | 配置请求有 `ovsdb-configuration` source；state/inventory-only 在 list/detail 全部 withheld/null，原生实际 ofport 仍可读；config revision 与操作观察区分 |
| 例外与不确定性 | 缺列/type 为 Unsupported；缺失/无效值为 Unknown；零与 empty 不冒充缺失；stale 为 Last observed，来源 partial/unavailable 不显示为当前值 |
| 正式正常路径 | 实际 Go/OVSDB/ovs-vswitchd；直接比对数据库请求与限速参数；暂停实际 daemon 后配置请求与实际分配不同，明确展示，最终恢复进程 |
| 模式与设备 | `interface-config-standard` / `interface-config-expert` / `interface-config-dark`；`interface-config-tablet` / `interface-config-mobile`，平板 Standard、键盘访问、无页面溢出 |
| 例外截图 | `interface-config-request-pending` / `interface-config-empty-zero` / `interface-config-withheld-standard` / `interface-config-withheld-expert` / `interface-config-stale`；既有 provider-stale 保留 |
| 回归 | 双架构全部 Go race 与正式浏览器、三 schema 原生库存/MTU/QinQ/Bridge/Port/Bond、安全补偿/真实断链/认证/TLS/存储与额外重复矩阵；共享单元、集成、原型浏览器、完整构建 |

最终 PR/main 的 `go-runtime-amd64`、`go-runtime-arm64` 与三个共享报告下载逐份核对；配置观察不授予写入，不证明实际 policing enforcement。修改写入与完整 #21/#42/#43/#54 各自验收，#71 登录根因不被本批宣称修复。
