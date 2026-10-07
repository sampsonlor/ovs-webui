# Interface Linux ingress policing 观察 v0.1

2026-10-07 · #21 / #42 E7。E4 已保留四项原生 policing 配置；本批增加独立的实际 Linux 规则观察，作为后续受控写入的证据基础。完整 policing 修改、QoS/Queue 管理和其他 Interface 字段继续单独验收。

## 数据与权限

API 1.21.0 为 Interface 增加可选、仅详情读取的 `linux_ingress_policing`。它通过 `NETLINK_ROUTE` 读取 Linux tc ingress qdisc 和 filter，authority 为 `linux-netlink-observation`，与 OVS 配置、ovs-vswitchd 状态、sysfs 设备观察分开。清单不执行逐行内核扫描。配置读取权限不足时返回 withheld、空 actions、无 ifindex/采样时间；Standard/Expert 权限完全一致。

只有已确认、未过期库存中的 system（含原生空 type）/internal、有效 native name 与正 ifindex 可以观察。内核读取前后核对对应 ifindex 的真实设备名；返回前再核对管理 ID、UUID、generation、type/name/ifindex、库存状态与年龄。过期、替换、上游断连或取消均丢弃规则值。观察不改变 config_revision，不授予 ownership 或 allowed_operations，不用于 OCC/Applied 或恢复判断。

## 内核边界与预算

只发送 GETLINK、GETQDISC、GETTFILTER 读取请求；不执行 tc/shell 或变更 host。既有 systemd AF_NETLINK 允许列表保持，无新增特权。独立最多 2 个同时读取，每次总期限 250 ms；非阻塞 socket 每次最多等待 10 ms，整个读取最多 256 KiB / 512 条 Netlink 消息、64 个 filter、16 个 police action。每个 action table 最多 32 槽。关闭 socket 释放读取，不创建无法取消的后台 worker。

支持 ingress 与 clsact ingress hook 的 basic/matchall/u32 police action 及旧 direct police 编码。64-bit rate 覆盖 32-bit rate；bytes/s、packets/s 以精确 uint64 十进制字符串传输，null 为未报告，不替换成 0。police exceed/conform 动作分别呈现。OVS 原生 kbit/s、kpps 与配置 burst 仍在原配置表，不做虚假等值对照。

未知 classifier/action、未来 police 参数、peak/average 限制或 shared ingress block 为 partial，不声明没有 policing。消息长度、嵌套属性、重复值、seq、kernel sender、截断、dump interruption、DONE/error、总量或耗时不成立时为 unavailable，清除全部 action；读取失败不能沿用上次值。Burst 为 kernel ticks，尚未证明换算，因此未展示“有效 burst”。

覆盖只到所读的 Linux tc ingress 规则；未证明匹配的全部流量、egress、XDP、userspace/DPDK、硬件执行或安装者。即使已观察 police action，也不能据此声称流量效果、规则归 OVS 所有或配置已 Applied。dump 和 OVS 库存不是原子快照。

依据：[OVS Interface 手册](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)、[OVS 3.3.9 Linux provider](https://github.com/openvswitch/ovs/blob/v3.3.9/lib/netdev-linux.c)、[Linux v6.8 classifier UAPI](https://github.com/torvalds/linux/blob/v6.8/include/uapi/linux/pkt_cls.h)、[调度 UAPI](https://github.com/torvalds/linux/blob/v6.8/include/uapi/linux/pkt_sched.h)、[rtnetlink UAPI](https://github.com/torvalds/linux/blob/v6.8/include/uapi/linux/rtnetlink.h)。

## 页面与验收

正式 Interface 详情独立呈现已安装规则、来源时间、完整/部分/不可用/隐藏，以及明确 kernel 单位。Expert 增加 filter kind/priority/handle、police index 和来源；两模式均不新增写入口。配置请求与实际安装规则不一致时分别保留。键盘刷新、表格滚动、深色、平板 Standard 和手机只读保持。

原生三 schema fixture 都实际创建隔离 system Bridge/veth，OVS 安装 byte+packet policing，检查 API、内核 iproute2 独立读、配置权限、partial action、无逐行扫描、配置 revision 与移除后的空观察。浏览器另验证暂停 ovs-vswitchd 后配置变化但 kernel 保持旧 rate、错误 ifindex 丢弃值、真实断连、两模式隐藏和不支持类型。parser 和库存测试覆盖 wire 异常、精确 uint64、取消/并发预算、身份/代际变化及无效样本。测试 tc/ip/OVS 写入只用于 disposable fixture，不进入产品。

接受以最终 PR/main 同树六项 CI、全部报告与视觉复核为准，见[审阅矩阵](../reviews/INTERFACE_POLICING_OBSERVE_v0.1.md)。#21/#42 保持完整范围开放，#43/#47/#54 和 #71 不因本批观察完成而关闭。
