# OVSDB Discovery / Inventory 审阅 v0.1

日期：2026-09-15。任务 [#36](https://github.com/sampsonlor/ovs-webui/issues/36) / [PR #62](https://github.com/sampsonlor/ovs-webui/pull/62)。实现已提交，原生 CI 验证中；保持 Open / In Progress，未开始后续任务。

实现、运行方法与安全边界见 [OVSDB Inventory](../implementation/OVSDB_INVENTORY_v0.1.md)。

| 验收要求 | 对应证据 |
| --- | --- |
| 实际 schema，不由版本猜 capability | 三份上游固定 schema；native type、reference、index、mutable 与缺列测试 |
| Bridge → Port → Interface / Bond | 同一 monitor 快照、local internal Port、共享 ID 和强引用；整条更新后发布 |
| 原生身份与生命周期 | 持久 generation、同名重建 tombstone、多证据连续性、复制与新建的区分 |
| 有界观察与权限 | 原子快照、48 KiB 响应、cursor scope、过期/Unknown、当前 Token 字段过滤、缺 provider 不伪造默认 |
| 故障与恢复 | 真实 OVSDB 失联/重连、mgrd SIGKILL、复制恢复、精确摘要人工核对与 Audit、manager restore 撤销 |

本地 typecheck、lint、208 项回归、契约生成/兼容检查、生产构建通过。Linux amd64 vet 与两架构二进制交叉构建完成；正式原生 race、systemd/OVS/HTTPS 与浏览器检查以本 PR 的 CI 为准。

当前 schema 矩阵加载到 CI 实际提供的 OVS binaries；分别报告 binary 与 schema 版本，不将其表述为三个产品版本的完整资格认证。发行版/内核/升级矩阵仍为 #51。#21/#22 和 #54 仍有页面验收，#36 不能提前关闭这些功能主单。

Standard/Expert 的权限一致、桌面/平板/移动职责、键盘和高保真外观沿用既有已接受批次；本批只交付正式只读资源及恢复基础。写操作继续走后续 Candidate/Validation/Apply/Safe Apply，不开放直接 live-save。
