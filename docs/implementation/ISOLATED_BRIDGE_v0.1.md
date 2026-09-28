# 隔离 Bridge 创建与精确补偿 v0.1

对应 #42 子批次 B。前一批已有 Port 的 Bond/LACP 见 [A 批](PORT_BOND_LACP_v0.1.md)。本批交付 `bridge.create-isolated`：创建一个全新的 `system` Bridge、同名 local Port 和 `internal` Interface，`fail_mode=secure`，无上联、地址、控制器或现有成员接管。已有对象删除、独立 Port 创建、Bond 成员变更、QinQ 和 Interface 原生属性仍由后续子批次交付，#42/#21/#22/#54 不因此关闭。

## 入场与身份

公开 API 1.9.0 在既有 intent union 末尾新增操作，不改变旧 `bridge.create` 等保留形状；这些尚未实现的旧形状仍显式拒绝。新操作只接收 intent_id、operation 和 name，name 为 1–15 个受限 ASCII 字符。一个 Candidate 只容纳一个新建图；不支持混合字段/创建批次。

所有配置仍沿 Candidate → Diff/Validation → Safe Apply → Job/Event/Audit；stage 和 validate 不写 OVS。mgrd stage 分配三组独立 management_id / OVS UUID，签名包含名称、generation、schema、root 和引用关系，编辑不能换名或重新分配身份。未执行的稿件没有身份库占位；执行入场将三个 pending 身份、私有预期标记和事务 journal 原子持久化。只有监控到相同 UUID 且匹配提交标记的行才激活保留身份，外部元数据不能自行指定 management_id。

新能力 `ovs.bridge.create` 独立于 VLAN/Bond。已有角色/credential ceiling 不会随升级自动扩大，必须显式授权并重新登录。root 还须配置 `--local-bridge-create-names=br-example`；默认空列表禁止创建。列表只授权全新名称，不把同名已有对象纳入管理。禁止与 probe 接口同名，未知 schema、外控 root、已有 OVS 名称或 Linux 网络接口阻止执行。撤权使待确认事务补偿。

创建和补偿共用 root 的专用 Bridge recovery marker，因此未决创建占用 `root.bridge-creation` 保护；既有 Port 字段组仍独立，未新增全局配置锁。共享管理路径恢复域沿用 #40。被拒绝/删除/回滚的身份永久 tombstone，重新创建同名对象使用新身份；旧 Candidate 不能复活它，需移除后重新 stage。名称只用于唯一性检查，不用于历史身份重绑定。身份数量沿用 16,384 上限，耗尽拒绝新事务，不清除未决记录。创建前为库存 2,048 行 / 4 MiB 上限预留三行与相应字节空间；容量门禁不阻止已有图的精确清理，其他写入者造成的后续库存耗尽仍按 provider degradation 保留恢复状态。

## 原生事务与证据

编译器根据实际 schema 检查 root-set、强引用、唯一索引与可保护引用类型，生成一笔有界 OVSDB 事务：零超时 UUID/名称不存在断言 → 三行 insert → root.bridges 插入引用 → root 提交标记 / next_cfg → 精确 target select → durable commit。只修改新行及 root 中目标引用/标记，不替换现有列表。每个 insert 回复 UUID 必须与已持久化计划一致。

OVS 2.13 起允许 insert 指定 UUID，本批仅在原生矩阵验证过的 OVS/schema 组合上接受该执行方式，不能以 schema 版本推断其他实现具备此扩展。[OVSDB server §5.2.1](https://docs.openvswitch.org/en/stable/ref/ovsdb-server.7/#insert)；表和引用语义见 [OVS 配置数据库](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)。不支持该扩展的 server 会原子拒绝，不能回退为按名创建或自动重发。

Applied 需要原 generation/file/peer/schema 连续、确切 next_cfg target、cur_cfg 到达、三行固定身份和完整配置图，以及 local Interface 的空 error / ofport=65534。最终证据用只含 wait/select 的实时事务复核，缓存不是最终证据。确认还需共享服务端窗口和真实 management probe。返回丢失时 root 标记可证明提交，但不能从后来的 next_cfg 猜测原 target；保留 RecoveryRequired，绝不重发。

## 补偿与边界

回滚从不可变原计划派生。一个原生事务中检查三行全部非临时配置列（仅排除明确的 daemon 输出列）、完整引用链、所有表对这三行的引用（包括未监控的 Mirror weak refs），然后仅移除 root.bridges 中本次 Bridge 引用，让 OVSDB GC 收走三个非 root 行。新增成员、QoS、Mirror、Interface policing、metadata 或其他配置使 CAS 拒绝，保留 Rollback Conflict 及保护范围。不会自动清理第三方依赖或覆盖外部配置。

Linux 侧在入场和派发前拒绝已有接口名；补偿前拒绝已出现的可路由地址、master/upper 设备依赖。主机网络和 OVSDB 不具有跨系统原子事务；这组保守检查不构成主机网络配置管理。root 应将允许的名称留给本服务使用，主机网络写入/管理路径迁移仍属后续独立服务范围。本操作不添加 IP 或移动已有物理口，不能代替唯一上联/管理网络迁移验收。

回滚有独立提交标记、精确 target 和 Applied 证明。丢失清理回执后即使图已消失也不推断 RolledBack，继续保留恢复记录。confirmed Last Known Good 表示已验证的目标图，不授权未来复用旧身份。

## 前端与验证入口

已有 Svelte Candidate/Diff/Safe Apply 识别新对象类型与实际 intent 权限，新建 Bridge 不生成错误的 Port 链接。Standard/Expert 使用同一授权与验证；平板/手机只能查看新事务草稿，不能开始新高风险变更。完整 Bridge 创建表单仍属 #54，本批浏览器测试从正式 API stage 后验证共享界面。

`TestNativeIsolatedBridge` 在 amd64/arm64、schema 3.3.9/3.7.1/4.0.0 分别覆盖 10 个场景，含真实 kernel local-interface 创建/清理；测试替身场景只用于确定性竞争和失败注入。第 8 个正式浏览器场景验证真实 HTTPS/IPC/OVS、两种信息深度、窄屏职责、共享 Apply/rollback 和截图。验收结果见 [审阅矩阵](../reviews/ISOLATED_BRIDGE_v0.1.md)。
