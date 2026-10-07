# Interface ingress policing 观察审阅 v0.1

2026-10-07 · #21 / #42 E7 交付候选。只有最终 PR/main CI、报告、截图和同树核对成立后记录 `phase1-interface-policing-observe-v0.1`。

| 范围 | 验证与所需证据 |
| --- | --- |
| 内核格式与单位 | `TestLinuxPolicingNativeLayoutsAndExactRates`：basic/matchall/u32、旧 direct police、uint64 覆盖、互相独立的 byte-only/packet-only rate、exceed/conform |
| 观察局限 | `TestLinuxPolicingUnsupportedCoverageIsNeverAbsence`：未知 classifier/action/option、average、shared ingress block、legacy ingress parent 别名、clsact ingress/egress 隔离 |
| 协议失败 | `TestLinuxPolicingMalformedAndInterruptedDumps`：长度、seq、错误类型、interruption、DONE/error、重复/无效属性与 rate |
| 实际读与预算 | `TestLinuxPolicingHostBindingBudgetAndCancellation`：真实 GETLINK/qdisc、设备错配、取消、并发占满、无效名称 |
| 授权/身份 | `TestInterfacePolicingPermissionsScopeAndRevision`、`TestInterfacePolicingRebindingAndInvalidSamplesDiscardValues`：权限、detail-only、revision、stale、unsupported、重绑定/代际、过期/错误 provider 数据、partial |
| 原生三 schema | `interface_linux_policing` metrics：真实 system Bridge/veth/OVS byte-only 和 packet-only police、独立 tc kernel read、125000 bytes/s 与 5000 packets/s、partial、权限、移除 |
| 前端 | 3 项 unit：精度、null/0、来源门禁、明确 partial/withheld 与身份异常 |
| 正常/响应式 | `interface-policing-standard/expert/dark/tablet/mobile`，相同读权限、窄屏无页面横向溢出、无写操作 |
| 例外 | `interface-policing-partial/config-pending/identity-mismatch/packet/empty/conflicting-request/unsupported/stale/withheld-standard/withheld-expert`，不把失败或空观察推断成不限速 |

要求本地契约/单元、Go 包、Linux amd64/arm64 静态检查、类型、lint、完整 pnpm build。正式门禁预期每架构 234 项唯一 Go race 顶层通过、27 项正式浏览器流程；共享 248 单元、3 集成、31 原型浏览器。既有三 schema native MTU/default/QinQ/Bridge/Port/Bond/Safe Apply、10 次 admission 和 15 次暂停恢复、真实存储/systemd/auth/TLS/库存门禁全部保留。不能把常规套件跳过的 root/native 场景算成已经执行，必须逐份核对专用报告。

最终 PR 查看所有新增状态，main 复核代表模式、窄屏和例外；截图存在不等于视觉验收。完整 #21/#42 和既有 #71 继续开放。此批不声称实际吞吐、丢包效果、所有 ingress 路径覆盖或设备/规则所有权。

CI 提前运行四项 Linux protocol 测试与真实 policing fixture，保留 go-policing-protocol/native 报告；既有完整矩阵与后续 race 仍全部执行。初始组合 byte/packet fixture 失败，不作为通过证据；最终分别证明两种模式，新增状态共 15 类。
