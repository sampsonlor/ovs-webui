# #42 C2：独立 internal Port 创建与精确补偿

本批允许在一个已有 system Bridge 下创建新的 internal Port 和 Interface，要求显式 access VLAN（1–4094）。沿用 Candidate → Diff/Validation → Safe Apply → Event/Audit。一般独立端口删除、物理接口接管、Bond 成员迁移、QinQ、Interface 原生属性以及完整表单仍由 #42/#54 后续批次验收。

## 准入与身份

API 1.11.0 追加 `port.create-internal`；请求只有 intent_id、operation、object（已有 Bridge 的完整绑定）、name、vlan_id。管理服务分配新 Port/Interface 的 management_id 与 OVS UUID。一个 Candidate 仅包含一个创建意图，最多捕获 32 个原有 Port。修改 VLAN 保留身份与原始依赖；改名、换父对象或重试已退休身份必须丢弃后重新暂存。禁止 rebase 此类结构操作。

新能力 `ovs.port.internal.create` 与既有 VLAN、Bond、Bridge 创建/删除能力独立。升级不扩大已有角色或会话的授权上限。根服务必须另行配置 `--local-internal-port-targets=<Bridge management-id>:<new-port-name>`，默认空，最多 32 个不重复目标。名称必须为 1–15 个 ASCII 安全字符，不得占用 Bridge/Port/Interface 名称或主机接口名称；probe 接口名禁止配置。Bridge 同名重建不会继承旧 ID 的授权。

父 Bridge 必须仍在同一 root/generation/schema 中、datapath_type 为 system、无 Controller/STP/RSTP/已识别的外部控制，且保留唯一同名 local Port → internal Interface。草稿封存父 Bridge 配置、local 两行配置、绑定与完整 Port 成员清单；Interface 状态更新不算配置漂移。父图改变会阻止执行或补偿。

## 原生执行与恢复

原生事务检查父图与名称空间后，插入固定 UUID 的两行，并且只向父 Bridge.ports 加入新 Port。Port 显式 access/tag，trunks/cvlans 为空；Interface 为 internal，其余配置使用对应 schema 的原生空值。父对象及已有成员不重写，host IP 不配置。事务同时更新 root 的提交标记/next_cfg，读取本次准确 target 并 durable commit。

身份、操作 journal 与四项保护（root、父 Bridge、新 Port、新 Interface）在派发前原子持久化；库存预留两行及对应字节容量。兼容已有内部 `ovs-webui.bridge-creation` 行标记、`ovs-webui.bridge-commit` root 提交标记和 `root.bridge-creation` 保护名，它们现在共同服务于受控图生命周期，不扩展公开输入。

Applied 要求提交标记、准确 target/cur_cfg、新图、父图及新 Interface 的 error/ofport 证据均成立。非 local Interface 仅接受 ofport 1–65279，65534 属于 local Port，不能替代新端口的证据。

回滚仅删除父 Bridge 中新 Port 的精确引用，由 OVSDB GC 回收这两行。所有新行的非状态配置及所有 schema 表中的入站引用（包括 monitor 未订阅的 Mirror 弱引用）都在同一事务中比较。未监控的 policing/mtu_request 或晚到成员/配置变化会阻止回滚。主机接口获得非链路本地地址或上层设备时也拒绝清理。回滚 Applied 必须证明两行与主机接口消失；原有 Bridge/local 对象及成员保持原身份。

删除后的新身份永久 tombstone；同名重建使用新身份。丢失创建或补偿回复保持 OutcomeUnknown/recovery-required：重启从 journal 核对，不重发写入，不猜测 target 或 Applied。撤销创建能力仍触发已授权安全补偿；原生冲突保留保护与人工恢复入口。

## 页面与验收

共享 Diff 的 Standard 显示父 Bridge、新名称和接入 VLAN，Expert 追加父/子身份。新对象尚不存在时不生成详情链接。两模式权限和校验相同；桌面发起新事务，平板/手机审阅和处理既有恢复。完整创建表单仍在 #54，本批通过真实 API 暂存并验收共享正式页面。

见[审阅矩阵](../reviews/INTERNAL_PORT_v0.1.md)。原生字段依据：[Open_vSwitch 数据库手册](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)。
