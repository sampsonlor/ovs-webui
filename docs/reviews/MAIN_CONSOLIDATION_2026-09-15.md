# main 归集审阅记录

日期：2026-09-15。用户已接受 #38，并授权基于当前测试结果将已完成的分支工作统一归集至 main。本轮通过 [PR #19](https://github.com/sampsonlor/ovs-webui/pull/19) 合并共享库存原型与正式基础；不开始下一项 #39。

## 范围与分支审计

归集前 main 为 `3bbf167dd873f18929c1fc4aba933d7edf74d09d`，即已接受的 #38 / PR #64。其 [main CI](https://github.com/sampsonlor/ovs-webui/actions/runs/34946179095) 六项全部通过。正式工程 #30–#38 均已接受，#29 仍为 In Progress；#20–#28 与 #39 之后的独立功能任务保持原有验收边界。

盘点得到 26 个远端功能分支、19 个本地功能分支，以及 main。除 PR #19 外，所有分支都满足以下一种证明：提交可从 main 到达；或对应 PR 已合并，其 squash merge 在 main 历史内，且该 merge 的完整源树与分支 tip 完全相同。没有将“分支很旧”作为删除依据。

PR #19 的原始提交为 `57ead1b88a3f589c66a3d6cd25f33bbc3c7b3727`。将当前 main 合入此分支时，只有 `docs/README.md` 与 `docs/STATUS.md` 的进度记录冲突；代码自动合并。冲突处理保留 #30–#38 正式基础和共享原型库存，并补录 #38 的已接受状态。共享库存的范围、异常状态及响应式责任见[原审阅记录](SHARED_INVENTORY_v0.1.md)，原型页面的剩余缺口见 [IA 盘点 v0.2](P1_IA_COVERAGE_v0.2.md)。

## 检查与合并门禁

- Windows 本地：锁文件安装、TypeScript、全仓 lint、生成契约、冻结 v1.0.0 与当前 main 的公共 API 兼容检查、215 项回归、3 项隔离 HTTP/恢复集成以及 `pnpm build` 通过。
- 本地完整浏览器尝试遇到隔离 Vite 服务启动的 60 秒超时；失败发生在页面测试开始之前，保留日志与 trace，不将其报告为浏览器通过。
- 合并以 PR #19 当前提交的[完整 Checks](https://github.com/sampsonlor/ovs-webui/pull/19/checks)为准：Quality/build、隔离集成、31 项 Chromium 浏览器回归、Go runtime amd64、Go runtime arm64 和 CI Gate 必须全部成功。旧提交的四项 CI 不替代本轮六项门禁。
- 原生两架构继续执行 126 项 Go race 测试，以及真实 OVSDB / ovs-vswitchd、三份 schema、身份授权、TLS 恢复、持久证据和 Candidate/Validation 场景；本轮不变更这些后端实现及 CI 配置。

通过后仅合并被检查的准确 PR head，并核对 main 源树一致。接受基线使用注释标签 `main-consolidated-2026-09-15`；正式前端接线、OVS 执行与 Safe Apply 仍按各工程任务交付。

## 历史分支清理与恢复

清理仅在 PR 合并、分支证明重新核对及备份验证后执行：

1. 归集前后分别创建完整 Git bundle，并保存分支名称、准确 SHA、PR 对应关系、worktree 清单及备份 SHA-256，位置为本机忽略目录 `outputs/branch-consolidation/`。
2. 将主工作目录切换到最新 main；三个根目录原始 DOCX 逐一核对 SHA-256，保持原样。
3. 九个辅助 worktree 在各自既有 HEAD 上转为 detached 快照，保留目录、源码和忽略的工具/证据文件。
4. 删除经审计的远端功能分支时使用原子 push 和逐分支预期 SHA；本地引用也按预期 SHA 删除。任何并发变更或未合并内容都会中止清理。已有接受标签保留。
5. 清理后的准确结果写入 `cleanup-result.json`；仓库以 main 作为继续开发的统一起点。后续任务创建短期功能分支，验收合并后及时清理。

`branches-before.bundle` 保留原始分支，`branches-final.bundle` 另含本轮整合提交。可用 `git bundle list-heads <bundle>` 查看可恢复引用，再将所需引用 fetch 到新的恢复分支；清理不依赖 GitHub 的短期 reflog 或 CI artifact 留存。
