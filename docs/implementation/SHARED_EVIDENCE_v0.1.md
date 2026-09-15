# 共享 Job、请求、Event 与 Audit v0.1

任务 [#37](https://github.com/sampsonlor/ovs-webui/issues/37) / [PR #63](https://github.com/sampsonlor/ovs-webui/pull/63)。以已接受的 [Phase 1 实现设计](PHASE1_IMPLEMENTATION_DESIGN_v0.1.md)、#32 双库、#33 请求恢复、#34 身份授权、#35 TLS 和 #36 库存为基础。

## 持久资源与事务边界

`ovs-mgrd` 的 `manager.db` migration 007 新增共享 Job 元数据、Event/Audit 记录、分页/保留状态和旧记录导入标记。`internal/repository/evidence` 的 SQL 方法加入调用方现有事务。安全变更、Job、Event、Audit 和 API receipt 一起提交；任何记录写入失败都会撤销该次数据库变更。实际 provider 调用不得跨越 SQL 事务，webd outbox 仍只表示传输状态。

安全管理与证书操作使用共享 Job；证书激活保留 #35 的持久截止时间和独立恢复逻辑。真实 OVSDB 库存观察和生命周期决策使用共享 Event/Audit。应用运行日志继续用于排查进程问题，不作为 Audit 权威。#32 未授权的 handoff 仍是 transport-only：保留原记录，公开其来源未知的 Event/Audit，不把旧 handoff owner 字段当作执行授权，也不把其原始 Job 暴露成已授权业务 Job。

管理请求在执行变更前生成一个 correlation，传入 Job 和证据，最后写入同一事务的 receipt。保留 #33 的原 receipt 优先、epoch、新 key 时效、内容指纹、同 key 冲突与一次性 secret 不重显规则。新 request ID 不用于重试原命令。终态 Job 可以完成关联 receipt 的处理状态；receipt 的 effect 不据此升级为 OVS 已提交或 Applied。

## Job 生命周期

每个 Job 有稳定 UUID、owner、原操作 capability、原请求/credential/correlation、资源/ChangeSet/transaction 引用和从 1 开始的独立递增 sequence。全局 revision 仅用于使变化中的 Job 分页失效。

| 状态或证据 | 含义 |
| --- | --- |
| queued | 已持久接受，尚未执行 |
| running | 受信任执行器正在处理；执行前仍须重新核对当前权限与业务前置条件 |
| cancel-requested | 取消请求已记录；不能推断 provider 已停止 |
| needs-attention | 重启等原因导致结果需要核对；不自动重发可能已发送的工作 |
| succeeded / failed / cancelled | Job 处理终态；业务结果、commit、Applied 和确认各自独立 |
| dispatch_state / business_outcome | 发送证据与业务处理结果 |
| commit_outcome / applied_outcome / confirmation_state | OVS 提交、实际生效与确认的独立证据 |

取消本人且当前仍有原操作 capability 的任务，还要求 `jobs.cancel`。未发送的 queued Job 可原子变为 cancelled；运行中的任务只能记录 cancel-requested。取消命令自身的成功 Job 表示请求已记录，通过 resource_ref 指向目标 Job。重试返回原取消 receipt，不再执行第二次。

通用服务保守限制 2 个运行中 Job、16 个等待 Job和 100k 条保留 Job。终态的同步安全管理回执不占运行槽；TLS 恢复定时器不依赖通用队列。执行器必须使用 mgrd 的当前授权与受控 provider，SQL 生命周期方法不提供任意命令执行能力。本批没有添加配置或诊断工作调度器；#38/#39/#46 接入各自真实执行器及更细的业务入场检查。

启动时分批导入旧记录，保留 ID、Job sequence、已知 credential 和唯一匹配 receipt 的 correlation。无可信来源的旧记录明确为 Unknown；不能根据 owner 或文本猜测操作者。每批最多 128 条，可在中断后继续。通用 running/cancel-requested Job 恢复为 needs-attention，保留原 receipt 未解决状态；重复启动不重复产生恢复事件。TLS Job 使用其专门恢复路径。

## 权限、读取与导出

Public v1 增量版本为 1.4.0，共 122 路径 / 139 操作，保持冻结 v1.0 基线兼容；IPC 继续使用已校验的类型化 auth.read / auth.command 操作。

| 路径 | 行为与权限 |
| --- | --- |
| `/api/v1/jobs`、`/jobs/{job_id}`、`/jobs/export` | `jobs.read`，本人 Job，且当前仍具备原操作 capability；每次读取与 WS 引用授权均重查 |
| `/api/v1/jobs/{job_id}/cancellations` | 当前权限与 owner 检查，持久幂等取消 |
| `/api/v1/events`、`/events/{event_id}`、`/events/export` | 显式 `events.read` 权限 |
| `/api/v1/audit`、`/audit/{audit_id}`、`/audit/export` | 显式 `audit.read` 权限 |

表中后续简写路径同属 `/api/v1`。既有 Reader 模板包含 Event/Audit 全局读取权限；这不授予读取其他用户 Job 的权限。Standard/Expert 不改变权限。对象、Job、事务、Event/Audit 和原请求通过稳定 ID 相互关联；完整页面与点击路由仍由各功能主单及 #54 实现。

集合接受 operation 子串 `filter`、`correlation_id`、`object_id`、`job_id`、`origin`（Manager/External/Unknown）、`limit` 和 `cursor`。默认 100、最多 500 条；实际响应另有 40 KiB items 预算及既有 48 KiB IPC 响应限制。先授权后分页，不输出未授权总数。cursor 经 AEAD 保护，绑定主体、当前权限版本、接口、过滤、limit、保留 epoch 和 30 秒有效期。Event/Audit 以 sequence 上界固定快照，后续追加不混入旧页；Job 变化或记录回收使相关 cursor 返回 410。

导出使用相同权限和分页机制返回 JSON，显式标注 `authorized-retained-records`、`truncated`、`next_cursor` 和保留范围。导出不承诺包含已回收记录，也不生成无限大下载文件。大于一页的调用方必须按 cursor 读取；过期后重新开始，不拼接两个快照。

## 证据与中央脱敏

追加者只能提供结构化 ID、已知结果/操作代码和资源引用。actor、credential、capability 来自 mgrd 已认证请求；系统恢复没有操作者时明确 actor_state=unknown。Record 的自由文本 Details 不序列化，密码、Token、grant、HTTP header、证书私钥、原始选项和命令输出没有可序列化槽位。summary 由中央固定模板生成。

同 collection/source/dedup key 且相同内容返回原 ID；同 key 内容不同返回冲突。需要跨重试去重的生产者应保留原 correlation 与 dedup key。真实 OVSDB 观察到快照变化时记录 External，保留 generation 引用，不猜测 OS 用户。周期重复观察不产生变化事件。后续 mgrd OVS 写入必须由事务执行器提供可核对的归属证据，不能仅按时间接近推断操作者。

## 保留与故障

| 集合 | 最短保留时间 / 容量 |
| --- | --- |
| Event | 30 天 / 100k |
| 终态 Job | 终态后 30 天 / 100k |
| Audit | 180 天 / 500k |
| terminal API receipt | 30 天；存在关联 Job 或证据时继续保留 |

每分钟在 manager writer 内执行有界维护，每集合最多回收 256 条。活跃 Job、同 correlation 的未解决任务、活跃 transaction journal 引用的记录受到保护；仍有记录引用或未完成 receipt 的 Job 也保留。证据写入者必须填充真实 Job/transaction/correlation 引用，不能只把引用放入日志文本。终态 Job 因关联 Audit 可保留超过 30 天。删除旧记录、更新可见 `pruned_through_unix_ms`、更换 retention epoch 在同一事务中完成。

墙钟倒退暂停回收，不使历史记录提前到期。容量不足拒绝新写，绝不按压力丢弃 unresolved evidence；存储错误沿用 #32 的 degraded/recovery 行为。有界 writer 队列和磁盘满恢复仍由正式 SQLite 服务验收。健康页面与更多运维提示由 #45/#54 接入。

技术验收、双架构结果与长期证据见 [本批审阅](../reviews/SHARED_EVIDENCE_v0.1.md)。
