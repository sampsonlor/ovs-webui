# Interface Linux 设备观察审阅 v0.1

2026-10-05 · #21 / #42 E3。接受必须有最终 PR 与同树 main 的双架构完整六项 CI、下载报告和视觉复核；仅本地通过或 CI 页面绿色不能代替证据。

| 门禁                         | 证据                                                                                                                                                                                                  |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 实际 Linux 设备正常路径      | native Linux `TestLinuxDeviceHostObservation` 无 mock fallback；正式浏览器 Go + kernel OVS + veth，独立比对 ip link 的 ifindex/MTU                                                                    |
| 独立 carrier / 配置 revision | veth peer up/down 与 MTU 变化；已知 false 显示 Down (0)，Linux 观察不改 config_revision；只读页无配置 HTTP 命令                                                                                       |
| 硬件证明与预算               | PCI/USB/virtual sysfs 夹具；USB 不借上游 PCI；driver 与父 subsystem；缺失、负哨兵/未知 enum、根逃逸和同名 index；最多十字段、两个 worker、250ms timeout/cancellation/busy                             |
| OVS 身份变化                 | 采样期间 index/type/管理 ID/generation/provider 变化丢弃值；stale 不请求 host 样本；错 index/零时间/过期样本无值                                                                                      |
| 真实异常                     | 正式浏览器暂停 daemon 的观察故障注入后 mismatch，实际 Linux 删除与同名新 index；原生类型不支持、配置 withheld、OVSDB stale 明确                                                                       |
| Standard / Expert            | `interface-linux-standard`、`interface-linux-expert`；Linux 与 OVS status 分栏，Expert 关联证据                                                                                                       |
| 响应式与深色                 | `interface-linux-tablet`、`interface-linux-mobile`、`interface-linux-dark`；平板 Standard 和手机只读、无横向页面溢出                                                                                  |
| 例外截图                     | `interface-linux-carrier-down`、`interface-linux-withheld`、`interface-linux-identity-mismatch`、`interface-linux-device-missing`、`interface-linux-unsupported`；保留既有 `interface-provider-stale` |
| 回归                         | 全套单元/契约、集成、原型浏览器；原生两架构 Go race、三 schema 库存/字段/QinQ/MTU/Bridge/Port/Bond、安全恢复、真实管理断链、认证/TLS/存储与额外重复矩阵均必须保留                                     |

报告由 `go-runtime-amd64` / `go-runtime-arm64` 与三个共享 artifact 下载核对，截图保留在 `frontend-evidence`。新浏览器测试使用真实服务和内核设备；PCI 成功解析是文件系统夹具，真实 NIC 与完整硬件发现继续独立验收。本批没有硬件配置、DPDK/Offload Manage、attachment 或 type 变更。#21、#42、#54 与 #71 保持各自范围。
