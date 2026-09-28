# #42 C3：独立 internal access Port 删除与补偿恢复

本批在 C2 基础上增加已由管理服务创建的独立 internal Port/Interface 删除。父 system Bridge、local Port/Interface 与其他成员始终保留原身份。流程仍为 Candidate → Diff/Validation → Safe Apply → Event/Audit。物理接口接管、一般图删除、成员迁移、QinQ、Interface 原生属性和完整表单仍分别验收。

## 准入和身份

API 1.12.0 追加封闭意图 `port.delete-internal`，仅接受 intent_id、operation、object（原 Port 的完整绑定）。调用方不能提交源图、恢复身份、标记或私有状态。服务端封存当前 access VLAN、两行身份与配置依赖、父 Bridge/local 对象身份和配置、排除待删对象后的完整成员集合。禁止混合批次、静默 rebase 或重新分配同一草稿的恢复身份。

独立能力 `ovs.port.internal.delete` 不从创建、VLAN、Bond 或 Bridge 删除能力继承；升级不扩大现有角色、会话和 token 的授权上限。根服务另行配置 `--local-internal-port-delete-targets=<Bridge management-id>:<managed-port-name>`，默认空，最多 32 项。父 Bridge 同名重建不继承原 ID 的授权，管理探针接口名称不能作为删除目标。

两行必须在私有身份仓库中 active、具有相同创建凭据，且原生 external_ids 与私有凭据一致。单独伪造 OVS 标记不能获得管理身份。目标不能是 local Port，必须只有一个同名 internal Interface、明确 access VLAN 1–4094、空 trunks/cvlans；父对象仍满足 C2 的 system/本地控制要求。完整原生配置和跨表入站引用由 provider 再检查，不能仅凭 monitor 投影认定可删。

## 原生执行与恢复

派发前在同一数据库事务内预留两个全新的补偿身份、journal，以及六项保护：root、父 Bridge、原 Port/Interface、恢复 Port/Interface。库存容量检查考虑删除原两行后的补偿容量；历史身份容量仍有独立硬上限。

删除前使用只读 OVSDB wait 证明源图、父图和入站引用。实际写入在同一原生事务重复这些检查，包括 monitor 未订阅的 Mirror 弱引用及 policing/mtu_request 等配置。写集只删除父 Bridge.ports 中该 Port 的精确引用，由 OVSDB GC 回收两行；同时记录 root 提交标记、递增 next_cfg、读取准确 target 并 durable commit。主机接口上的非链路本地地址或上层设备会阻止删除；本操作不配置主机 IP。

确认删除后，原身份保持 tombstone，未使用的恢复身份也永久退休。回滚使用预留的新 UUID 重建同名 access Port/Interface，仅重新挂接父 Bridge；额外原生等待证明原两行仍不存在。原父图或成员变化、名称被占用、原身份重新出现都会阻止恢复，不能覆盖外部对象。

Applied 需要准确 target/cur_cfg、提交标记和原生图证明。删除还要求主机接口消失；恢复要求新 Interface 的 error 为空、ofport 为普通端口有效范围。只有补偿提交、Applied、健康检查和终态全部成立，身份映射才是 restored；pending/OutcomeUnknown 不提供可用恢复对象的断言。删除/恢复回复丢失后保留保护，重启读取 journal 核对，不重发、不猜测 target。

## 页面和验收

Standard Diff 明确显示删除的父对象、名称和 access VLAN，并说明保留父图与新身份恢复。Expert 展示源/恢复绑定；两模式权限和校验一致。桌面发起新操作，平板和手机支持审阅、处理已有 Safe Apply。共享事务页展示两个恢复身份，只在 restored 时提供新 Port 链接；旧身份仍为 404，不重定向。完整创建/删除表单继续由 #54 负责，本批通过真实 API 暂存并验收共享正式页面。

验证要求与保留证据见[审阅矩阵](../reviews/INTERNAL_PORT_DELETE_v0.1.md)。本批沿用既有三份真实 OVS schema 和 C2 图编译器，无新的数据迁移或部署访问变更。
