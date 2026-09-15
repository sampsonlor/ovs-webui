# Candidate / Validation 审阅 v0.1

日期：2026-09-15。任务 [#38](https://github.com/sampsonlor/ovs-webui/issues/38)，[PR #64](https://github.com/sampsonlor/ovs-webui/pull/64)。**实现与技术验收完成，等待用户接受。** 前置 #37 / PR #63 已接受合并为 `ba8a4ee`，标签 `phase1-shared-evidence-v0.1`。

正式服务可按用户保存 Candidate，显示 Original / Current / Yours 三方 Diff，并生成不可变 Validation、typed ChangeSet、Job、Audit 和原请求 receipt。首个 intent 是 `port.vlan.set`。实现与边界见 [Candidate / Diff / Validation](../implementation/CANDIDATE_VALIDATION_v0.1.md)。

## 路径与异常验收

| 场景 | 实测结果与约束 |
| --- | --- |
| 正常路径 | HTTPS 保存 VLAN 意图，强 ETag / SQL CAS 更新私有草稿；mgrd 从真实 Port / Bridge / Interface 和发现的 schema 捕获原始字段，签名后持久保存。默认 ownership unknown 阻止 Validation，通过 root 审阅声明归属并显式 rebase 后校验通过；Job / Audit / ChangeSet 关联一致 |
| 原生语义 | Native tagged / untagged、缺省 mode 的 null 原始值及空 trunks 明确保留；保留原生整数的精确表示，按实际 schema 可变性、enum、基数和整数范围验证。未知字段、保留 VLAN、未交付的 QinQ validator 和外部控制证据失败关闭 |
| 三方冲突 | Live 原为 38，意图为 67，外部改为 39；Diff 显示 38 / 39 / 67。缺少显式选择或 snapshot 已变化时拒绝 rebase；keep-mine 捕获已审阅的 39，历史 Validation 保留 passed 但不可复用 |
| 并发与丢应答 | 同 revision 的两个并发保存只有一个成功。丢弃保存/验证响应后按原 domain / epoch / request ID 恢复原 receipt；同时重启 webd / mgrd 后仍保留草稿、验证与关联 |
| Provider 失联 | 草稿显示 stale，旧验证不可用；已经提交的原保存和验证请求可重放 receipt，不依赖 provider 恢复。未提交的新操作无法绕过 provider。所有测试中 Candidate/Validation 均未改变 Live，最后仍为外部设置的 39 |
| 身份与权限 | 两个真实认证用户的草稿、Validation 与 workspace receipt 相互隔离；角色撤销后立即拒绝读取。Go 测试还验证凭据更换、策略/能力上限、expiry、原始值篡改、跨用户冒用及版本失效 |
| 身份生命周期 | 真实 Port 同名重建后旧 binding 和验证失效；真实 OVS 数据库 copy-back 经显式核对进入新 generation 后，旧验证继续不可用，不能自动将旧意图绑定到新对象 |
| 原子性与界限 | 注入 Validation SQL 失败时 Job / Audit / receipt 同时回滚；草稿保存后 witness 同步丢失的窗口由当前 web.db revision 检查关闭。超预算拒绝入场；过大的 Current Diff 保留完整意图、显示 review-limited，并阻止部分审阅后 rebase |
| IPC | 非空草稿的读取、编辑、验证请求完整往返且签名原始值不变；未知字段、大小写别名和重复键仍被拒绝 |

## 实测证据

[功能 CI](https://github.com/sampsonlor/ovs-webui/actions/runs/34941958450) 的提交为 `301e3f23bee8fdeb6229fdcfaaa612386ba1b859`，六个 Job 全部成功。[长期证据](evidence/CANDIDATE_VALIDATION_v0.1.json)保存 artifact SHA-256、实际测试的 PR merge SHA、测试名称、原生 smoke 结果和三份 schema 的摘要，避免只依赖短期 CI artifact。后续仅整理文档/证据的当前 PR 提交检查另见 PR Checks。

- 原生 Ubuntu amd64 / arm64 各 **126 项 Go race 测试**，Go 1.27.1；包含本批新增的 13 项顶层测试、既有存储、安全、IPC、库存与共享证据回归。
- 两种架构分别在真实 OVSDB / ovs-vswitchd 中加载 **3.3.9、3.7.1、4.0.0 三份上游 schema**，完整执行上述 HTTPS → Unix IPC → OVSDB 场景。CI 安装的是原生 OVS 3.3.9 程序；这不是三个产品二进制或全部发行版/内核的资格认证。
- 实际 systemd / Unix peer / OVS 转发隔离 smoke、专用 tmpfs 磁盘满与持久恢复、认证撤销与重启、TLS 新连接确认及真实 120 秒未确认恢复均通过。
- **208 项回归、3 项 HTTP/重启集成、26 项浏览器检查**通过，无跳过；typecheck、lint、冻结 v1.0.0 baseline 兼容、生成契约检查与 `pnpm build` 通过。
- 迁移只新增 web 006 / manager 008；公共契约 1.5.0 保持 122 路径、139 操作、58 Scope / 53 页映射，IPC 1.4 固定三项 Candidate 操作。现有迁移、锁文件及部署配置保持原状。

## 交互与审阅处置

本批交付正式 Go 后端。既有原型的正常路径、异常路径、Standard / Expert 与平板/手机责任矩阵由浏览器回归保留；页面尚未连接这套正式服务，不能将原型展示当作正式前端完成。#41 负责首切片前端，#22/#54 负责完整功能及页面。

Validation 的历史 state 与当前 usable 分开；Job succeeded 不替代 Validation passed，Validation passed 也不代表 OVS 已提交或 Applied。`execution_ready` 始终 false，风险为 connectivity-unknown-safe-apply-required；保存和验证没有 OVSDB 写操作。root Port allowlist 只声明 VLAN 字段归属。

技术验收通过；#38 保持 Open / Project In Progress，PR #64 待用户接受后合并。#39 OVSDB 字段级执行、OutcomeUnknown 与 Applied 证据保持 Todo，随后由 #40/#41 完成安全执行和前端闭环。
