# 现有 Port Bond / LACP 子批次审阅

2026-09-24 · #42 子批次 A。范围见[实现说明](../implementation/PORT_BOND_LACP_v0.1.md)。接受要求为最终 PR head 与合并 main 的全部六个 CI job 通过；最终交付记录与 annotated tag `phase1-port-bond-lacp-v0.1` 标识接受提交，中间运行不能代替接受证据。

## 必须保留的证据

| 范围 | 证明与预期 |
| --- | --- |
| 正常 Bond | 当前成员保持身份，Candidate → 三方 Diff → Validation → 原生 balance-tcp/active/fallback → Applied → Safe Apply 显式确认；一次应用派发 |
| 单 Interface Port | LACP 是 Port 属性；补偿精确恢复原始 lacp/bond_mode 空集合与 fallback 缺失 |
| 约束和未知 | TCP/LACP/fallback、SLB/flood_vlans、STP/RSTP、原始非法组合、future native 值、外控/未知所有权、local/internal/不支持的成员和 schema 有明确阻断 |
| 身份与依赖 | 使用当前成员管理 ID；删除成员、替换 UUID 或重复父 Port 无法沿用旧计划；重叠字段要求显式 rebase |
| 原子性 | 预检后外部改动 Bond，混合 VLAN/Bond 原生事务整体拒绝 |
| 丢失回执 | 原 marker 只读恢复 Commit；缺失 Applied target 仍为 unknown，不重放写入 |
| 权限 | 独立 allowlist/字段能力；VLAN 权限不能执行 Bond；旧 credential ceiling 不自动扩大；Bond 撤权触发补偿 |
| 回滚冲突 | 外部改动 after-image 后保留原作者值与 rollback-conflict，不覆盖、不解除保护 |
| 精细补偿 | 只恢复三字段，保留无关 VLAN/other_config/身份；保护记录正确标为 port.bond |
| API / IPC | 闭合 intent 拒绝夹带字段，观察响应符合 schema，签名原值经严格 IPC 编解码可验证，篡改或重复 key 被拒绝 |
| 页面与设备 | 既有共享审阅页，Standard/Expert 相同权限，桌面配置、平板已有事务、移动端事故处理；专用编辑器仍属 #54 |
| 旧功能与恢复 | 保留 VLAN/Safe Apply 三 schema、kernel 管理连接恢复、每架构七项正式浏览器验收及原型浏览器/契约/集成回归 |

`TestNativeBondLACP` 在真实 ovsdb-server/ovs-vswitchd 私有实例执行七个子场景。CI 在 amd64、arm64 分别使用 3.3.9 / 3.7.1 / 4.0.0 上游 schema，每架构保留 21 项 Bond/LACP 子场景报告：`go-runtime-{arch}/go-bond-lacp-{schema}.json`。这些场景使用 dummy Interface 和隔离 probe，证明原生事务与补偿，不冒充物理 LACP 对端协商。已有 #40 kernel OVS/veth/namespace 测试继续验证真实管理连接断开及恢复；多 schema 不等于多 OVS binary/硬件发行版认证。

## 审阅结论与交付门控

本批实现达到上述子范围；Windows 本地验证类型、契约、构建与跨平台 Go 测试，Linux 运行时、SQLite、OVS 与 race 结论只依据最终双架构 CI。新 HTTP 用例贯通实际 handler/双库，身份测试验证当前 capability/旧 ceiling，原生测试另验证派发与恢复。

回归发现既有合成 lab 的两次读取可能跨过服务器提交：旧 workspace 与新 Candidate 混合时，浏览器进入错误状态并停止轮询。现在仅对这类不一致进行最多三次完整只读重取；认证/网络错误仍直接关闭操作，持续变化也在预算耗尽后关闭。HTTP 回归通过真实 store worker 在两个 GET 之间提交来稳定重现，验证一次 Apply、恢复确认窗口，以及持续变化不开放新操作；未增加命令重试或放宽浏览器断言。

最终接受需核对 PR 与 main 两个架构的全部 job 和新增三 schema 报告。#42 保持开放，后续继续对象生命周期子批次；#21/#22/#29/#54 不因本批交付提前关闭。
