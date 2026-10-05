# Interface 原生类型与 patch 配对观察 v0.1

2026-10-05 · #21 / #42 E5。正式 Interface 详情补齐原生类型说明及 patch 对等关系；只依据已读取的 native type、受限 options 和同一库存快照，名称、角色、Linux/PCI 或硬件提示不互相推断。完整字段编辑和类型/attachment 变更继续独立验收。

## 原生关系与来源

OVS patch 两端要求 type=patch，且 options:peer 与对端 name 互相对应。不同 datapath 的 Bridge 不满足关联条件；空 datapath_type 按原生 system 默认解释，missing 仍为 Unknown。同 Bridge 的配对允许被观察，但配置不证明运行转发、无环或已 Applied。依据：[OVS Interface 原生类型及 Patch Options](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)。

Provider 根据实际 schema 为 Interface.type、Interface.options 与 Bridge.datapath_type 提供私有形状识别证明，拒绝把 optional/set、非 string map、有额外 enum 限制或未知列解释为本批关系；版本字符串不授予能力。既有监控、options 安全子集和完整原始 map 的 MTU 证明保持原边界，不增加列、开放 map、host 路径或命令。

mgrd 从一个不可变、已 confirmed 且 fresh 的库存 view 解析双方。必须存在唯一 peer 名称、双向配置、唯一 Port/Bridge 父关系和 active 的当前 Interface/Port/Bridge 绑定；缺失、歧义、自引用、对端类型不同、单向配置、未知/不同 datapath、未绑定或已退休身份都返回明确 reason，三个 peer 引用均为 null。只在证据成立时返回当前稳定管理身份。同名删除/重建获得新的身份；旧详情保持 NOT_FOUND，既有关系不会被旧链接静默重定向。

## 权限与契约

API 1.19.0 为 Interface 增加可选 `patch_peer`，包含 availability、reason、独立的 ovsdb-configuration 来源及三个 nullable 对端引用。旧响应继续兼容，冻结 v1.0 与请求契约不修改。`configuration.read` 在类型适用性、schema、新鲜度与 peer 核对之前检查；没有该权限时始终 Withheld，且不透露是否 patch 或对端身份。Standard/Expert 服务端权限完全相同。

陈旧/partial/provider 故障时 peer 关系为 Unknown 且引用为空。原始 native type 仍可按 Last observed 展示，但页面不提供当前 peer 导航。该观察不改变 `config_revision` 算法、不分配身份、不授予 ownership/操作权限，也不参与 Candidate、Applied、Safe Apply 或补偿的写入准入。

## 页面与边界

Standard 说明 system 默认、internal、patch、原生 tunnel、DPDK、dummy 与未识别类型；Expert 额外显示 patch reason、来源、时间、confidence/freshness。未知字符串保留原值，不变成 system。safe options 内的 remote_ip/local_ip/key=flow 仅说明其依赖 OpenFlow 动作，不产生固定远端身份。DPDK 原生配置类型不构成 NIC 关联、驱动已绑定、运行就绪或调优授权；现有 OVS reported device 与独立 Linux 表仍各用自身来源。

桌面、平板 Standard 和手机均提供只读观察与严格导航，支持键盘、深色和窄屏；不新增类型、attachment、patch、Tunnel 或 DPDK 写操作。已有 MTU 独立 root grant、账户权限、Candidate/Diff/Validation、原生 CAS、实际 MTU Applied、Safe Apply 与补偿门禁保留。

## 验收

Go 验证三份真实 schema 的识别与未知形状，关系例外/权限、system 默认、partial/stale 以及同名新身份。三份 schema 的真实 OVS/HTTPS/IPC 场景核对 reciprocal 引用、token 隐藏、单向、缺失、不同 datapath 与重建。正式浏览器新增两个流程，在实际 Go/OVS 服务验证页面与 API、Standard/Expert、键盘/响应式、深色及实际 OVSDB 停止恢复；类型配置样例不宣称实际 DPDK 硬件或 Tunnel 流量已验收。

接受以最终 PR 和同树 main 的六项完整 CI、下载报告与截图复核为准，见[审阅矩阵](../reviews/INTERFACE_NATIVE_TYPES_v0.1.md)。接受标签 `phase1-interface-native-types-v0.1` 仅在该门禁成立后记录；#21/#42 保持 In Progress，#43/#47/#54/#71 继续各自范围。
