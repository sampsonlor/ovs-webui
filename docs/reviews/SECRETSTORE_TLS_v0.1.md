# SecretStore / HTTPS 技术审阅 v0.1

日期：2026-09-14。任务 #35 / [PR #61](https://github.com/sampsonlor/ovs-webui/pull/61)。实现及技术验收完成，等待用户接受；#35 保持 Open / In Progress。前置 #34 已合并，接受标签 `phase1-auth-grants-v0.1`。

实现与操作边界见 [SecretStore / HTTPS](../implementation/SECRETSTORE_TLS_v0.1.md)。

| 要求 | 验收覆盖 |
| --- | --- |
| 分区 AEAD | 数据库/消费者/ID/用途/version/provider 绑定、nonce 随机、跨分区和缺钥拒绝、密文备份、版本轮换中断恢复 |
| HTTPS 候选 | bootstrap、自定义信任根、SAN/有效期/链/EKU/配对/强度验证，私钥加密、幂等且不进入 mgrd |
| 有界激活 | 强 ETag、当前权限和二次认证、旧/新 TLS 连接区分、原子安装、确认 Job/receipt、旧证书恢复 |
| 进程和时间故障 | mgrd/webd 分别 SIGKILL、原始 deadline 不变、mgrd 停止时真实 120 秒本地恢复、跨 boot 和时钟回退 |
| 会话与恢复 | legacy envelope 迁移、轮换不延长 expiry、校验备份后的认证 epoch/Token/会话撤销、普通重启独立处理 |
| 代理和脱敏 | 显式 peer/header allowlist、Origin/CSRF 独立、密码/私钥/Cookie/Token 不进入普通日志/回执/Audit/数据库快照 |

功能提交 `96a1eaebe4d39af84f69baef2c8fd6c20e20585a` 的 [CI](https://github.com/sampsonlor/ovs-webui/actions/runs/34834861945) 六项全部通过。[长期证据](evidence/SECRETSTORE_TLS_v0.1.json)保留 head、实际测试 merge SHA、每个顶层测试名称、服务实测、artifact ID 和 SHA256，避免依赖短期日志保留期。证据提交还增强了真实 HTTP 的不可信 CA 叶证书输入；PR 当前提交继续由 CI 独立核对。

| 原生 Linux 架构 | Go race 顶层用例 | mgrd 停止时的 TLS 自动恢复 |
| --- | --- | --- |
| amd64 / Ubuntu 24.04 | 91，通过 | 119.315 秒，恢复已确认证书 |
| arm64 / Ubuntu 24.04 | 91，通过 | 119.704 秒，恢复已确认证书 |

上述时间从试用激活请求前测量，保护期限上限为 120 秒；wall-clock 辅助截止点使用秒精度，可能提前不足一秒结束。两端独立 SIGKILL 后原 deadline 保持不变，mgrd 停止期间 webd 仍恢复旧证书；mgrd 重启后的首个读取能看到 rolled-back 和 failed Job/完成回执。

真实测试定位并修复了 mgrd 启动时等待首个维护 tick 才更新过期 trial 的竞态：现在启动接收请求前同步核对恢复，证书读取也核对 deadline。对应即时读取回归保留，未通过放宽检查或延长确认期限消除失败。另区分新候选的最短剩余有效期与既有身份的实际有效期，避免普通重启提前废止尚未到期的证书。

两架构还通过真实 systemd/Unix peer、隔离 OVS 转发和独立 2 MiB tmpfs 满盘恢复。原型侧 208 项回归、3 项进程集成、26 项浏览器测试、类型检查、lint、冻结契约兼容检查及生产构建全部通过。TLS 私钥、登录凭据和 Token 不出现在服务日志、数据库快照、manager receipt 或 Audit 中。

Standard/Expert、响应式职责和已接受原型保持原有范围；本批提供正式 Go API 与恢复基础，AD-09 管理页面及 #20–#28 各功能主单仍单独验收。无 ACME、通用 PKI 或外部 KMS 强依赖；Export/Support Bundle、备份编排和 provider 依次由后续任务实现。#35 用户接受后才合并、关闭并开始 #36。
