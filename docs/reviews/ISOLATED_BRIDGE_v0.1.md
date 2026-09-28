# #42 B 批：隔离 Bridge 创建审阅 v0.1

日期：2026-09-28。范围和限制：[实现说明](../implementation/ISOLATED_BRIDGE_v0.1.md)。本记录描述验收要求；最终通过情况以测试提交对应的 PR / main CI 和接受标签 `phase1-isolated-bridge-v0.1` 为准，未绿灯前不视为接受。

| 责任 | 证据 |
| --- | --- |
| 正常创建 | 单个原生事务生成 Bridge/local Port/internal Interface；UUID 与 mgrd 分配一致；Applied / probe / 显式确认；无 live-save |
| 精确取消 | root detach + GC，三行消失且旧身份 tombstone；同名新对象获得新身份，旧计划不能重放 |
| 竞争与外部依赖 | 在发送前注入同名抢占、成员增添、未监控 Mirror weak ref、Interface policing；事务整体拒绝并保留外部配置 |
| OutcomeUnknown | 创建/回滚丢失回复；新执行器读取持久 journal；没有重复派发，没有伪造 target 或 Applied |
| 权限 | 当前创建权限、credential ceiling、root name allowlist、外控 root；已入场撤权补偿；不把 VLAN 权限当创建权限 |
| 原生内核 | 每 schema 都创建真实 kernel local Interface，ofport=65534；回滚后数据库与 Linux 接口均消失 |
| 浏览器 | 正式服务 stage → 共享 Diff/Validation → Safe Apply → rollback；Standard/Expert 对比；平板/手机禁用新提交；状态和截图 |
| 回归 | 既有 VLAN/Bond、管理网络丢失/进程重启恢复、认证/HTTPS、存储、原型浏览器与全部构建 |

新增 native 矩阵每 schema 10 个叶子场景，报告 `go-isolated-bridge-{schema}.json` 同双架构 runtime artifacts 保留。正式浏览器报告 `frontend.xml` 与 `frontend-evidence/bridge-create-*.png`；它不包含密码、cookie、CSRF 或 network trace。

本批没有宣称一般 Bridge 删除、Port/Interface 独立生命周期、成员调整或完整 UI 完成。现有对象不会因名称进入 root allowlist 而被收编。原始三份用户根目录 DOCX 保留；部署配置及站点访问未改动。后续 #42 C 批继续已有对象删除的身份变化/补偿边界与独立 Port 生命周期。
