# Interface Linux 设备观察与硬件关联 v0.1

2026-10-05 · #21 / #42 E3。延续已接受的原生清单及 MTU 两批，在正式 Interface 详情加入独立 Linux 数据源。其余原生字段编辑、类型/attachment 变更和一般图生命周期保留独立验收。

## 关联与范围

mgrd 仅从当前已确认、fresh 的 OVS Interface 行构造请求。仅原生空 type、system、internal 且 ifindex 为已知正整数的设备可采样；名称本身、OVS status 的 bus_info、DPDK 类型和 host attachment candidate 不授予关联。名称必须是有效的单个 Linux 接口名称。内核名称及 ifindex 必须同时一致，读取前后核对同一设备 inode 和 index，返回前再次核对原 OVS UUID、管理 ID、generation、type/name/index 与来源新鲜度。

API 1.17.0 在 `readInterface` 增加可选 `linux_device`，携带独立 source、availability、reason、已匹配 ifindex 和固定十个观察字段。清单、快照分页和配置 revision 不加入 Linux 采样；全库存 coverage 仍仅覆盖 OVSDB 列，并明确 detail-only Linux 范围。使用既有 state/inventory 读取权限；configuration.read 缺失时既有配置字段继续 withheld。任何写权限、Candidate、Validation、Applied、Safe Apply 和补偿逻辑均不使用该新观察授予权限或替代既有原生证明。

## 数据与语义

读取 carrier、operstate、mtu、speed、duplex，以及真实父设备的 PCI address、driver、vendor/device ID 和 NUMA node。carrier=0 是已知 down；缺失/读取错误不伪造成 down。kernel `operstate=unknown` 是已读到的原值，区别于无法证明字段。speed=-1、NUMA=-1 与未知 enum 保持 unknown，不补默认速度、NUMA 0 或 MTU 1500。

Linux 设备关联与已知 carrier 不证明 OVS 已成功挂接、数据转发、对端健康或硬件角色；OVS error/ofport 与共享健康证据继续各自审阅。

依据：[Linux net sysfs ABI](https://github.com/torvalds/linux/blob/v6.8/Documentation/ABI/testing/sysfs-class-net)、[operstates](https://www.kernel.org/doc/html/latest/networking/operstates.html)与[sysfs 访问规则](https://www.kernel.org/doc/html/latest/admin-guide/sysfs-rules.html)。OVS link_state、OVS admin_state、OVS mtu 与 Linux carrier/operstate/mtu 各自呈现，source 与采样时间独立；这些是有前后身份核对的顺序采样，不能宣称跨 provider 原子主机快照。

从实际 /sys/devices devpath 向上查找第一个设备 subsystem，最多十六层；不依赖固定设备层级，不使用 device-link 猜父身份。该最邻近设备须确认为 PCI subsystem 且名称符合 BDF 才发布 PCI 关联。USB 网卡不会继承上游 PCI 控制器地址；虚拟设备没有证明时显示 unavailable。driver 只来自该真实父设备的 driver link，并再次核对设备/subsystem/driver。OVS status 中报告的 bus/driver 单独标明，不升级为 host 硬件证明。

## 预算与异常

生产固定 `/sys`，公开 API/IPC 不接受 host path 或 sysfs root。os.Root 对每次属性读取约束根目录及符号链接；每个属性最多 128 字节，输出最多十个字段，不公开路径、设备序列号、地址或其它 host inventory。最多两个独立采样 worker，每次设备读取等待上限 250ms；超时后卡在内核的 worker 继续占用采集槽位。采集只读并使用独立于配置和安全工作流的资源。

OVS stale、类型或 ifindex 不可证明、sysfs 不可用、设备消失、同名新 index、读取期间重新关联、超时与 busy 均返回明确状态，无旧设备值回填。字段读取失败只影响其字段；已验证的设备关联不制造缺失硬件能力。

## 交付与边界

正式 Svelte 详情的 Standard 显示可读值与 availability，Expert 增加关联来源与字段证据；桌面、平板 Standard 和手机均只读，保留表格键盘访问。没有新配置按钮。

设备本身已证明、但没有硬件关联的字段使用中性 unavailable 标记，避免把正常虚拟接口缺少 PCI 信息呈现为设备故障；关联不匹配、来源不可用等真实例外仍明确提示。

## #44 共享采样

`ovs-mgrd` 每秒启动一次有界采样轮次，每轮最多安排 24 个已确认的 Interface，两个 worker 共用 500ms 时间预算。采样按管理 ID 轮转，容量压力下继续轮到其余设备。每个 Interface 最多保留一份 RAM 样本，条目总数受库存 2048 行上限约束。详情读取共享结果，列表维持原有投影。页面刷新读取已发布的样本及其采样时间。

管理 ID、原生 UUID、实例 generation、类型、名称、ifindex 及 OVS 连接连续性共同约束缓存。发生重新绑定或 OVS 断线时淘汰旧证据。设备读取失败时发布该次失败状态；无样本或样本超过六秒时隐藏字段并显示待采样或过期原因。服务重启后从空缓存开始。

mgrd 使用一个 `NETLINK_ROUTE` 的链路订阅接收内核 `RTM_NEWLINK` 和 `RTM_DELLINK` 通知。监听器校验数据报来源和结构，合并重复 ifindex，并按固定时间窗口、数据报数和索引数限制每批。命中已确认的 Interface 时，缓存立即进入待采样状态。下一轮的 24 个名额中最多 12 个优先用于受影响的 Interface，其余名额继续按管理 ID 轮转。发布仍以重新核验的 sysfs 样本为依据。在途样本跨越通知或可检测的通知缺口时会丢弃。

通知溢出、截断或订阅中断会使 Linux 设备样本进入待采样状态，并由既有有界轮询重建。订阅带有上限的重试间隔。初次订阅不可用时，由周期采样提供当前状态。后续由 #44 继续跟踪邻居事件、硬件角色证明及共享流通知。 [rtnetlink 消息定义](https://github.com/torvalds/linux/blob/v6.8/include/uapi/linux/rtnetlink.h) · [Netlink 通知与丢失](https://man7.org/linux/man-pages/man7/netlink.7.html)

本地 Windows 检查不能运行 Linux 设备验收。双架构 native CI 使用真实 kernel sysfs loopback 和正式 Go/OVS 浏览器中的隔离 veth：验证 index、MTU、carrier up/down、消失、同名新设备拒绝、配置权限隐藏与原生 provider stale。PCI/USB/driver 的成功解析使用受限 sysfs 文件系统夹具，不能等同真实 NIC/DPDK/Offload 硬件 qualification。三份 OVS schema fixture 亦不代表三个 daemon 二进制版本。

完整证据与接受门禁见[审阅矩阵](../reviews/INTERFACE_LINUX_DEVICE_v0.1.md)。接受标签 `phase1-interface-linux-device-v0.1` 仅在 PR/main 完整 CI、报告和截图复核后记录；#21 / #42 / #54 继续独立开放，#71 未被本批宣称修复。
