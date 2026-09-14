# SecretStore / HTTPS 实现 v0.1

任务 #35，前置 #34 已接受并合并为 `da24f62215940b113d1316bc5e60f55964b6f2c4`，接受标签 `phase1-auth-grants-v0.1`。本批实现 Go 安全基础；AD-09 完整页面仍由 #28 / #54 单独验收。

## 消费者和存储

| 消费者 | 数据库和密文 | 独立密钥目录及权限 |
| --- | --- | --- |
| webd TLS | web.db `secret_records` 的 `webd-tls`；私钥与证书组合加密，当前身份及有界 trial 也加密 | web.db 同级 `tls-keys/`，webd UID，目录 0700 / 文件 0600 |
| webd session | web.db `browser_sessions`；Cookie SHA256 查找，grant/CSRF/原到期时间密文 | 同级 `session-keys/`；独立随机密钥；可读取 #34 的 `session.key` 及旧 envelope |
| privileged consumers | manager.db `secret_records` 的 `mgrd-privileged` | manager.db 同级 `secret-keys/`，root 0700 / 0600；webd 无读取权 |

AES-256-GCM、每条随机 96-bit nonce；AAD 包含数据库身份、格式版本、provider、consumer partition、secret ID、purpose 和 key_version。修改任一绑定或缺少相应版本密钥均拒绝解密。每分区最多 4096 条、单个明文最多 192 KiB。私有消费者显式解密，没有网络读取原始 secret 的接口。密码继续 Argon2id，API Token/Cookie/grant 查找继续只存 SHA256。

master key 不进入数据库备份。`secret.Value` 默认 JSON/fmt/slog 表示均脱敏；两守护进程使用共同日志边界，仅允许固定事件及操作属性。证书 GET 仅包含指纹、SAN 对应主机、有效期、状态、revision/sequence 和 `private_key_configured`；私钥不进入 mgrd IPC、Job、receipt 或 Audit。`redact.Document` 提供普通诊断/导出共用边界；尚未实现的 Export/Support Bundle/Debug 服务仍拒绝访问，后续 #47/#48 必须沿用此边界。

## 初始化和运行

先按 #32/#34 创建数据库、session.key、auth.key 和本地管理员。以下命令分别由指定 UID 运行，不覆盖已有材料；不提供默认密码。

```sh
# root，mgrd 停止时
ovs-mgrd --database /var/lib/ovs-webui/manager/manager.db --init-secret-store

# webd UID，webd 停止时；Origin 必须是实际访问地址
ovs-webd --database /var/lib/ovs-webui/web/web.db --public-origin https://switch.example --bootstrap-tls
ovs-webd --database /var/lib/ovs-webui/web/web.db --public-origin https://switch.example --print-tls-certificate
```

bootstrap 生成 ECDSA P-256 自签名、90 天有效的 HTTPS 身份，SAN 来自 canonical Origin。管理员通过可信本地通道取得公钥证书/指纹并建立初始信任；不会关闭客户端证书验证。导入正式证书默认使用系统信任根，也可显式设置 `--tls-trust-file` 为 webd 所有的 0600 PEM 根证书集合。私有 CA 信任文件随进程启动加载。

正常启动自动读取已有 `tls-keys/` 和加密身份。明确配置的旧 `--tls-cert` / `--tls-key` 静态模式继续支持 #31/#34 迁移；静态模式不提供动态证书管理。存在 managed 目录但密钥丢失/损坏时不退回静态配置或自动生成新身份。daemon `--help` 列出可覆盖的 key directory 参数。systemd 的 TLS_CERT/TLS_KEY 在 managed 模式可为空；私有 CA 使用 ExecStart override 添加 trust-file。

## 证书候选、激活与恢复

