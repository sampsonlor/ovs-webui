# 正式 Candidate、Diff 与 Validation v0.1

日期：2026-09-15。任务 [#38](https://github.com/sampsonlor/ovs-webui/issues/38)，依赖已接受的 #33–#37。首个正式 typed intent 是 `port.vlan.set`；全量 VLAN 页面 #22、其他 intent 执行器、Apply #39、Safe Apply #40 和 Svelte 首切片 #41 分别验收。

## 权威与持久化

web.db 的 `candidate_workspaces` 按实际认证 principal 保存一个 Candidate，强 ETag 和 SQL CAS 保护 revision。stage 更新已有 intent 时保留首次原始值；remove/discard/rebase 均生成新 revision。workspace receipt 与保存同事务提交。API 请求不包含 before、角色、权限、校验结果或 live-save。

mgrd 从同一个新鲜 OVSDB monitor 快照投影真实 Port binding、四个 VLAN 原生字段、schema 约束和相关 Bridge/Interface 依赖，并用私有密钥签名捕获的原始数据。webd 只能保存该 envelope；篡改 web.db 的 before、对象、owner 或 revision 会在 mgrd 校验失败。没有直接 OVSDB transact 或 shell 执行入口。

manager.db 的 `candidate_witnesses` 保存 workspace epoch、Candidate ID 和单调 sequence/revision，拒绝旧副本回退。它是观察到的版本证据，不是第二份可编辑草稿。webd 每次读取/验证都提交当前本地版本；更新后的草稿会使旧 Validation 不可继续使用。管理恢复导致 epoch/identity 不匹配时需要显式核对，不自动接管旧草稿。

POST `/validations` 先在 web.db 保存不含凭据的不可变 outbox，mgrd 独立检查真实凭据、签名和原生依赖。最多 32 个 intent 的校验是有界内存计算，完成的 Job、Validation、typed Diff/ChangeSet、Audit 和原请求 receipt 在同一笔 manager.db SQL 事务提交后返回 202。Job succeeded 只表示完成校验；Validation 可以是 blocked。没有需要重启后盲目重跑的在途 provider 写操作。

所有 IPC/原生快照读取在数据库写事务之外完成，SQL mutation 只使用准备好的有界数据并重验本库授权。双库之间没有 2PC。webd 的 ack 只保存关联资源；丢失 ack 不证明管理端未接受。相同 request/domain/epoch/body 优先重放原 receipt，不能修改 payload 或用新 ID 掩盖不确定结果。

## 校验和三方比较

- `vlan_mode/tag/trunks/cvlans` 作为相关字段组比较。Diff 保留 Original、Current、Yours、object、field、operation 和 authority。实际配置未改变时，其他对象或运行统计变化不制造全局 config_revision 冲突。
- 当前 schema 必须具有可变、非 ephemeral、已监控且约束受支持的四个字段；允许 mode 来自实际发现的 enum，普通新值限制为 VLAN 1–4094。未知模式、观察到的 0/4095、已有 cvlans 和未交付的 QinQ validator 会明确阻止通过，不清空或静默转换。
- 原生缺省 mode 以 null 保留在 before/Diff，明确提示；新值必须由用户显式选择。空 trunks 在 trunk/native 模式下表示所有 VLAN，检查结果明确显示范围。依据 [OVS Port VLAN 原生语义](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)。
- stage 可以保存待审意图，validation 需要 `configuration.validate`、`configuration.read`、`inventory.read` 和实际 `ovs.port.vlan.write` 能力，并重新检查当前角色与凭据上限。Standard/Expert 不进入授权参数。
- VLAN 字段默认 ownership unknown。root 管理的 mgrd 参数 `--local-vlan-ports=<Port management UUID,...>` 是经过审阅的字段归属声明，最多 128 项；默认空，重启加载。已识别的 OVN/Neutron 控制证据优先阻止本地验证。OpenFlow Controller 不被推断为所有 OVSDB 列的所有者。该参数不开放 Apply。
- Bridge 归属、Port member Interfaces/类型/options 和字段 authority 是相关依赖；不以全库 config_revision 相等作为通过条件。缺失绑定、未知原生值、provider unavailable/stale、跨 generation 均失败关闭。
- rebase 必须携带 GET Candidate 返回的 generation、当前 revision 和 conflict_snapshot_id；冲突项必须显式 keep-current 或 keep-mine。前者移除该 intent，后者用经过用户审阅的当前值替换 baseline。陈旧 snapshot、schema 改变和 generation/对象身份变化不自动处理。

Validation 的 `state` 保留当时结果；`usable/invalidations` 在每次 REST 读取时重新计算，受当前 Candidate revision、原验证凭据、当前 policy/capability、schema、provider 归属策略、对象 generation、相关字段/依赖和 300 秒有效期约束。换凭据可在现有权限内读历史，但不能继承旧凭据的可执行资格。失效不会把历史 passed 改写成一次新校验。

## 后续执行必须遵守的边界

`execution_ready` 始终 false，风险为 connectivity-unknown-safe-apply-required。#39 必须在新事务入场前加载当前 web.db Candidate、同步 manager witness、核对原 Validation 对应不可变 envelope，并由 mgrd 再验原始字段、grant、policy、generation 和短时 preflight。不能把 `usable`、Job succeeded 或 webd 交接应答当作授权/Applied。

由于不存在跨库原子提交，草稿刚提交而 witness 同步应答丢失的窗口由当前 web.db revision 检查关闭；下一次读取/验证重新同步。离线保存的回执可恢复，新的验证不会绕过 mgrd。候选原始快照和 validation 记录包含配置数据，受当前 owner 与 configuration.read 控制；Audit 只记录固定元数据，不携带原生 options、密码或 grant。

大尺寸 Current Diff 超过响应预算时，GET Candidate 保留完整私有意图，返回 `diff_truncated: true`、`review-limited` 和阻止继续比较的 gate；不返回可供 rebase 使用的 conflict snapshot。用户仍可移除或丢弃意图，不能把部分比较当作完整审阅。

默认预算：32 intents、40 KiB 私有 envelope、48 KiB validation representation、10,000 个 workspace、10,000 个 validation/outbox；容量不足拒绝新入场。公共请求最大 1 MiB 不承诺每个合法大数组都能放入当前资源预算。历史 validation 引用的 Job 不被共享 TTL 回收删除；当前批次达到持久记录上限后拒绝新增，不宣称已交付完整配置历史清理或恢复编排。

迁移只新增 web 006 / manager 008；已有 SQL 迁移和 v1.0.0 契约 baseline 保持不变。公共契约 1.5.0 增加可读字段，IPC 1.4 增加三个固定 typed 操作并校验严格版本/摘要配对。
