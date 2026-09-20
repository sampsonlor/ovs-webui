# #40 Safe Apply 审阅记录

交付入口：[PR #66](https://github.com/sampsonlor/ovs-webui/pull/66)。范围、运行配置和恢复边界见[实现说明](../implementation/SAFE_APPLY_v0.1.md)。接受条件为最终提交的六项 CI 全部通过、按已授权流程合并 main 并核验合并结果；接受基线由 annotated tag `phase1-safe-apply-v0.1` 记录。未通过的中间运行不构成验收。

## 验收矩阵

| 场景 | 要求与证据入口 |
| --- | --- |
| 正常路径 | HTTPS/CSRF → 双库冻结与 journal → 原生 Commit → Applied → 绑定接口的 TCP probe → 显式确认 → 单个 Last Known Good；`safe_apply.py` |
| 确认后的连续操作 | 生成新的空 Candidate，同步 manager witness / web workspace；原请求、决定仍幂等重放；Go auth 测试与 VM 连续阶段 |
| Applied 尚未成立 | 不出现健康成功与确认窗；Go safety 测试及 #39 原生 Applied 矩阵 |
| webd 停止、mgrd SIGKILL | 读取持久截止时间，不延长真实 120 秒确认窗；独立恢复并保留用户意图；VM 测试 |
| mgrd SIGSTOP | 实际安全循环不再发 heartbeat，systemd 在 WatchdogSec / TimeoutAbortSec 预算内重启；VM 测试 |
| 管理链路中断 | kernel OVS access VLAN 实际隔离 namespace 中的客户端，TCP probe 阻止确认；webd 关闭且 mgrd 重启后恢复 VLAN 和 TLS 连通；VM 测试 |
| 撤权、boot 变化、时钟倒退 | 确认关闭、服务端要求补偿；Go auth 测试；撤销真实发起会话另由 VM 验证 |
| 精细回滚 | 同一 OVSDB transaction 比较 current == our_after，只恢复 touched VLAN 字段，其他 Port / metadata 保留；native matrix |
| Prepare 后第三方抢写 | 两 Port 补偿整笔被原生 wait 拒绝，无部分回滚；native matrix |
| generation / 强身份变化 | 旧事务不能补偿到替换数据库；native matrix |
| commit 成功但回执未持久化就 SIGKILL | marker 证明 Commit，缺失 target 保留 Applied Unknown；只补偿一次，不重发原 Apply；native matrix |
| 回滚回复丢失 | 即使物理值恢复、marker 证明提交，缺失精确 target 仍保留 Recovery Required 与保护；native matrix |
| 第三方重叠修改 / manager.db 损坏 | 拒绝确认、保留冲突和 Last Known Good；损坏后不猜测恢复写入；VM 测试 |
| 数据库繁忙 / 长期未知状态 | 恢复专用 reader 与 writer 优先级；相同观察不重复追加状态证据；SQLite 与 auth 回归 |

原生 runner 为 Ubuntu 24.04 amd64 / arm64，各自运行 race、真实 systemd/HTTPS/IPC/双库故障测试。真实 OVS field matrix 保留每份 schema 八个既有场景，新增 Safe Apply 每份六个场景；schema 为 3.3.9、3.7.1、4.0.0。实际 OVS binary 版本由产物记录，三份 schema 不代表三个 binary 版本或完整发行版资格。独立 VM 网络测试必须加载 kernel datapath，失败不能改用 dummy 或 skip 代替。

Go native 场景的 probe / 时间可合成，以精确覆盖故障边界；VM 网络场景使用实际管理 TCP/HTTPS 和生产 120 秒窗口。两类证据分别保留，不互相替代。Windows 本地只承诺可运行的 Go 测试、Linux vet / 编译、Python 语法检查、契约回归与 pnpm build，Linux 安全与恢复结果以两个原生 CI job 为准。

## 审阅处置与边界

只开放已审阅 local VLAN allowlist 中的 `port.vlan.set` Safe Apply。未配置 root 管理端点/接口、checkpoint 不可写或 rollback 不可用时拒绝入场；普通 Apply 继续关闭。确认不等同于所有客户端或全部转发业务健康。未知提交、丢失 Applied target、generation 断层和重叠第三方变更均保留人工处理边界，没有 force 或整库覆盖入口。

本批为正式后端验收。Standard/Expert 使用相同权限与裁决；原型页面、键盘与响应式职责沿用已接受基线，不增加移动端高风险事务。完整真实页面链路、刷新恢复及模式/设备浏览器矩阵由 #41/#54 交付，#20/#21/#22 不因本批关闭。

初轮真实 VM 验证发现确认后遗留的 Candidate Consumed 标记阻挡下一次编辑；修复为原子生成新 Candidate，并增加 Go 与 VM 连续操作覆盖。最终检查可从 PR Checks 追溯；CI 产物包括每架构 `go-safe-apply-<schema>.json`、`go-safe-apply-vm.json`、VM journal 与既有运行时/认证/库存报告。
