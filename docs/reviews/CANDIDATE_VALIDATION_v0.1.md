# Candidate / Validation 审阅 v0.1

日期：2026-09-15。任务 [#38](https://github.com/sampsonlor/ovs-webui/issues/38)。实现与验证进行中；尚未获得本批用户接受。

实现和边界见 [正式 Candidate、Diff 与 Validation](../implementation/CANDIDATE_VALIDATION_v0.1.md)。

| 验收范围 | 对应证据 |
| --- | --- |
| 私有持久草稿 | 正式 web.db、强 ETag/CAS、按用户隔离、重启、stage/remove/discard |
| Diff / Validation | 真实 schema 约束、四字段原始值、typed before/after、三方冲突和显式 rebase |
| 当前授权和失效 | owner、credential、role ceiling、policy、generation、schema、provider、expiry、Candidate revision |
| 丢应答和原子性 | workspace receipt、管理 outbox、同 key 原结果重放、Job/Validation/Audit 同事务 |
| 原生环境 | CI 的 Ubuntu amd64 / arm64；实际 OVSDB/ovs-vswitchd 加载 3.3.9、3.7.1、4.0.0 三份 schema |
| 产品边界 | 当前页面与 Standard/Expert 交互不变；正式前端 #41/#54 独立验收；无 live-save / Apply |

本地与原生 CI 的最终实测结果、提交 SHA 和长期证据将在完成后补齐。#39 仍待当前批次接受后开始。
