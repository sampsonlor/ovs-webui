# 现有 Port 的 Bond / LACP 原生字段服务 v0.1

2026-09-24 · #42 子批次 A，接续 #41 已接受基线。复用正式 Candidate、Diff/Validation、字段 OCC、Safe Apply、Job/Audit 和恢复服务。本批不完成 #42 的对象生命周期，也不关闭 #21/#22/#54。

## 对象与原生语义

Bond 是包含至少两个 Interface 的 Port。`bond.configure` 只修改已经存在的 Bond Port，输入的 `member_interface_ids` 必须与当前成员管理 ID 完全相同；重名、新 UUID、其他 Port 的成员、重复成员及成员增减都不能自动重绑定。`port.lacp.set` 可修改单 Interface Port 的 LACP，保留原有 bond_mode。

管理字段为 `Port.lacp`、`Port.bond_mode` 和 `Port.other_config["lacp-fallback-ab"]`。读取与 checkpoint 保留原生空集合和 map key 缺失，不把默认值写回数据库。LACP 支持 off/active/passive；Bond 支持 active-backup/balance-slb/balance-tcp；fallback 支持 preserve/enabled/disabled/default，分别保留、写 true、写 false、移除 key。既有 `bond.configure` 省略 fallback 时按 preserve 处理；新 `port.lacp.set` 须显式指定 fallback。

校验拒绝 balance-tcp + LACP off、fallback enabled + LACP off、balance-slb + Bridge flood_vlans，以及启用 STP/RSTP 的 Bond。原始字段语义不明或原始组合无法安全恢复时保留 Observe，不能静默归一化。local/internal Port、非 system/dummy 成员、重复父 Port、脱离 root 的 Bridge、未知/外控 authority 和不兼容 schema 都不能开放写入。dummy 用于隔离原生验收；隧道、DPDK、Offload 等类型继续按后续能力边界处理。

原生依据：[OVS 数据库手册](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)与[Bonding 文档](https://docs.openvswitch.org/en/latest/topics/bonding/)。`Applied` 证明 ovs-vswitchd 消费了配置，不证明对端已完成 LACP 协商。Validation 给出对端与管理路径审阅提示；正式确认仍要求当前 Applied、独立管理 TCP probe 和用户确认。物理上联与对端的业务验收另行进行。

## Authority 与启用

root 在 mgrd 启动参数中配置 `--local-bond-ports=<Port 管理 ID 列表>`，与 `--local-vlan-ports` 独立，默认列表为空。它不接受 OVS 名称或 Interface ID，也不能通过 HTTP/IPC 更改；外部控制标记始终优先于本地允许列表。

调用者必须具备共享 workspace/validation/apply 权限与新的 `ovs.port.bond.write`。新建 NetworkAdmin/Admin 模板包含该能力，已有角色和 credential ceiling 不随升级扩大。现有安装需通过正式角色管理流程显式授予能力，再重新登录或签发包含该能力的 token。VLAN 权限不足以修改 Bond；混合 Candidate 必须同时拥有两种字段权限。step-up、范围、撤销和授权上限继续由正式身份服务检查。

库存新增 `bond_configuration`、`bond_ownership`、`bond_editable` 与相应 `allowed_operations`。未知值或不安全原始组合保持不可编辑；库存提示不替代服务器校验。

## Candidate 与 API

公开契约升级为 1.8.0，保留既有 union 顺序，仅增加可选 Bond fallback、新 LACP intent 与可选观察字段。签名 envelope 新增可选 `bond`/`before_bond`；历史 VLAN 签名序列化不变。Bond 项保留兼容的空 `value`/`before` VLAN 槽位，执行只依据 operation 与 Bond 字段，不将它们当成 VLAN 写入。

在已有 `PATCH /candidate` stage 请求的 intents 中使用以下合成样例，object 与 members 必须替换为库存当前绑定；请求外层仍要求 ETag、workspace epoch、CSRF 和独立 request_id。

```json
{
  "intent_id": "11111111-1111-4111-8111-111111111111",
  "operation": "bond.configure",
  "object": {
    "management_id": "22222222-2222-4222-8222-222222222222",
    "ovs_uuid": "33333333-3333-4333-8333-333333333333",
    "table": "Port",
    "instance_generation": "44444444-4444-4444-8444-444444444444"
  },
  "mode": "balance-tcp",
  "lacp": "active",
  "fallback": "enabled",
  "member_interface_ids": [
    "55555555-5555-4555-8555-555555555555",
    "66666666-6666-4666-8666-666666666666"
  ]
}
```

单 Port LACP 使用相同 object，加 `operation=port.lacp.set`、lacp、fallback，省略 mode 和 members。所有变更继续经过 Validation 与 safe-apply；请求不能携带 observed before、原生 OVSDB payload 或权限声明。

首次 stage 捕获原值与结构依赖；编辑不重抓原值，preserve 沿用本项 desired fallback。外部改动需要当前 conflict snapshot 与显式 keep-current/keep-mine rebase。validator 版本变为 `port-fields-v2`，旧 Validation 需重做；已保存的 VLAN draft 与 journal 仍可读取。

目前同一 Candidate 的同一 Port 只保留一项字段意图；不同 Port 的 VLAN 与 Bond 可进入同一原生原子事务。专用正式 Bond 编辑器与完整页面迁移由 #54 继续，现有 Svelte 共享审阅/事务/证据页能展示字段 Diff。Standard/Expert 不改变授权；桌面开启配置、平板处理已有 Safe Apply、移动端查看事故与回滚的责任保持有效。

## 派发、恢复与补偿

原生事务的零等待条件绑定 Port UUID、成员集合及唯一父 Port、Interface 身份/type/options、Bridge 父关系/STP/RSTP/flood_vlans、root 可达性、authority 与字段原值。外部写者在预检后修改依赖或重叠字段时整个事务拒绝，混合 VLAN/Bond 不会部分提交。

派发只 update lacp/bond_mode，使用 map-key delete/insert 修改 fallback，不替换整个 other_config，也不修改 VLAN、成员或身份。guard 对 other_config 整体保守比较，预检与派发间的无关 map 修改可能要求重新审阅；补偿则读取最新 map 并只恢复 fallback key。计数、durable commit、只读 Applied 证明与 OutcomeUnknown 恢复沿用 #39/#40。

每个 Port 沿用历史 `ovs-webui.vlan-commit` marker key，以便恢复既有计划。保护记录区分 `port.vlan` 与 `port.bond`；同一 Port 的未结束应用串行以保护恢复标记，其他 Port 不获取这项保护。管理路径另使用已有 Safe Apply 恢复域。

新授权记录保存所需字段能力，watchdog/confirmation 按当前权限重查；旧记录缺少该字段时只要求原有 VLAN 能力。Bond 撤权触发服务器补偿。第三方改动本次 after-image、成员或身份时保留 Rollback Conflict/Recovery Required，不强制覆盖或重放未知写入。正常补偿精确恢复原始空集合、fallback 缺失及显式值，并保留无关 VLAN/metadata。

## 后续子批次

#42 保持 In Progress。Bridge/Port/Interface 创建与删除、root strong references/GC、不可变身份重建、成员增减、QinQ/cvlans Advanced 写入和 Interface 属性编辑需要独立方案与验收。本批不变更部署或生产网络。
