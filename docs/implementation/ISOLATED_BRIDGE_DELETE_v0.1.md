# #42 C1：已管理隔离 Bridge 删除与补偿重建

本批继承 [B 批创建边界](ISOLATED_BRIDGE_v0.1.md)，允许删除本服务创建、配置仍与原始隔离图一致的 Bridge、同名 local Port 和 internal Interface。一般已有 Bridge、独立 Port/Interface 生命周期、成员变更、QinQ、原生属性及完整表单仍为 #42/#54 后续范围，不关闭主任务。

## 准入与原生执行

API 1.10.0 追加 `bridge.delete-isolated`，输入仅 intent_id、operation、Bridge object binding。操作遵循 Candidate → Diff/Validation → Safe Apply → Event/Audit，不提供对象直接 DELETE 或 live-save。单个 Candidate 只承载一个隔离图删除。

删除须独立授予 `ovs.bridge.delete`，并由 root 配置 `--local-bridge-delete-names=br-example`；默认空列表。创建名额和创建权限不会授权删除。旧角色及 credential ceiling 不随升级扩大，授权变化后须重新登录。probe 接口名称禁止进入删除列表。

名称与 OVS external_ids 均不构成管理来源证明。三行都必须具有 manager.db 中已激活的创建记录，且私有创建标记、表、management_id、OVS UUID、generation 一致。检查当前 schema、root authority、依赖、精确配置和补偿容量。

执行前做只读原生检查，包括 monitor 未订阅的 Interface 配置与 Mirror 等表的引用；同样的 wait 条件和删除写入处于一个原子事务内，阻止预检查后的外部竞争。只移除 Open_vSwitch 的精确 Bridge 引用，让原生 GC 回收图，不按名称删除，不清空外部依赖。Linux 中出现非链路本地地址或上层设备时拒绝删除；Applied 还要求原生图及同名主机接口均已消失。

## 身份和恢复

Candidate 保存原图与三组全新补偿身份。准入时，在同一 SQLite 事务中持久保留三个 management_id / OVS UUID、预期回滚标记、journal 及七项保护：root 加原图/补偿图各三个对象。身份容量不足时删除不会开始。

删除后的原身份永久 tombstone。确认删除会永久退休未使用的补偿身份；回滚以新身份重建相同隔离配置，不复用旧 UUID。补偿只在原对象仍不存在、同名空间为空、generation/schema/root authority 及事务证据仍成立时派发。已预留的补偿身份同样不接管外部对象。

同名抢占或依赖冲突进入 rollback-conflict；provider 不可用与丢失回复保留 recovery-required。重启读取持久计划和派发记录，不重发删除/重建，不从创建标记或对象名称推断 next_cfg target、Applied 或健康。

公开事务新增 `identity_replacements`，每项记录 previous / replacement / state：

| 状态 | 含义 |
| --- | --- |
| reserved | 已保留供补偿使用，不证明对象已恢复 |
| restored | 回滚已终结，原生 Applied 与管理可达性均已证明 |
| not-used | 已确认删除或原删除明确未提交，补偿身份未使用 |
| unverified | 恢复证据不足或冲突，不提供新对象导航 |

Standard 提示删除范围及回滚身份变化；Expert 额外展示旧/新管理 ID 与 OVS UUID。两模式共用同一权限与校验。桌面允许新事务；平板/手机保留审阅和既有事务恢复职责。完整创建/删除表单仍在 #54，本批正式浏览器用真实 API 暂存，并验收共享页面。

## 验收

见 [审阅矩阵](../reviews/ISOLATED_BRIDGE_DELETE_v0.1.md)。每架构三份 schema 各运行 13 个原生场景；含真实 Linux 接口删除/重建、主机地址拦截、晚到外部写入、丢失回复、重启与撤权。正式浏览器增加独立账户场景，验证列表/详情一致的新身份映射、旧对象 404、Standard/Expert、窄屏职责与恢复导航。

原生依据：[OVSDB server 扩展](https://docs.openvswitch.org/en/stable/ref/ovsdb-server.7/)（显式插入 UUID）、[Open_vSwitch 数据库](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)（root 可达性/GC 与 local Interface）。本项目进一步要求补偿使用新身份，避免旧管理身份复活。
