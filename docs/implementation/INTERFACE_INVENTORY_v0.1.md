# Interface 清单与原生观察 v0.1

2026-10-03 · #42 E1 / #21 SW-06、SW-07。正式 Go 库存接入 Svelte 清单与详情页，作为后续 Interface 受控编辑的读取基础。本批仅提供 Observe，完整 #21 / #42 / #54 继续开放。

## 数据与原生含义

- 使用实际 OVSDB monitor 的 Interface 行、管理身份、原生 UUID 和 instance generation；Bridge → Port → Interface 的父关系必须唯一，Bond 始终是多成员 Port。父关系缺失或歧义返回明确错误，不选择其他对象代替。
- 读取 type、ofport、admin_state、link_state、mtu、mtu_request、ifindex、link_speed、duplex 和错误存在状态。保留空可选集合、未报告、未知 native type、ofport=-1 和精确整数，不能把它们归一化为零或成功。
- 实际 mtu 与 status 等运行观察使用 ovs-vswitchd-observation 来源，不改变配置 revision；mtu_request 使用 ovsdb-configuration 来源。原生空 mtu_request 表示未请求，不表示当前 MTU 已恢复为某个常数。依据：[OVS Interface MTU 文档](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)。
- status 仅发布 driver_name、driver_version、firmware_version、bus_info、numa_id、if_type；其余键隐藏。options 延续已批准的 peer、remote_ip、local_ip、dst_port、key 安全子集。空子集不证明完整原生 map 为空；错误自由文本继续不公开。
- PCI 关联只能来自 provider 报告的 bus_info，且须符合 PCI BDF 格式；名称、原生 type 或 internal 状态不能证明物理设备、PCI、DPDK 管理能力。Linux carrier、全量硬件库存仍 unavailable。

## 页面与权限

Interfaces 支持服务端全库存名称过滤、10/25/50/100 页大小及绑定 snapshot 的游标。外部变化或游标过期返回 CURSOR_EXPIRED，页面提示重新从第一页读取；空过滤结果、provider 故障和缺失对象分别显示。

Expert 在 Switching 提供清单入口，Standard 保留 Port 关联入口、相同清单和详情路径。两种模式只改变字段来源、身份和原生证据的展示深度。缺少 configuration.read 时，type、mtu_request、options、internal / local_interface 等配置含义 withheld；运行观察仍按当前读取权限展示。退出、撤权、路由和过滤变化均隔离旧响应，不允许旧成功数据恢复到当前页面。

详情展示严格父关系、原生字段及来源、实际/请求 MTU、设备观察与 ownership。所有 Interface 字段 editable=false、allowed_operations=[]。没有直接 live-save；可回到所属 Port 的现有 Candidate 工作流。后续 MTU/type/options 编辑须另行实现字段准入、原生语义、CAS、Applied、补偿和 Safe Apply 验收。

桌面提供完整观察；平板和手机继续只读。表格可通过键盘滚动，长身份与证据在容器内换行或滚动。API 1.14.0 仅增加可选响应字段和字段证据类型，不扩大输入或 Interface 写操作；v1.0 冻结基线不修改。

## 验证与后续

本地契约兼容、228 项单元、Go 包测试、类型检查、lint、构建为提交门禁。真实三份 schema（3.3.9、3.7.1、4.0.0）的库存测试同时验证父关系、字段来源、只读性及 token 配置隐藏。正式浏览器新增清单/分页/过期/深度/响应式/键盘、空结果/隐藏/实际 OVSDB 断连/同名重建两项流程；保留已有字段写入、Safe Apply、网络恢复和其他业务回归。

接受以 PR / main 双架构完整 CI 和保留报告为准，见[审阅矩阵](../reviews/INTERFACE_INVENTORY_v0.1.md)。接受标签 phase1-interface-inventory-v0.1。后续继续 #42 E2 Interface 受控编辑及 #21 剩余完整验收；本批不关闭主单。
