# QinQ / 客户 VLAN 审阅与验收 v0.1

2026-10-03 · #42 D 批。实现边界见 [实现说明](../implementation/QINQ_VLAN_v0.1.md)。最终接受以 PR / main 完整 CI、保留原生报告、浏览器截图和 annotated tag phase1-qinq-vlan-v0.1 为准；Windows 本地检查不替代 Linux 原生验收。

| 项目 | 验证要求与证据 |
| --- | --- |
| 正常路径 | access → dot1q-tunnel，独立 tag/cvlans，Candidate 无 live-write；Safe Apply 确认保持原身份 |
| 原生报文含义 | 三份实际 schema，各架构使用 ovs-vswitchd dummy normal switching trace 验证服务 VLAN 封装、两种 TPID 和客户 VLAN 拒绝 |
| 原始/反向含义 | 空 cvlans、QinQ → access → QinQ 补偿、原生缺省 mode 精确恢复、保留无关 map |
| 外部变更 | 在派发窗口修改 cvlans 或 TPID，混合事务原子拒绝；补偿前重叠修改保留 rollback-conflict |
| OutcomeUnknown | 原始响应和补偿响应丢失，不重放、不伪造 target/Applied |
| 对象/授权边界 | 单成员/唯一父/active identity/root、未知 TPID、schema 不支持、原生保留 VLAN、不安全原始组合、只读权限及移动端门禁 |
| 页面 | 真 Go/IPC/OVS 的编辑器输入错误、Standard/Expert、平板/手机审阅、Safe Apply/回滚与原身份；截图 qinq-*.png |
| 兼容/回归 | 旧请求兼容检查、已有标准 VLAN/Bond/对象生命周期、完整领域与浏览器、双架构 Go race、Safe Apply VM 管理连接故障 |

新增独立 TestNativeQinQ：每份 schema 14 个叶子场景，每架构 42 个。保留 go-qinq-{3.3.9,3.7.1,4.0.0}.json、frontend.xml、frontend-evidence/qinq-*.png，CI 失败或跳过不能当成接受。已有工作流的原生测试和 30 分钟 job 上限保持不变。

## Review disposition

完成实现与本地检查后提交 PR；双架构原生测试与真实浏览器通过后才合并 exact tested head。随后复验 main，保留标签和 Issue 验收记录，清理特性分支。完整 #42 / #22 / #54 继续开放；本批不能证明对端、硬件 offload 或任意 OpenFlow pipeline 的生产效果。
