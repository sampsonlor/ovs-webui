# OpenFlow、DPDK 与 Offload 原生观察

OpenFlow 按 Bridge 管理 ID 和 OVS 实例代次采集。在 root 审阅并配置 Bridge 到本地管理 socket 的对应关系后，Manager 使用已验证的运行中 OVS 3.3.9 构建、Bridge 只读见证、当前 OVSDB 拓扑与 datapath ID 确认采集来源。其间来源切换、socket 更换、进程换代、身份不明或见证失效会使查询不可用。常规 OVS 包和超出当前见证能力的 Bridge 可继续使用现有清单和配置查看。

查询使用 OpenFlow 1.3 的只读握手及 multipart 查询，必填 table 0–254，可选精确 cookie，也可请求 Group Description 和 Meter Configuration。一次查询最多 2 秒、12 个 multipart 回包、256 KiB 读入及 128 行，总 JSON 上限 48 KiB。单次只允许一个采集，达到上限显示截断。数值保留为精确十进制计数和十六进制 cookie。默认保留 match、instruction 和 action 的原生编号、类型和长度。有 `configuration.read` 的用户可以显式保留完整的 match 与 instruction 负载，由快照保存十六进制原始字节。单条 instruction 负载最多保存 256 字节，超出时保留描述并标记截断。Group/Meter 统计未知，Group/Meter 配置按自身观测结果展示。OVSDB 交换配置和 OpenFlow 规则归属独立，规则归属保持 unknown。

快照在 Manager 内存中保存 30 秒，并绑定原始查询、Bridge、实例代次、观测时间、当前帐号和授权版本。分页及导出读取同一份快照，授权、来源或实例变化时重新检查。浏览器只提供 Observe。

DPDK/Offload 分别展示 OVSDB 配置、OVS 报告的运行值与 Linux 主机观察。配置值受 configuration.read 控制。Linux 全局 HugeTLB 与 NUMA 数据只说明主机内存状态。PMD/RXQ 运行、进程 NUMA 亲和性、硬件 offload 和软件回退缺少正向证据时按字段返回 unknown。硬件验收需要对应 DPDK 和 NIC 环境。

原生 CI 使用已验明构建，在独立网络命名空间验证本地 Bridge、按 table/cookie 查询、计数解析、Group 配置和上限。Meter 证据取决于运行的 datapath 能力，并由报告明确区分。
