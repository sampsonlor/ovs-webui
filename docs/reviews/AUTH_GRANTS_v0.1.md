# Auth Grant 技术审阅 v0.1

日期：2026-09-14。任务 #34 / [PR #60](https://github.com/sampsonlor/ovs-webui/pull/60)。实现及技术验收完成，等待用户接受，保持 Open / In Progress；尚未建立本批接受标签。前置 #33 已合并并接受，基线为 `phase1-public-api-v1.0`。

本批提供 #20 用户/角色、#27 AAA、#28 API Tokens 共用的真实 Go 身份与授权基础。完整功能页面与后续 provider 保留各自验收。实现与初始化说明见 [AUTH_GRANTS_v0.1](../implementation/AUTH_GRANTS_v0.1.md)。

## 已验证行为

| 要求 | 正常路径与异常证据 |
| --- | --- |
| Local 登录与初始管理员 | 一次性本地 root CLI、完整成本 Argon2id；重复初始化拒绝，无默认用户/密码或 HTTP 初始化入口 |
| 每操作授权 | mgrd 从编译后的 registry 选择实际操作，重新校验当前权限、授权上限、对象、风险与前置条件；客户端 actor/role/mode 无授权效力 |
| 撤权与执行顺序 | 并发撤销和持久化操作经过同一决策门；先撤销则无副作用，先完成则保留回执，race 测试验证结果与持久证据一致 |
| 最后本地管理员 | 禁用、删除、移除角色及间接修改角色权限均受原子保护；错误 If-Match 拒绝且不改变权限 |
| 浏览器会话 | 登录 Origin、Cookie 属性、会话绑定 CSRF、重复或混合凭据拒绝；API Token 与私有 grant 不能冒充浏览器会话 |
| 持久会话 | 加密映射绑定数据库身份和 Cookie；换行、换 Cookie、换密钥、篡改正文失败；真实双进程分别 SIGKILL 后保留原到期时间 |
| 权限与作用域 | 当前角色降权立即收紧既有会话和 Token；既有 grant 不突破签发上限；NetworkAdmin / SecurityAdmin 保持职责分离 |
| Token 与请求恢复 | 明文仅首次返回，重放只取原回执；按当前权限和原操作授权读取；撤销、过期、跨所有者访问受到检查 |
| 到期与 epoch | idle / absolute / elevation 分别到期；提权到期不终止普通会话；认证 epoch 改变同时废止旧 grant/Token；时钟回退拒绝授权 |
| 密钥边界 | 显式初始化、服务账户私有权限、禁止覆盖、符号链接和多硬链接；webd UID 不能读取 manager.db 或 mgrd 密钥 |
| 持久证据与预算 | 安全副作用、终态 Job、回执和脱敏 Audit 同事务落库；Job 与回执关联一致；密码并发、尝试次数及分页均有上限 |
| 共享运行时与存储 | 两种架构保留原有真实 2 MiB tmpfs 满盘恢复、systemd/Unix peer、双库降级和隔离 OVS 转发验收 |

真实 HTTPS 验收还验证：篡改 web.db 无法伪造管理员；恢复已经登出的旧加密映射无法撤销 mgrd 的登出记录；日志、回执和安全 Audit 不含密码、Cookie、CSRF 或 Token 明文。

## 执行证据

[完整 CI](https://github.com/sampsonlor/ovs-webui/actions/runs/34827812912) 六项全部通过。对应功能提交为 `3e336755bfc04c002294b3c120fb7df4a6b8c558`。[长期证据](evidence/AUTH_GRANTS_v0.1.json)保留提交、GitHub 实际测试 merge SHA、测试名称、服务指标、原生架构和 artifact SHA256；后续仅补充文档与证据的提交仍由 PR 检查核对。

| 原生 Linux 架构 | Go race 顶层用例 | 两个并发登录耗时 | mgrd 峰值内存 |
| --- | --- | --- | --- |
| amd64 / Ubuntu 24.04 | 76，通过 | 136 / 135 ms | 152.52 MiB |
| arm64 / Ubuntu 24.04 | 76，通过 | 168 / 166 ms | 152.55 MiB |

上述是本次临时 CI 主机实测，不作为所有设备的性能承诺。两个架构均使用生产 Argon2id 参数：64 MiB、3 次迭代、并行度 1、最多两个活跃哈希工作；mgrd 的 systemd MemoryMax 为 256 MiB。Go race 的整包时限为 180 秒，生产 IPC 请求时限仍为 5 秒。

原型检查全部通过：208 项回归、3 项进程集成、26 项浏览器测试、类型检查、lint 和生产构建。公共 API 兼容扩展至 v1.1.0，114 路径 / 131 操作；58 项 Scope / 53 页映射及冻结的 v1.0.0 发布契约保留。

实际服务测试发现并已修复两处接线问题：严格 IPC 解码误拒绝嵌套操作正文，及安全 Job 序号与冻结契约的字符串类型不一致。对应回归已保留，未通过放宽契约或跳过 arm64 消除失败。

## 范围与审阅决定

Standard/Expert 继续只改变呈现深度；桌面、平板和移动端职责沿用已接受原型。本批不增加移动端配置流程。交换配置仍须经过共享 Candidate / Diff / Validation / Apply 或 Safe Apply / Evidence，尚未接入的对象与字段 authority 拒绝操作，configuration_ready 保持 false。

#20–#28 已全部归入 Phase 1 milestone，仍保留 Todo 与完整功能验收。TACACS+ 返回 AUTH_PROVIDER_UNAVAILABLE，后续按 #27 与 #35 的 SecretStore 依赖实现；Svelte 页面按 #41/#54 接入。本批的消费者专用会话加密不等同于 #35 的完整 SecretStore、证书激活与密钥轮换，也不代替 #37 共享证据或 #48 恢复流程。

用户接受本批后再合并 PR #60、关闭 #34，并推进 #35 SecretStore、HTTPS 与证书安全激活。PR #19 继续作为独立库存原型审阅。
