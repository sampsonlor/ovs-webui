# Interface 自动 MTU 请求与依赖恢复 v0.1

2026-10-04 · #21 / #42 E2b。延续已接受的 [E2a](INTERFACE_MTU_v0.1.md)，允许从原生空请求设置明确 MTU，以及将已有明确请求清空为自动模式。完整 Interface 功能与一般图生命周期仍独立验收。

## 原生语义和准入

`mtu_request` 的原生空 optional set 与缺失/未知字段不同；不以 0 或固定 1500 代替。`mtu` 是设备观察。OVS 对未设置请求的 internal Interface 根据 Bridge 其他设备推导最小 MTU，未设置请求的其他 internal Interface 不贡献这个最小值。依据：[OVS 配置说明](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)、[原生推导实现](https://github.com/openvswitch/ovs/blob/v3.3.9/ofproto/ofproto.c#L2721-L2808)。本批据此采用更窄的有证明准入：没有已知贡献设备时拒绝自动转换，不使用原生 fallback 猜测恢复结果。

沿用 E2a 的独立 root `--local-mtu-interfaces`、`ovs.interface.mtu.write`、单 intent、非 Bridge 本地 standalone internal Interface、已有 system Bridge、唯一父身份和 raw options 空证明。Bridge 依赖图上所有成员必须具有有效管理身份、唯一 Port/Bridge 归属、已知 type/request、无错误、有效 ofport、无外部控制及完整 options 空证明。仅 internal/system/原生空 type 被纳入；未知、patch、tunnel、DPDK 等保持 Observe。贡献设备的实际 MTU 必须有效，已有请求必须已实际应用。

从空请求设置时，当前目标实际 MTU 必须等于已证明的自动值；清空时，当前明确请求必须已实际应用。自动依赖限制为 32 个 Port、32 个 Interface 和最多 60 个绑定，保留现有 64 个 Candidate 绑定及执行计划大小预算。图超限、设备状态不明或无贡献者拒绝自动转换，既有明确请求之间的 E2a 修改仍可按其独立边界使用。

## 捕获、执行和恢复

mgrd 捕获请求原值和自动依赖：Bridge 成员、每个 Port 的 Interface 集合、所有依赖的原生/管理身份、类型、请求、ofport、raw options 证明及贡献设备的实际 MTU。目标自身的请求/实际 MTU 独立处理，其他自动 internal Interface 的派生实际 MTU 不作为自身依赖，以免正常执行后误判为外部漂移。非控制 metadata 不改变依赖摘要。

Diff 同时展示 Original / Current / Yours 的请求和自动 MTU 依赖值。草稿修改保留 sealed original，不能重捕获依赖、静默 rebase 或改换对象。若明确请求草稿原先未捕获自动依赖，切换为清空必须丢弃并重新 stage。依赖图身份纳入 pending transaction 保护，防止管理器内部删除或改变恢复所需对象。

原生单次事务对所有捕获依赖执行零超时 CAS 和唯一父关系检查，只更新目标 `mtu_request`，以原生 `set []` 清空；提交 marker 和 next_cfg 沿用共享执行服务。其他设备和未知配置不写入。Applied 仍需精确返回的 next_cfg、目标 after-image、无错误、实际 MTU 等于明确请求或 captured automatic value，并重新取得只读原子证明；共享健康探测通过后才开启确认窗。

回滚精确恢复原来的数字或空请求，并验证对应设备值。贡献值漂移、未知图状态、同名依赖重建等阻断旧 Validation、确认和补偿；不能用同名新对象替代捕获身份。丢失执行回复不会重放或制造 Applied target；丢失补偿回复保留 recovery-required。持久 Safe Apply、独立 Watchdog、Job、Event、Audit、Last Known Good 不改为页面状态。

## 契约和页面

API 1.16.0 增加闭合的 object-only `interface.mtu.clear` intent、可选自动 MTU hints 和 captured dependency。客户端不能指定默认值、before、依赖或执行结果。MTU change 的 before/after 可为 null，表示原生空请求；原有数字请求/响应及已持久化的明确请求 journal 保持可读和可补偿，冻结 v1.0 输入基线继续检查。

正式 MTU 页面提供明确请求/自动模式选择、当前请求与观察、当前依赖推导值和拒绝原因。Standard/Expert 的 Diff、权限和风险门禁相同。桌面发起新配置，平板/手机审阅与处理已有 Safe Apply；无权限、陈旧、隐藏配置、未证明自动值和直接窄屏编辑均保持禁用。验证与证据见[审阅矩阵](../reviews/INTERFACE_MTU_DEFAULTS_v0.1.md)。
