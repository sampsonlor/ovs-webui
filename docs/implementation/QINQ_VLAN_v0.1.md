# QinQ / 客户 VLAN 原生服务 v0.1

2026-10-03 · #42 子批次 D，接续 C3。新增现有 Port 的 dot1q-tunnel 与 cvlans 管理，沿用 port.vlan.set、Candidate、Diff/Validation、Safe Apply、字段 OCC、Job/Event/Audit 和精细补偿。#42/#21/#22/#54 保持独立的未完成范围。

## 范围与原生含义

服务 VLAN tag 是外层 VLAN，cvlans 是允许的内层客户 VLAN；空 cvlans 表示全部客户 VLAN。dot1q-tunnel 必须有 1–4094 的服务 VLAN，trunks 为空；客户 VLAN 为唯一的 1–4094 整数。离开 QinQ 时明确清空客户 VLAN 列表，Diff 保留原值，补偿恢复原值。0/4095、未知模式、非 QinQ 上被忽略的 cvlans、无法证明原始语义的组合保持 Observe。

原生依据：[OVS 数据库手册，Port VLAN Configuration](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)。这些字段控制 OVS normal switching 的 VLAN 行为，不证明自定义 OpenFlow pipeline、对端或整条生产链路的正确性。

本批保留 Port.other_config 的 qinq-ethtype，不提供 TPID 写入：缺失表示原生 802.1ad 默认值；明确 802.1ad / 802.1q 都可观察、校验与使用，未知值阻止 QinQ。字段缺失与显式默认值不相互改写。TPID 编辑以及 Bond、internal、隧道、DPDK/Offload 上的 QinQ 仍需独立语义验收。

## Authority 与适用对象

沿用 root 的 --local-vlan-ports 管理 ID 授权，以及 workspace.write、ovs.port.vlan.write 和共享 apply/confirm/rollback 权限。Standard/Expert 只改变信息深度，二者均可审阅和配置已获授权的 Advanced VLAN；UI 模式不能授予权限。

对象必须是现有、单 Interface、具有唯一父 Port/Bridge 的对象；成员类型为 system（含原生空字符串）或隔离测试 dummy，无未验证 options；Bridge 为 root 可达的 system（含空字符串）或隔离测试 dummy datapath。local/internal、共享成员、未知/退休身份、外控、过期 provider、不兼容 schema 和未知 TPID 均不开放 QinQ。普通 VLAN 已接受边界不扩大为图或成员写入。另须 root 已配置 vlan-limit=0 或 2，并观察到对应 Datapath.capabilities.max_vlan_headers ≥ 2；默认的一层解析、未知或缺失 capability 均阻止 QinQ。Datapath 记录须先受 Open_vSwitch.datapaths 的实际类型键引用，ovs-vswitchd 才填入实测能力；缺失时保持 unavailable，不伪造 capability。新增只读 Datapath monitor 不分配管理身份或参与 switching generation anchors；本操作不修改全局解析设置。

## 签名、并发与恢复

API 1.13.0 新增可选 qinq_context（TPID 原始值和依赖摘要）及库存 qinq_editable/qinq_ethertype。公开 VLAN 输入保持闭合且不接受这些 mgrd 捕获字段；旧签名 envelope 的序列化保持不变。validator 版本改为 qinq-vlan-v1，已有 Validation 需重新生成。

QinQ 的进入、编辑和退出均捕获上下文；复用草稿保留原始上下文，显式 snapshot-bound rebase 才能接受外部变更。After-image 与补偿保留上下文，即使补偿结果已离开 QinQ，也仍检查原 TPID 与成员关系。

原生事务只 update vlan_mode/tag/trunks/cvlans，另按既有协议写 commit marker、递增 next_cfg 并 durable commit。事务重复检查 TPID 所在 other_config、root 解析设置和 datapath 引用、实际 datapath capability、成员唯一父关系及既有身份/authority/root/结构/字段条件。dispatch 前后出现重叠变化时整个事务拒绝。other_config 的派发 guard 保守比较整张 map，晚到的无关 map 变更可能需要重试审阅；回滚使用新观察并只恢复四个 VLAN 字段，保留无关 map key。

Commit、Applied 和确认继续分离。Applied 是只读原生 proof；正式确认仍要求服务端健康 probe。丢失响应不重放、不推测 target；原始字段、TPID 或依赖被外部修改时保留冲突/恢复状态，不强行覆盖。可恢复的原生缺省 mode 保持缺失值，不能在补偿时静默写成 access/trunk。

## 页面与后续范围

正式 Svelte VLAN 编辑器提供 Advanced dot1q-tunnel 选项，区分 Service VLAN tag / Customer VLANs，说明空列表、TPID 保留和离开 QinQ 的变化。共享 Diff 显示语义与上下文；Expert 增加摘要详情。平板/手机不能发起新配置，继续审阅及处理既有 Safe Apply。读者、未知配置、未验证 TPID 保持只读。

本批未包含 TPID 编辑、一般 Bridge/Port/Interface 生命周期、物理接管、成员迁移、Interface 原生属性或 #22 完整跨 Port VLAN 工作流。它们继续留在 #42/#21/#22/#54 的剩余范围。
