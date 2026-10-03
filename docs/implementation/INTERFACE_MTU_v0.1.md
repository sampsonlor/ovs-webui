# Interface 明确 MTU 请求受控编辑 v0.1

2026-10-03 · #42 E2a / #21。在已接受的 E1 原生观察基础上，贯通一个 Interface 原生字段的正式编辑闭环；完整 #21 / #42 继续开放。

## 本批准入

仅接收一个 `interface.mtu.set` intent，携带完整 Interface 管理 ID、OVS UUID、instance generation 和 576–65535 字节的明确 `mtu_request`。mgrd 的独立 root 参数 `--local-mtu-interfaces` 授予具体 Interface 管理身份的字段 Authority，操作账户还须具有 `ovs.interface.mtu.write`。Port 的 VLAN/Bond、创建或删除权限均不能替代这两层授权。

当前原生请求必须是范围内的单元素可选整数集合，Interface 必须为非 Bridge 本地、独立单成员 Port 下的 internal 类型，位于已有 system Bridge，父 Port、Bridge 和根关系唯一且有效，并有完整原生 options 为空的 provider 证据。外部控制标记优先于 root grant。默认空请求、其他类型、Bond 成员、Bridge 本地 Interface、raw options 非空/未知及未知约束保持 Observe。本批不提供 clear、type/options/attachment 修改或通用 CRUD。

OVS 将 `mtu` 作为设备观察，客户端配置的是 `mtu_request`；internal 未指定请求时由 Bridge 其他成员的最小 MTU 推导。因此本批只覆盖明确请求之间的修改，避免把清空请求视为恢复某个固定 MTU。依据：[OVS Interface MTU 原生说明](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)。产品范围小于原生 schema 的全输入范围，设备实际支持仍须 Applied 证明。

## Candidate、执行和恢复

mgrd 捕获原始请求、依赖、父身份和 schema；浏览器不能提供 before、依赖或补偿值。修改已有 intent 保留 sealed original。Diff 分别展示 Original / Current / Yours。请求值、generation、schema、Authority、type/options 或父关系变化会阻断旧 Validation；本批要求丢弃并重新 stage，不能通过 rebase 改换 Interface 绑定，不能与其他 intent 混合。

执行前和原生事务中检查 touched `mtu_request`、Interface type/options、唯一 Port/Bridge 归属、Bridge 成员集合、根引用及控制标记。仅更新 Interface.mtu_request，并精细修改共享提交证据 marker；未知列、other_config 和非控制 external_ids 保留。待处理 MTU 事务保护当前 Interface、Port、Bridge 和图生命周期，防止管理器内部并发删除或改换其依赖。

Commit 使用原生事务返回的精确 next_cfg 目标。Applied 必须同时证明 current counter、配置 after-image、commit marker、Interface 无错误以及原生观察 `mtu` 等于请求值；随后通过一次只读原子 OVSDB 证明重新核验，不能仅凭数据库提交或后续全局计数推成成功。确认窗口只在 Applied 和共享健康探测通过后开启。

补偿先比较已提交请求和依赖，再恢复 captured explicit request，并再次验证实际 MTU；外部请求、type/options/归属/控制变化和同名重建阻断恢复。丢失执行或补偿回复保留 OutcomeUnknown / recovery-required，不能重放写入或制造 Applied target。共享持久 Safe Apply、独立 Watchdog、Event、Job、Audit 和 Last Known Good 沿用已接受服务。

## 页面和契约

正式 Interface 详情提供 MTU 字段 Authority 与符合准入的编辑入口；独立 `/interfaces/{id}/mtu` 页面可直接刷新，输入只进入 Candidate。页面打开后的原生配置或父关系变化阻断旧表单。Standard / Expert 使用相同权限和风险门禁；Expert 增加原生身份与来源。桌面创建/验证/Apply，平板和手机审阅并处理已有 Safe Apply，直接访问 MTU 编辑地址也不能 stage。

API 1.15.0 增加独立的 typed intent、可选 MTU hint 和只读 captured change 证据，冻结 v1.0 基线和既有调用保持兼容。完整 raw options 仅保留为空/非空的私有 provider 断言；公开 map 仍为安全键子集，空公开 map 不构成原生为空的证明。

验证与接受见[审阅矩阵](../reviews/INTERFACE_MTU_v0.1.md)。下一步仍需默认请求及恢复策略、其他 Interface 字段/原生类型、一般图生命周期和完整 #21 验收。
