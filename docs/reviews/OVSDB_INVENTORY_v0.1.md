# OVSDB Discovery / Inventory 审阅 v0.1

日期：2026-09-15。任务 [#36](https://github.com/sampsonlor/ovs-webui/issues/36) / [PR #62](https://github.com/sampsonlor/ovs-webui/pull/62)。实现与技术验收完成，等待用户接受；保持 Open / In Progress，未开始 #37。

实现、运行方法与安全边界见 [OVSDB Inventory](../implementation/OVSDB_INVENTORY_v0.1.md)。

| 验收要求 | 对应证据 |
| --- | --- |
| 实际 schema，不由版本猜 capability | 三份上游固定 schema；native type、reference、index、mutable 与缺列测试 |
| Bridge → Port → Interface / Bond | 同一 monitor 快照、local internal Port、共享 ID 和强引用；整条更新后发布 |
| 原生身份与生命周期 | 持久 generation、同名重建 tombstone、多证据连续性、复制与新建的区分 |
| 有界观察与权限 | 原子快照、48 KiB 响应、cursor scope、过期/Unknown、当前 Token 字段过滤、缺 provider 不伪造默认 |
| 故障与恢复 | 真实 OVSDB 失联/重连、mgrd SIGKILL、复制恢复、精确摘要人工核对与 Audit、manager restore 撤销 |

功能提交 `7954f7c6e8d7368eba2acafcfe22c6f4f18a9e40` 的 [CI 34920821551](https://github.com/sampsonlor/ovs-webui/actions/runs/34920821551) 六项全部通过。长期证据保存在 [OVSDB_INVENTORY_v0.1.json](evidence/OVSDB_INVENTORY_v0.1.json)，包含实际测试的合并 SHA、原生架构、测试名称、schema 和 artifact 校验和、OVS binary 版本及测量值。后续文档提交的完整 CI 状态见 PR #62，不把文档提交与这份功能证据的 SHA 混用。

| 检查 | 结果 |
| --- | --- |
| Ubuntu 原生 amd64 / arm64 | 各 104 项 Go race 测试通过；vet、依赖校验、IPC fuzz 与 CGO-free build 通过 |
| 真实 OVS schema 矩阵 | 两架构分别加载 3.3.9、3.7.1、4.0.0 上游 schema，每份通过 7 组库存/权限/恢复场景 |
| 既有真实服务回归 | systemd、Unix peer、OVS forwarding、SQLite 磁盘满、认证撤销、HTTPS 激活/120 秒恢复均通过 |
| 前端与契约 | 208 项回归、3 项集成、26 项浏览器测试、typecheck、lint、契约生成/不可变基线兼容检查及生产构建通过 |

本地还完成 Linux amd64 vet、两架构 daemon 交叉构建及 pnpm build。CI 中既有 daemon 测试使用专属的未启动 OVS endpoint，防止 runner 自带 OVS 改变它们的管理库基线；真实库存由独立 inventory fixture 验收。Token 场景保留真实 step-up 门禁，先验证拒绝，再重新认证后创建观察 Token。

当前 schema 矩阵加载到 CI 实际提供的 OVS binaries；分别报告 binary 与 schema 版本，不将其表述为三个产品版本的完整资格认证。发行版/内核/升级矩阵仍为 #51。#21/#22 和 #54 仍有页面验收，#36 不能提前关闭这些功能主单。

Standard/Expert 的权限一致、桌面/平板/移动职责、键盘和高保真外观沿用既有已接受批次；本批只交付正式只读资源及恢复基础。写操作继续走后续 Candidate/Validation/Apply/Safe Apply，不开放直接 live-save。
