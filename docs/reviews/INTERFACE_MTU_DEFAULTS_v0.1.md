# Interface 自动 MTU 审阅矩阵 v0.1

2026-10-04 · #21 / #42 E2b · 接受目标 `phase1-interface-mtu-defaults-v0.1`。

| 门控              | 必须保留的证据                                                                                                                                                                             |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 两个正常方向      | 从 native empty 设置明确请求，再回滚到同一身份的 empty 与原设备 MTU；清空已有请求，实际自动值被证明后确认，或回滚到原数字                                                                  |
| 依赖和并发        | 原设备 MTU 漂移阻断旧验证执行，late 原设备或 peer MTU 修改使原子 dispatch 失败；peer 漂移阻断确认/补偿；同名 peer 重建不能继承旧恢复；无贡献者和 root 撤销拒绝执行                         |
| Applied / Unknown | 实际 ovs-vswitchd 暂停时不制造自动设备证明或确认窗，但仍可补偿恢复原请求，恢复守护进程后验证原设备值；两个方向丢失提交回复都不重放/不制造 target；丢失自动补偿回复保持 recovery-required   |
| 原生和输入边界    | native empty 与未知/0/缺失区分；未知或不稳定图、raw options 非空/未知、外部控制、其他类型拒绝；公开 clear 不接受客户端默认/before/outcome/原生写入                                         |
| 草稿和旧 journal  | 保留 sealed empty/数字及依赖，不能通过编辑重捕获默认；旧明确请求 JSON 仍能精确反向恢复；服务派生补偿标记跨 journal 序列化保留且公开 intent 不接受                                          |
| Standard / Expert | 请求与自动依赖两行 Diff 相同，单位/风险/恢复语义在两种模式均可读；Expert 不增加权限                                                                                                        |
| 响应式            | 桌面编辑/验证；900px 平板与 390px 手机审阅禁用新验证且无整页横溢出；既有 Safe Apply 处理保持共享行为                                                                                       |
| 异常和权限        | 原生空请求但设备实际值与推导值不一致时不可编辑，保留真实 kernel 例外；依赖漂移需重新 stage，无贡献者默认不可编辑并保留深色证据；E2a 的 Reader、手机直接编辑和隐藏原生 options 场景继续通过 |
| 回归              | E1/ E2a 全部截图、分页/provider/退休身份；既有三 schema 原生矩阵、管理连接恢复 VM、认证/TLS、正式与原型浏览器持续通过                                                                      |

新矩阵 `go-interface-mtu-default-{3.3.9,3.7.1,4.0.0}.json`：每架构、每 schema 14 项不跳过原生场景。E2a 的 11 项 × 三 schema 原生矩阵继续独立执行。schema fixture 不等于三种实际 OVS 二进制版本；原生字段矩阵使用隔离 dummy provider 的 system 映射，新增原设备漂移场景在暂停发布守护进程后注入 OVSDB 观察故障。正式浏览器在实际 system kernel Bridge 上检查 OVS 报告和 Linux `ip link` MTU，独立覆盖真实设备与推导值不一致的例外。

完整验收目标：每架构 209 个唯一 Go race 顶层测试、18 项正式浏览器流程、10 次额外 Safe Apply 准入重复和全部旧原生矩阵；共享 233 项单元、3 项隔离集成、31 项原型浏览器。数字仅为核验目标，须从 PR 与 main 实际报告分别核对，失败或跳过不算完成。

新增截图：interface-mtu-default-unapplied、interface-mtu-default-editor、interface-mtu-default-standard、interface-mtu-default-expert、interface-mtu-default-tablet-review、interface-mtu-default-mobile-review、interface-mtu-default-awaiting-confirmation、interface-mtu-default-rolled-back、interface-mtu-clear-standard、interface-mtu-clear-rolled-back、interface-mtu-clear-confirmed、interface-mtu-default-drift、interface-mtu-default-unavailable-dark。保留双架构 frontend-evidence，并查看两种模式、窄屏与异常图像。

## Review disposition

required tests 和 PR 完整 CI 及原生/浏览器报告通过后合并 tested head；核对 main 同树、完整 CI 和证据后创建接受标签并删除本地/远程特性分支。#21 / #42 保持 Open / In Progress，其他 Interface 字段、原生类型和一般图生命周期尚未接受。#71 的 arm64 一次性登录问题继续独立跟踪。