1. 已登录且二次认证的 session 提交 `POST /api/v1/certificates`。webd 验证证书链、服务端 EKU、SAN 主机名、有效期、私钥配对以及最低密钥强度；只接受 RSA 2048–8192、ECDSA P-256/P-384/P-521 或 Ed25519。证书/私钥输入各最多 64 KiB、链最多 8 张；至少覆盖完整恢复窗口。
2. 候选私钥仅在 webd TLS 分区加密。mgrd 接收封闭的候选元数据并重新授权实际 operation，保存 validated Certificate、完成的 Job、幂等回执和 Audit。相同 principal/epoch/request ID 的重复输入恢复原候选；改变输入拒绝。最多 128 个候选，当前版本由运维规划轮换容量，完整删除/页面管理不在本批开放。
3. `POST /api/v1/certificates/{id}/activations` 需要强 If-Match、当前权限和二次认证。mgrd 先持久化 120 秒 trial 和运行中 Job；webd 校验候选与旧身份，将本地恢复状态落盘后原子切换 TLS 指针。安装失败时保留原身份，Job 继续等待并最终失败恢复。试用窗从持久入场开始，安装延迟不会延长保护期限。
4. 新增兼容接口 `POST /api/v1/certificates/{id}/confirmations`：当前 session、CSRF、Origin、二次认证、If-Match 均有效，而且当前 HTTP 请求所属 TLS 握手实际使用该候选，才可确认。webd 从服务端连接上下文记录身份，禁止用请求头自报；TLS session tickets 关闭，激活响应要求关闭旧连接。旧 keep-alive 连接的确认返回 `TLS_FRESH_CONNECTION_REQUIRED`。
5. 确认完成原激活 Job/receipt，更新证书及 Audit；未确认则恢复上一已确认身份（第一次激活时为 bootstrap）。mgrd 与 webd 独立使用 boot ID + CLOCK_BOOTTIME 截止点 + wall-clock 防倒退检查。相同 boot 的进程重启不延长期限，跨 boot 或时钟异常立即结束 trial。mgrd 不可用时 webd 的握手回调仍按原期限选择旧证书；恢复 mgrd 后先补齐持久失败证据再接收请求，证书读取也先核对 deadline。没有存储/授权依据时不伪造成功。既有身份直到实际到期才失效，但安装候选要求旧身份仍覆盖此次恢复截止点；应提前完成证书轮换。

Public v1.2.0 为 115 路径 / 132 操作；冻结的 v1.0.0 保留，旧 v1.1.0 输入/响应兼容。私有 IPC minor=2，增加 peer-checked `tls.execute` 和 `tls.state`；精确 digest/软件版本握手继续生效。该恢复保护用于 HTTPS 身份，不代替后续 OVS 配置的 Applied/Health/Safe Apply 状态机。

## 轮换、恢复和代理

停止对应 daemon 后执行 `ovs-webd --rotate-secret-key tls`、`ovs-webd --rotate-secret-key session` 或 `ovs-mgrd --rotate-secret-key`，同时提供原 database、Origin、trust-file 等相关参数。数据库独占锁拒绝在线运行此命令。

轮换先落盘下一版本 `key-NN`，再事务重加密，最后 fsync/原子替换 `active`。保留所有旧版本和已准备版本；进程在任一步中断均能读取已提交密文，重复操作复用 prepared key，不覆盖它。支持 1–16 版本，达到上限拒绝操作；后续 key retirement 需独立恢复/备份保留策略。session 从 legacy 文件显式迁移后保留旧材料，不改变任何 cookie、grant 或失效时间。TLS 候选幂等指纹保留其原 digest_version。`auth.key` 的认证用途沿用 #34，独立于 privileged reusable-secret 分区。

恢复必须先停止两个 daemon、校验并安装匹配的双库备份、恢复各自独立密钥，再分别运行 `--prepare-restore-security`。mgrd 原子旋转 auth/request epoch、撤销恢复的 grant/token、清空 elevation、终结 TLS trial 并保留 Audit；webd 删除 session 映射和未确认 handoff、旋转 workspace request epoch、丢弃本地 trial。之后才启动 mgrd/webd 并重新认证。不能把普通重启当成恢复，也不能通过恢复旧 Cookie 复活权限。#35 提供并测试这些安全 reconciliation hooks，完整备份编排、安装和 UI 属于 #48。

反向代理仍须通过 TLS 连接 webd。只有 `--trusted-proxy-cidrs` 命中的直接 peer，且在 `--trusted-proxy-headers` 明确列出的 `X-Forwarded-Proto` / `X-Forwarded-Host` 才可影响 authority 检查。仅接受单值 https 和 canonical Host；不接受代理链、Origin、认证身份或任意 header 转发授权。所有代理断言进入 public handler 前剥离；Origin/CSRF/session 检查独立保留。HTTPS 上 canonical Host 不匹配返回 421。未配置信任时忽略转发断言。

实现依据：[Go AEAD](https://pkg.go.dev/crypto/cipher#NewGCM)、[X.509 Verify](https://pkg.go.dev/crypto/x509#Certificate.Verify)、[TLS GetCertificate](https://pkg.go.dev/crypto/tls#Config.GetCertificate)。验收入口见 [技术审阅](../reviews/SECRETSTORE_TLS_v0.1.md)。
