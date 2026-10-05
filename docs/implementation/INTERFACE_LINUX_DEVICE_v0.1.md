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

生产固定 `/sys`，公开 API/IPC 不接受 host path 或 sysfs root。os.Root 对每次属性读取约束根目录及符号链接；每个属性最多 128 字节，输出最多十个字段，不公开路径、设备序列号、地址或其它 host inventory。最多两个独立采样 worker，响应上限 250ms；读取超时后 worker 继续占用其 slot，后续 busy 请求不增加 worker，不占共享配置/安全队列。没有采样缓存或主机写入。

OVS stale、类型或 ifindex 不可证明、sysfs 不可用、设备消失、同名新 index、读取期间重新关联、超时与 busy 均返回明确状态，无旧设备值回填。字段读取失败只影响其字段；已验证的设备关联不制造缺失硬件能力。

## 交付与边界

正式 Svelte 详情的 Standard 显示可读值与 availability，Expert 增加关联来源与字段证据；桌面、平板 Standard 和手机均只读，保留表格键盘访问。没有新配置按钮。

本地 Windows 检查不能运行 Linux 设备验收。双架构 native CI 使用真实 kernel sysfs loopback 和正式 Go/OVS 浏览器中的隔离 veth：验证 index、MTU、carrier up/down、消失、同名新设备拒绝、配置权限隐藏与原生 provider stale。PCI/USB/driver 的成功解析使用受限 sysfs 文件系统夹具，不能等同真实 NIC/DPDK/Offload 硬件 qualification。三份 OVS schema fixture 亦不代表三个 daemon 二进制版本。

完整证据与接受门禁见[审阅矩阵](../reviews/INTERFACE_LINUX_DEVICE_v0.1.md)。接受标签 `phase1-interface-linux-device-v0.1` 仅在 PR/main 完整 CI、报告和截图复核后记录；#21 / #42 / #54 继续独立开放，#71 未被本批宣称修复。
