# 双 SQLite Repository、迁移与故障保护 v0.1

日期：2026-09-14。对应 [#32](https://github.com/sampsonlor/ovs-webui/issues/32)，[PR #58 已接受合并](https://github.com/sampsonlor/ovs-webui/pull/58)至 `7117620`，标签 `phase1-sqlite-repositories-v0.1`。前置 #31 已通过 PR #57 接受合并，基线为 `e61d36c` / `phase1-go-runtime-v0.1`。本文保留 #32 的 schema 2 验收范围；#33 的 schema 3 增量见[公开 API 与请求恢复](PUBLIC_API_v1.0.md)。

本批为两个正式 Go 进程建立持久存储、升级前提和故障状态。普通启动不能重建缺失或损坏的权威库。收到并保存 handoff 回执只证明请求已到达 manager，不代表授权、配置 admission、OVS Commit、Applied 或 Safe Apply Confirmed。

## 存储归属与实现边界

| 存储 | 所有者与默认路径 | 本批持久内容 |
| --- | --- | --- |
| web.db | 非 root `ovs-webd`；`/var/lib/ovs-webui/web/web.db` | owner/revision 约束的 Candidate，用户 preference、Profile/Label/override，session hash 与 SecretStore reference，不可变 handoff outbox 和关联回执 |
| manager.db | root `ovs-mgrd`；`/var/lib/ovs-webui/manager/manager.db` | Principal Repository，角色/Grant/token/policy/generation/identity 的基础 schema，request receipt、transaction journal、Validation/operation protection、Job、Event、Audit 的基础 schema |

两个目录分别为对应进程所有、`0700`；库、锁和 SQLite sidecar 为 `0600` 普通单链接文件。打开与每次操作校验目录祖先、UID、模式、symlink、硬链接及库 inode；目录路径由部署配置提供，HTTP/IPC 请求不能提供路径或 SQL。Linux 独占文件锁与 SQLite OFD locking 保留单一 repository owner；webd UID 无权读取 manager 目录。

`internal/repository/{web,manager}` 提供类型化方法；SQLite SQL/driver 细节留在 repository 内部。State Cache、rolling/LKG checkpoint 和 SecretStore 仍有独立责任，本批不导入 `.ovs-lab/`，也不把 SQLite WAL checkpoint 当作 OVS 配置恢复点。新增安全表仅定义存储结构；认证、角色策略、Grant 撤销及凭据算法由 #34 等任务实现。没有新增公开数据库操作端点或通用 IPC 代理。

按已接受的 ADR 固定 CGO-free `modernc.org/sqlite v1.58.0` 及其 `modernc.org/libc v1.75.6`，依赖记录在 `go.mod` / `go.sum`。Go 1.27.1 的两个正式二进制仍以 `CGO_ENABLED=0` 构建；race 检查在原生 Linux runner 单独启用 CGO。驱动要求的依赖配套与连接选项见[对应版本文档](https://pkg.go.dev/modernc.org/sqlite@v1.58.0)。

## 初始化、打开和迁移

安装阶段先准备目录和固定服务账户，再分别以实际数据库所有者运行显式初始化：

```text
# root，manager 的专用目录已存在且为 0700
ovs-mgrd --database /var/lib/ovs-webui/manager/manager.db --init-database

# 固定非 root 服务账户，web 的专用目录已存在且为 0700
ovs-webd --database /var/lib/ovs-webui/web/web.db --init-database
```

初始化使用 exclusive create；文件已存在、空文件或上次初始化中断均拒绝覆盖。它提交 database identity、kind、software version、迁移账本和当前完整 schema 后退出，不启动网络服务。普通 daemon 启动只打开已有库；systemd 的 `StateDirectory` 创建私有目录，不初始化数据库。账户和完整安装/升级编排留给 #50；TLS 启动前提仍按 #31/#35 处理。

当前每个库均为 schema 2。`internal/migrations/{web,manager}` 嵌入按序 SQL，校验和先将 CRLF 归一为 LF。正常打开先使用只读连接执行 `quick_check`、`foreign_key_check`、kind/identity 与完整 migration checksum 检查。更高 schema、账本缺失或历史 checksum 变化均拒绝打开，不自动降级 schema。

有待应用的迁移时，先创建并验证旧 schema 的一致性快照，成功后才在一个 SQL transaction 中执行所有待应用 DDL、迁移账本及 software version 更新。失败回滚整笔迁移，旧数据和备份保留，服务保持 degraded；commit 结果不明也不继续启动写入。`database_meta.software_version` 记录最近初始化/迁移的软件版本，不在每次只读启动时伪装成当前进程版本。

## 写入与双库恢复边界

每个业务写方法只有 SQL transaction `Commit` 明确成功后才返回成功。单库提交中任何一条 evidence 写失败会回滚整笔写入。磁盘满、I/O 故障或未知 commit 不自动重试；未知结果按原持久 request ID 查询、在恢复后对账。

| 阶段 | durable acceptance 与重启行为 |
| --- | --- |
| 保存 Candidate | 在 web.db 中以 owner + expected revision 比较后提交新 revision；owner 不匹配或旧 revision 拒绝覆盖 |
| `PrepareHandoff` | 先在 web.db 固定 request / transaction / correlation IDs、Candidate revision、不可变 document 与 hash，提交 outbox 后才可进行后续传输 |
| `Receive` | manager.db 在一笔 transaction 中提交 receipt、`received` journal、pending-validation Job、Event 与 Audit；该 journal 明确 `execution_authorized=false` |
| 回执丢失或进程重启 | 使用原 request 和不可变 payload 重试存储交接，manager 返回同一 receipt；相同 request 携带不同身份/hash 拒绝；不能创建第二次执行 |
| `Acknowledge` | web.db 校验 receipt 的 request/trans/correlation/hash，再原子保存 receipt 和 outbox 状态；重复确认幂等，不删除用户后来编辑的新草稿 |

双库不使用 ATTACH 或跨库 2PC。该协议目前由 Repository 测试执行；后续 #33/#34/#38/#39 接入正式请求幂等、认证、验证和 provider admission。它不会自动发出配置 IPC 或启动 OVS 写入，不能被理解为已经交付完整事务状态机。

## 有界并发与故障状态

每库一个 writer、最多四个独立 query-only reader；读写各最多等待 16 项。SQLite busy timeout 为 2 秒，Repository 读预算 2 秒、写预算 5 秒、备份预算 30 秒，继承请求取消。单 document 上限 1 MiB；一次 pending handoff 查询最多 8 项，避免无界读取队列。

连接启用 WAL、`synchronous=FULL`、外键、defensive 与 `trusted_schema=OFF`。SQLite 设置的具体语义见[官方 PRAGMA 文档](https://www.sqlite.org/pragma.html)。每 30 秒执行有界 PASSIVE checkpoint；长读事务阻塞进度时返回 `STORAGE_CHECKPOINT_PENDING` warning，释放快照后可恢复，不误报永久库损坏。

| 状态 | 服务行为 |
| --- | --- |
| 正常 | storage ready/readable/writable；产品 `authentication_ready` / `configuration_ready` 仍为 false |
| 暂时 busy、排队超限或请求取消 | 返回静态错误；本次 readiness probe 失败不能声称 ready，不永久锁死健康库 |
| 磁盘满 | 禁止后续写入并保留可读证据；释放空间后由受控重启重新检查 |
| commit 完成结果不明 | `STORAGE_COMMIT_UNKNOWN`，关闭 writer，保留读取/对账；没有自动重放 |
| 缺失、损坏、schema 不符、路径不安全或库被替换 | 明确 degraded，不重建、不继续高风险操作，保留原文件 |
| 迁移前备份失败或迁移失败 | 拒绝迁移后的服务写入，保留旧库、已有备份和未完成输出用于排查 |

mgrd health 和 webd `/readyz` / `/api/v1/runtime` 携带脱敏 storage code，任一库不可写时返回 503；`/healthz` 仍报告 webd 存活。没有将路径、SQL、数据库错误原文或凭据写入 HTTP 响应/日志。任一管理进程降级或停止都不能停止 OVS 数据面。

## 一致性备份与恢复前提

`Store.Backup` 使用 `VACUUM INTO` 生成包含 WAL 已提交内容的隔离快照，不复制裸主文件。输出位于同一私有父目录下新建的 `backup-UUID/`；数据库快照经过完整性检查、fsync 和 SHA-256 计算后，才写入并同步 manifest 及目录。中断产生的部分输出不会报告成功，也不替换之前的备份。SQLite 关于一致性和中断边界的依据见 [VACUUM INTO](https://www.sqlite.org/lang_vacuum.html)。

`VerifyBackup` 是离线校验入口：限制 manifest 为 8 KiB，拒绝未知字段/额外 JSON 与快照旁未纳入校验和的 WAL/SHM/journal，校验权限、kind、database ID、schema/迁移账本、SHA-256 与数据库完整性。它不覆盖在线库、不激活旧 Grant/session，也不是产品 Restore API。完整恢复须在 #48 处理备份来源、generation/credential epoch 和失效策略，并由 #50 提供安装/恢复编排。SHA-256 提供配套完整性校验，不替代恢复来源认证。

## 验证与评审门禁

本机 Windows 已完成 Linux 编译、vet、依赖校验与测试交叉编译。实现提交 `b568963` 的 [Linux CI](https://github.com/sampsonlor/ovs-webui/actions/runs/34812025289) 已在 `ubuntu-24.04` / `ubuntu-24.04-arm` 原生通过 vet/race、协议 fuzz、CGO-free 构建、真实磁盘满与 systemd/OVS 故障验收。OVS 实测为 3.3.9；[保留证据](../reviews/evidence/SQLITE_REPOSITORIES_v0.1.json)记录测试 merge SHA、job、artifact 校验和、实际场景与原型回归结果，不依赖短期 artifact 长期可用。最终 PR 的 CI Gate 仍以其最新提交为准。

| 验收路径 | 测试入口 |
| --- | --- |
| 显式创建、重启持久、四 reader/单 writer、独占实例 | `internal/repository/sqlite/store_linux_test.go` |
| 空/坏/丢失库、future schema、checksum、权限、symlink/hardlink/文件替换 | 同上；失败启动前后保留原字节及旁侧 sentinel |
| 迁移成功/失败、DDL 原子回滚、旧数据与迁移前快照、WAL/tampered backup | 同上；snapshot 实际重新打开验证 |
| read deadline、BUSY、取消排队、checkpoint warning、未知 commit | 同上；对实际 SQLite 执行查询并验证状态恢复/停止写入 |
| 强杀 SQLite 进程前后 | 子进程未提交/已提交后分别 SIGKILL，重开验证 commit 边界 |
| 真实磁盘满 | `tests/daemon/storage_full.py` 在一次性 runner 挂载专用 2 MiB tmpfs，耗尽空间后检查旧提交恢复 |
| 双库丢回执、新草稿、重复交接及 evidence 原子性 | `internal/repository/recovery_linux_test.go`，关闭并重开两个独立库 |
| 实际进程 UID、systemd 重启、私有数据库与降级后 OVS 转发 | `tests/daemon/smoke.py`，独立 OVSDB/dummy datapath 和唯一服务/数据目录 |

本批没有增加 UI 操作；Standard/Expert、桌面配置、平板审阅/既有 Safe Apply、手机事故协作职责保持已接受定义。本次 CI 同时通过 202 项回归、3 项集成、26 项浏览器测试，以及 pnpm 类型检查、lint 与 `pnpm build`；均保留在 CI Gate。

#32 的实现与证据须单独接受后再合并；当前保持 Open / In Progress。下一项为 #33 正式 REST/OpenAPI 与幂等契约。#29 仍进行中，Phase 1 未完成；Scope 原文仍为 Draft for Review，PR #19 保留独立待审。
