# SecretStore / HTTPS 技术审阅 v0.1

日期：2026-09-14。任务 #35。实现已进入验证；本批尚未接受，#35 保持 Open / In Progress。前置 #34 已合并，接受标签 `phase1-auth-grants-v0.1`。

实现与操作边界见 [SecretStore / HTTPS](../implementation/SECRETSTORE_TLS_v0.1.md)。

| 要求 | 验收覆盖 |
| --- | --- |
| 分区 AEAD | 数据库/消费者/ID/用途/version/provider 绑定、nonce 随机、跨分区和缺钥拒绝、密文备份、版本轮换中断恢复 |
| HTTPS 候选 | bootstrap、自定义信任根、SAN/有效期/链/EKU/配对/强度验证，私钥加密、幂等且不进入 mgrd |
| 有界激活 | 强 ETag、当前权限和二次认证、旧/新 TLS 连接区分、原子安装、确认 Job/receipt、旧证书恢复 |
| 进程和时间故障 | mgrd/webd 分别 SIGKILL、原始 deadline 不变、mgrd 停止时真实 120 秒本地恢复、跨 boot 和时钟回退 |
| 会话与恢复 | legacy envelope 迁移、轮换不延长 expiry、校验备份后的认证 epoch/Token/会话撤销、普通重启独立处理 |
| 代理和脱敏 | 显式 peer/header allowlist、Origin/CSRF 独立、密码/私钥/Cookie/Token 不进入普通日志/回执/Audit/数据库快照 |

本地类型检查、lint、208 项回归、生产构建、Go 可移植测试及 Linux amd64/arm64 交叉编译已通过。原生双架构 race、真实 systemd/HTTPS 和完整 CI 尚待当前 PR 实测，结果完成后在本记录和长期证据中补齐。

Standard/Expert、响应式职责和已接受原型保持原有范围；本批提供正式 Go API 与恢复基础，AD-09 管理页面及 #20–#28 各功能主单仍单独验收。无 ACME、通用 PKI 或外部 KMS 强依赖；Export/Support Bundle、备份编排和 provider 依次由后续任务实现。#35 用户接受后才合并、关闭并开始 #36。
