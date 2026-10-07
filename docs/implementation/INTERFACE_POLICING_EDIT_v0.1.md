# Interface ingress policing 受控编辑 v0.1

2026-10-07 · #21 / #42 E8 交付候选；基于已接受的 E7 独立 Linux ingress 观察。完整任务与 Phase 1 发布门禁继续独立验收。

## 范围与授权

只接收一个 `interface.policing.set` intent：`policing.mode` 为 `disabled`、`bandwidth` 或 `packets`。禁用 rate=0；带宽 1–1000000 kbit/s（十进制）；包速率 1–1000 kpps。启用一种速率时另一种写零。四个原生值都必须已知，两个 burst 必须为零；本批只更新两个 rate，保留原生 burst=0，让 OVS 选择默认值。自定义 burst、组合速率及超出本批范围的现有配置继续只读。

账户须有 `ovs.interface.policing.write`，root 须独立指定 `--local-policing-interfaces=<Interface management IDs>`。该参数明确声明这些身份的 Linux ingress 由本服务独占管理；不是通过已安装规则推断安装者，也不能从 Port/MTU 权限继承。独占策略未建立前不得启用这个参数。外部控制标记优先阻止写入。

仅支持 fresh/confirmed、原生 ifindex/name、已知有效 ofport/空 error、唯一 Bridge→Port→Interface 归属、非 Bridge 本地的 standalone internal Interface、已有 system Bridge、完整 raw options 空证明。根 `other_config` 必须已知且 hw-offload 不启用。物理/system Interface、Bond 成员、DPDK、offload、patch/tunnel 和未知类型保持各自只读边界。

## Candidate 与执行证据

Candidate 封存原始四项参数、Interface/Port/Bridge 身份、name/ifindex、schema 与依赖。编辑草稿保留原始值；不能混入其他操作，也不能 Rebase 接受变动的原始配置。Diff 显示原始/当前/拟修改值、原生单位和保留的默认 burst。验证通过只表示配置与授权门禁通过；`POLICING_KERNEL_CHECK_AT_DISPATCH` 明确表示独立内核检查仍将在发送前执行。

Prepare 读取受限 Linux ingress 状态，并把私有语义指纹写入 durable prepared execution。禁用态必须无 ingress qdisc；启用态只接纳一个软件 ingress matchall priority=1/handle=1 的 police drop/continue，且 byte 或 packet rate 精确匹配原始配置。tc 协议的结构性头可出现；额外 selector、action、chain、clsact、shared block、硬件安装或未知属性均阻止写入。指纹包含原生 burst 参数而排除统计、时间、引用计数等瞬态数据，不将内核 tick 伪装成用户 burst 单位。

发送前重读原生绑定/配置/策略及内核指纹。唯一 OVSDB mutation 原子守卫全部四项参数、ifindex/type/options、精确父关系、根 offload 策略和控制标记，再更新两个 rate、commit marker 与 next_cfg。返回值必须给出本次精确 target；超时或响应丢失不能猜测 target 或重放写入。

Applied 除 cur_cfg 达到精确 target、原生 after-image 和无错误外，还要求两次合格内核读之间的一次只读 OVSDB 原子证明。两次内核指纹及安装速率必须一致。OVSDB 与 Linux 仍是独立顺序观察，不宣称跨 provider 的原子主机快照、真实吞吐/丢包效果或规则安装者归属。

Safe Apply 确认重新检查这些证据与当前账户权限。补偿只从已封存原始值导出，守卫 after-image、marker、身份及合格内核状态，恢复原始两个 rate 并再次证明安装结果。若 ovs-vswitchd 在 commit 后尚未安装规则，允许匹配原始私有内核指纹后精确恢复；不要求先应用待回滚的危险配置。外部/部分规则、身份变化、证据缺失或补偿响应丢失保留冲突/恢复状态，不覆盖或虚构成功。

## 页面与共享证据

Svelte Interface detail 提供独立 policing 编辑入口。仅桌面可暂存；Standard/Expert 权限与限制相同，平板/手机查看 Diff 和处理已有 Safe Apply。专用页面可刷新进入，显示互斥速率模式、单位、默认 burst 与管理路径风险。所有写入经共享 Candidate/Diff/Validation/Safe Apply，Interface 事件/审计关联沿用服务端导出的不可变目标身份。

测试使用独立真实 system Bridge，避免改变既有 MTU 默认依赖。PR 和 main 各自执行完整六项 CI、三 schema 双架构 native matrix 与真实 Go/OVS 浏览器验收；截图存在不等于视觉验收。

## 原生依据

[OVS Interface policing schema](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html) 定义速率与默认 burst。受控范围还参考 [OVS v3.3.9 netdev-linux](https://github.com/openvswitch/ovs/blob/v3.3.9/lib/netdev-linux.c) 的 ingress 重建及 matchall 实现。[Linux v6.8 police](https://github.com/torvalds/linux/blob/v6.8/net/sched/act_police.c)、[matchall](https://github.com/torvalds/linux/blob/v6.8/net/sched/cls_matchall.c) 和 [action dump](https://github.com/torvalds/linux/blob/v6.8/net/sched/act_api.c) 分别约束组合速率、选择器及硬件计数。对不满足已验证格式的系统保持只读。
