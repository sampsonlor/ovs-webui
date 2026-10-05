# Interface 高级原生配置观察 v0.1

2026-10-05 · #21 / #42 E4。延续已接受的 Interface 清单、MTU 受控编辑和独立 Linux 观察，在正式详情加入请求端口号与四项 ingress policing 配置。完整 Interface 编辑、类型/attachment 及 #43 的 QoS 管理继续独立验收。

## 原生语义与来源

`ofport_request` 是可选的 1–65279 整数请求；原生 empty set 表示没有明确请求，区别于 missing/unknown。实际分配仍取只读 `ofport`，可能与请求不同，-1 仍表示分配失败。改变请求可能使其他端口重新分配编号，不据配置值推断当前 OpenFlow 管理权或 Applied。

`ingress_policing_rate` / `ingress_policing_burst` 分别为 kbit/s 与 kbit；`ingress_policing_kpkts_rate` / `ingress_policing_kpkts_burst` 为每秒千包与千包。rate=0 表示禁用对应限速；burst=0 保留原生默认语义，未转换成某个实测或有效 burst。四项仅描述配置，不证明内核 qdisc、实际丢包或流量已按该值运行。依据：[OVS Interface 原生配置手册](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)。

Provider 按当前实际 schema 列与类型选择监控，不根据版本字符串猜能力。端口号要求已知 optional integer 与原生上下界；policing 要求非负 scalar integer。缺列、非监控或不兼容类型为 Unsupported；不兼容类型不请求监控，意外返回该列会拒绝协议更新，不进入观察缓存。已监控但没有值为 Unknown；无效 cardinality、范围、非原生整数亦为 Unknown，不补 0/默认值。完整 int64 使用原生十进制字符串，客户端不转换为失去精度的 Number。

## 权限、版本与预算

五字段在正式清单/详情共用既有 `InventoryField`，API 1.18.0 为该类型增加可选 `reason`；冻结 v1.0 与旧响应继续兼容。配置均需要 `configuration.read`，state/inventory 权限不足以读取请求或限速。隐藏值为 null，原因为 `CONFIGURATION_WITHHELD`；Standard/Expert 的服务端权限相同。实际 `ofport` 仍按 state 权限独立提供。

字段 source 为 `ovsdb-configuration`，沿用 provider 采样时间、新鲜度和完整共享库存身份。原生配置变化更新 `config_revision`，单独的端口分配变化不更新它。过期配置展示为 Last observed，并保留页面 stale 提示；来源未知不作为当前配置。既有行数、单行、快照、响应分页/截断预算保留；新增只读 numeric 列，不增加 host 扫描、路径或开放 map。

全部五字段 `editable=false`、`ownership=unknown`；原生 schema mutable 不授予写权限。现有 MTU 的独立 root grant、账户能力、Candidate、Diff/Validation、CAS、Applied、Safe Apply 和补偿证据保持原门禁。本批不增加 intent、执行计划、直接保存、OpenFlow/DPDK/Offload Manage 或主机修改。其余字段写入按原 #21/#42/#43/#47 的各自范围推进。

## 页面与验收

Standard 显示配置请求、明确单位、空请求/禁用/默认 burst；Expert 增加原生键、来源、reason、时间与 freshness。配置表格支持键盘滚动，桌面、平板 Standard、手机只读，深色保留。与现有实际 OVS 状态及独立 Linux 表分别呈现。

双架构正式浏览器使用隔离 Go/OVS 服务，直接比对实际数据库配置；暂停真实 ovs-vswitchd 后更新 fixture 请求，检验数据库请求已改变而实际分配未改变的正常例外，随后恢复进程和清理隔离 Bridge。无配置读取权限时 Standard/Expert 都隐藏全部五项。既有真实 OVSDB 断连用例同时检查这些字段的 stale 来源与权限。三份 schema 的真实库存流程检查原生空请求、默认配置、来源和隐藏；自定义缺列/类型变化、未知/无效值、64-bit 与旧配置的证据由 Go/unit 提供，不宣称真实 policing enforcement 或三个 daemon 版本已验收。

接受须通过最终 PR 和同树 main 的六项完整 CI、下载报告及截图复核，见[审阅矩阵](../reviews/INTERFACE_NATIVE_CONFIGURATION_v0.1.md)。接受标签 `phase1-interface-native-configuration-v0.1` 仅在该门禁成立后记录；#21、#42、#43、#54 与 #71 保留各自完整范围。
