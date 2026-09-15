# Phase 1 范围与页面交付映射

日期：2026-09-09。状态：Proposed for Review，关联 #30。这里追踪正式服务责任与验收，不用页面存在或 lab CI 代替产品交付。

## 来源与状态

Architecture v1.0.1 与 IA v1.0 为批准基线。新收到的 [Scope v1.0](../baselines/OVS_WebUI_Phase1_Scope_v1.0.docx)仍标 Draft for Review，现原样归档；“原文未收到”的缺口已解决，“Scope 已获批准”未被推断。

main 基线 `176bca1` 包含已接受 P0/P1 六批及 PR #18 整合；原型覆盖采用[盘点 v0.1](../reviews/P1_IA_COVERAGE_v0.1.md)。PR #19 的共享库存/只读 Interface 页是另一个待审增量，不计作本稿已接受基线。机器可读交叉映射见 [JSON](phase1-traceability.v0.1.json)。


| 来源 | SHA-256 |
| --- | --- |
| scope | d9fe23b0ee348c8680e8993eac12d0a79d769b5c816a066dcb81c6aecd87df81 |
| architecture | 27638d3bca2bb5f0b657c0fab462c254a19b6136df9c79f148de369de44d6c42 |
| ia | d84b81275874c0fa15b5cae10b0cc87e908cd98b0e0108ee92727bf6f0e28900 |


## 验收 Gate

G1 = Domain/unit/lint/static/fuzz；G2 = 公共 API/兼容与客户端契约；G3 = 真实 OVS/provider integration；G4 = amd64/原生 arm64 和发行版 userspace；G5 = VM/systemd/kernel/reboot/升级与恢复；G6 = security/dependency/redaction。这些对应 Scope §14 的六组 release Gate。UX 是本映射额外使用的页面验收标记，包含模式、设备职责、键盘/焦点、深浅色/缩放和异常反馈，不是新造的 Scope 编号。

所有正式条目当前都待实现/待证据。某硬件/provider 缺失时，依照批准能力级别验收 Unsupported/Unavailable/Unknown 的表现；不能据此免除核心软件行为，也不能让 DPDK/Offload 硬件成为 Core GA 前置条件。

## 58 项明确 Scope ID


### 管理面基础


| Scope ID | 原文能力和级别 | 页面 | 责任议题 | 验收 |
| --- | --- | --- | --- | --- |
| P1-PLAT-01 | Two-process runtime · Required | AD-11 | [#31](https://github.com/sampsonlor/ovs-webui/issues/31) | G1 G4 G5 G6 |
| P1-PLAT-02 | Field/Domain Authority · Required | SW-01 / VI-01 / AD-01 | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#47](https://github.com/sampsonlor/ovs-webui/issues/47) | G1 G3 |
| P1-PLAT-03 | OVS Semantic Operation Layer · Required | SW-02 / SW-05 / CH-02 | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#39](https://github.com/sampsonlor/ovs-webui/issues/39)、[#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#43](https://github.com/sampsonlor/ovs-webui/issues/43) | G1 G3 G5 |
| P1-PLAT-04 | Schema/Capability Discovery · Required | AD-01 | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45) | G1 G3 G4 |
| P1-PLAT-05 | Dual SQLite · Required | CH-01 / AD-06 / AD-11 | [#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38) | G1 G5 G6 |
| P1-PLAT-06 | SecretStore · Required | AD-07 / AD-09 | [#35](https://github.com/sampsonlor/ovs-webui/issues/35) | G1 G5 G6 |
| P1-PLAT-07 | Candidate Workspace · Required | CH-01 | [#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#41](https://github.com/sampsonlor/ovs-webui/issues/41) | G1 G2 G5 G6 |
| P1-PLAT-08 | OCC / Safe Apply / Rollback · Required | CH-02 / CH-03 / CH-04 | [#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#39](https://github.com/sampsonlor/ovs-webui/issues/39)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40) | G1 G3 G5 |
| P1-PLAT-09 | Commit vs Applied · Required | CH-03 | [#39](https://github.com/sampsonlor/ovs-webui/issues/39)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40) | G1 G3 G5 |
| P1-PLAT-10 | OutcomeUnknown recovery · Required | CH-03 / OP-05 | [#39](https://github.com/sampsonlor/ovs-webui/issues/39)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37) | G1 G3 G5 |
| P1-PLAT-11 | Instance Generation · Required | CH-04 / AD-04 | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#48](https://github.com/sampsonlor/ovs-webui/issues/48) | G1 G3 G5 |
| P1-PLAT-12 | Bounded resource/backpressure · Required | AD-10 / AD-11 | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#46](https://github.com/sampsonlor/ovs-webui/issues/46) | G1 G4 G5 |

### 交换功能


| Scope ID | 原文能力和级别 | 页面 | 责任议题 | 验收 |
| --- | --- | --- | --- | --- |
| P1-SW-01 | Bridge / Port / Interface · Manage | SW-01 / SW-02 / SW-03 / SW-04 / SW-05 / SW-06 / SW-07 | [#21](https://github.com/sampsonlor/ovs-webui/issues/21)、[#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#42](https://github.com/sampsonlor/ovs-webui/issues/42) | G1 G3 G5 |
| P1-SW-02 | VLAN · Manage | SW-05 / SW-08 | [#22](https://github.com/sampsonlor/ovs-webui/issues/22)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#39](https://github.com/sampsonlor/ovs-webui/issues/39)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#42](https://github.com/sampsonlor/ovs-webui/issues/42) | G1 G3 G5 |
| P1-SW-03 | Bond / LACP · Manage | SW-09 | [#42](https://github.com/sampsonlor/ovs-webui/issues/42) | G1 G3 G5 |
| P1-SW-04 | STP / RSTP · Basic Manage | SW-10 | [#23](https://github.com/sampsonlor/ovs-webui/issues/23)、[#43](https://github.com/sampsonlor/ovs-webui/issues/43) | G1 G3 G5 |
| P1-SW-05 | IGMP / MLD Snooping · Basic Manage | SW-11 | [#43](https://github.com/sampsonlor/ovs-webui/issues/43) | G1 G3 G5 |
| P1-SW-06 | Port Profile / Drift · Manage | CH-01 / CH-04 / SW-05 | [#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#43](https://github.com/sampsonlor/ovs-webui/issues/43) | G1 G3 G5 |
| P1-SW-07 | Mirror / SPAN · Manage | SW-13 | [#43](https://github.com/sampsonlor/ovs-webui/issues/43) | G1 G3 G5 |
| P1-SW-08 | QoS / Queue · Basic Manage | SW-14 | [#43](https://github.com/sampsonlor/ovs-webui/issues/43) | G1 G3 G5 |
| P1-SW-09 | Tunnel · Manage | SW-12 | [#43](https://github.com/sampsonlor/ovs-webui/issues/43) | G1 G3 G5 |
| P1-SW-10 | Protected Port / Isolation · Manage if native | SW-15 | [#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45) | G1 G3 G5 |
| P1-SW-11 | OpenFlow · Observe + limited Expert Manage | SW-16 / SW-17 / SW-18 / SW-19 | [#47](https://github.com/sampsonlor/ovs-webui/issues/47)、[#53](https://github.com/sampsonlor/ovs-webui/issues/53) | G1 G3 G5 G6 |
| P1-SW-12 | DPDK · Observe | VI-06 | [#47](https://github.com/sampsonlor/ovs-webui/issues/47) | G2 G3 G4 |
| P1-SW-13 | Hardware Offload / SmartNIC · Observe | VI-07 | [#47](https://github.com/sampsonlor/ovs-webui/issues/47) | G2 G3 G4 |
| P1-SW-14 | sFlow / NetFlow / IPFIX · Manage | VI-08 | [#26](https://github.com/sampsonlor/ovs-webui/issues/26)、[#43](https://github.com/sampsonlor/ovs-webui/issues/43) | G1 G3 G5 |
| P1-SW-15 | Endpoint / FDB / ARP/ND / LLDP · Observe | OV-02 / VI-01 / VI-02 / VI-03 / VI-04 | [#24](https://github.com/sampsonlor/ovs-webui/issues/24)、[#25](https://github.com/sampsonlor/ovs-webui/issues/25)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#52](https://github.com/sampsonlor/ovs-webui/issues/52) | G2 G3 G6 |
| P1-SW-16 | Management Interface · Explicit Manage | AD-02 | [#49](https://github.com/sampsonlor/ovs-webui/issues/49) | G1 G3 G5 G6 |

### 运行与诊断


| Scope ID | 原文能力和级别 | 页面 | 责任议题 | 验收 |
| --- | --- | --- | --- | --- |
| P1-OPS-01 | Unified State Cache · Required | VI-05 / OP-01 | [#44](https://github.com/sampsonlor/ovs-webui/issues/44) | G1 G3 G5 |
| P1-OPS-02 | Event Timeline · Required | OP-02 | [#37](https://github.com/sampsonlor/ovs-webui/issues/37) | G1 G2 G5 |
| P1-OPS-03 | Health Engine · Required | OP-01 | [#45](https://github.com/sampsonlor/ovs-webui/issues/45) | G1 G3 G5 |
| P1-OPS-04 | Diagnostics · Required | OP-03 | [#46](https://github.com/sampsonlor/ovs-webui/issues/46) | G1 G3 G6 |
| P1-OPS-05 | Packet Capture · Required | OP-03 / OP-05 | [#46](https://github.com/sampsonlor/ovs-webui/issues/46) | G1 G3 G6 |
| P1-OPS-06 | Support Bundle · Required | OP-07 | [#46](https://github.com/sampsonlor/ovs-webui/issues/46) | G1 G3 G6 |
| P1-OPS-07 | Self-Observability · Required | AD-11 | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45) | G1 G4 G5 G6 |
| P1-OPS-08 | Pagination / streaming · Required | OV-02 / OP-02 / OP-04 / OP-06 / VI-02 / SW-17 | [#33](https://github.com/sampsonlor/ovs-webui/issues/33)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#47](https://github.com/sampsonlor/ovs-webui/issues/47)、[#52](https://github.com/sampsonlor/ovs-webui/issues/52) | G1 G2 G3 G6 |

### 安全与 AAA


| Scope ID | 原文能力和级别 | 页面 | 责任议题 | 验收 |
| --- | --- | --- | --- | --- |
| P1-SEC-01 | Local authentication · Required | AD-06 | [#20](https://github.com/sampsonlor/ovs-webui/issues/20)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34) | G1 G5 G6 |
| P1-SEC-02 | TACACS+ · Required | AD-07 | [#27](https://github.com/sampsonlor/ovs-webui/issues/27)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34)、[#35](https://github.com/sampsonlor/ovs-webui/issues/35) | G1 G3 G5 G6 |
| P1-SEC-03 | RBAC / Capability · Required | AD-06 / AD-01 | [#20](https://github.com/sampsonlor/ovs-webui/issues/20)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34) | G1 G2 G6 |
| P1-SEC-04 | Auth Grant / Session · Required | AD-06 / CH-03 | [#34](https://github.com/sampsonlor/ovs-webui/issues/34)、[#35](https://github.com/sampsonlor/ovs-webui/issues/35) | G1 G5 G6 |
| P1-SEC-05 | API Token · Required | AD-08 | [#28](https://github.com/sampsonlor/ovs-webui/issues/28)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34) | G1 G2 G6 |
| P1-SEC-06 | HTTPS · Required | AD-09 | [#35](https://github.com/sampsonlor/ovs-webui/issues/35) | G1 G4 G5 G6 |
| P1-SEC-07 | Secret handling · Required | AD-07 / AD-09 / OP-06 / OP-07 | [#35](https://github.com/sampsonlor/ovs-webui/issues/35)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#46](https://github.com/sampsonlor/ovs-webui/issues/46)、[#48](https://github.com/sampsonlor/ovs-webui/issues/48) | G1 G6 |
| P1-SEC-08 | Audit Core · Required | OP-06 | [#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34) | G1 G2 G3 G6 |

### 公共接口


| Scope ID | 原文能力和级别 | 页面 | 责任议题 | 验收 |
| --- | --- | --- | --- | --- |
| P1-API-01 | REST API v1 · Required | CH-01 / CH-03 / AD-08 | [#33](https://github.com/sampsonlor/ovs-webui/issues/33)、[#41](https://github.com/sampsonlor/ovs-webui/issues/41)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 UX |
| P1-API-02 | OpenAPI · Required | AD-12 | [#33](https://github.com/sampsonlor/ovs-webui/issues/33)、[#51](https://github.com/sampsonlor/ovs-webui/issues/51) | G2 |
| P1-API-03 | WebSocket · Required | VI-05 / OP-02 / OP-05 / CH-03 | [#33](https://github.com/sampsonlor/ovs-webui/issues/33)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G2 G6 UX |
| P1-API-04 | Error Model · Required | OP-05 / CH-03 | [#33](https://github.com/sampsonlor/ovs-webui/issues/33)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G2 UX |
| P1-API-05 | Idempotency · Required | CH-01 / CH-03 | [#33](https://github.com/sampsonlor/ovs-webui/issues/33)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#39](https://github.com/sampsonlor/ovs-webui/issues/39) | G1 G2 G3 G5 |
| P1-API-06 | Async Job · Required | OP-04 / OP-05 | [#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#33](https://github.com/sampsonlor/ovs-webui/issues/33) | G1 G2 G5 |
| P1-API-07 | External platform boundary · Required | AD-01 / AD-12 | [#33](https://github.com/sampsonlor/ovs-webui/issues/33)、[#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44) | G2 G3 |

### 生命周期与恢复


| Scope ID | 原文能力和级别 | 页面 | 责任议题 | 验收 |
| --- | --- | --- | --- | --- |
| P1-LIFE-01 | OVS Lifecycle · Manage | AD-03 | [#49](https://github.com/sampsonlor/ovs-webui/issues/49) | G1 G3 G4 G5 |
| P1-LIFE-02 | Lifecycle provider boundary · Required | AD-03 | [#49](https://github.com/sampsonlor/ovs-webui/issues/49) | G1 G4 G5 |
| P1-LIFE-03 | Checkpoint · Required | CH-05 | [#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#48](https://github.com/sampsonlor/ovs-webui/issues/48) | G1 G3 G5 |
| P1-LIFE-04 | Config Export/Import · Required | AD-05 | [#48](https://github.com/sampsonlor/ovs-webui/issues/48) | G1 G3 G5 G6 |
| P1-LIFE-05 | Management Backup/Restore · Required | AD-04 | [#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#35](https://github.com/sampsonlor/ovs-webui/issues/35)、[#48](https://github.com/sampsonlor/ovs-webui/issues/48) | G1 G4 G5 G6 |
| P1-LIFE-06 | Reconciliation after restore · Required | AD-04 / CH-04 | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#48](https://github.com/sampsonlor/ovs-webui/issues/48) | G1 G3 G5 |
| P1-LIFE-07 | WebUI service recovery · Required | AD-11 | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#50](https://github.com/sampsonlor/ovs-webui/issues/50) | G1 G4 G5 |


## 53 个批准 Page ID

每页的正式 Svelte 实现与完整 UX 验收共同归 #54；#41 只交付首条 Ports/VLAN 切片。已有 #20–#28 保留功能主单职责。表中的原型覆盖是有限证据，不表示该页完整验收。相应 source 的具体职责、mode、risk 也保存在 JSON 中。


### Overview


| Page ID | 批准页面和 route | main 原型 | 正式服务责任 | 议题 | 验收 |
| --- | --- | --- | --- | --- | --- |
| OV-01 | Dashboard `/` | 独立 | read model / shared health and evidence | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 UX |
| OV-02 | Search Results `/search` | 未实现 | search service / authorized cross-resource query | [#52](https://github.com/sampsonlor/ovs-webui/issues/52)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G6 UX |

### Switching


| Page ID | 批准页面和 route | main 原型 | 正式服务责任 | 议题 | 验收 |
| --- | --- | --- | --- | --- | --- |
| SW-01 | Switching Overview `/switching` | 独立 | inventory / native relations and state | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 UX |
| SW-02 | Bridges `/switching/bridges` | 独立 | native Bridge semantic operations | [#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 G6 UX |
| SW-03 | Bridge Detail `/switching/bridges/{id}` | 独立 | Bridge relations and native feature domains | [#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-04 | Ports `/switching/ports` | 独立 | Managed Port inventory and bounded bulk operations | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-05 | Port Detail `/switching/ports/{id}` | 独立 | Port config, state, events and audit | [#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G5 UX |
| SW-06 | Interfaces `/switching/interfaces` | 局部 | native Interface inventory and permissions | [#21](https://github.com/sampsonlor/ovs-webui/issues/21)、[#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G6 UX |
| SW-07 | Interface Detail `/switching/interfaces/{id}` | 局部 | Interface identity, fields and semantic operations | [#21](https://github.com/sampsonlor/ovs-webui/issues/21)、[#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-08 | VLANs `/switching/vlans` | 局部 | native VLAN membership and safe transaction | [#22](https://github.com/sampsonlor/ovs-webui/issues/22)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#39](https://github.com/sampsonlor/ovs-webui/issues/39)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-09 | Bonds & LACP `/switching/bonds` | 独立 | Port Bond/LACP operations and native validators | [#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-10 | STP / RSTP `/switching/spanning-tree` | 局部 | native STP/RSTP policy and runtime | [#23](https://github.com/sampsonlor/ovs-webui/issues/23)、[#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-11 | Multicast `/switching/multicast` | 未实现 | multicast native config and group/port observations | [#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-12 | Tunnels `/switching/tunnels` | 未实现 | static Tunnel Port operations; dynamic options Observe | [#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-13 | Mirrors `/switching/mirrors` | 未实现 | MirrorSession config and reference validation | [#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-14 | QoS & Queues `/switching/qos` | 未实现 | QoS/Queue native fields and relationships | [#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-15 | Isolation `/switching/isolation` | 局部 | native isolation capability and guarded mutation | [#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| SW-16 | OpenFlow Overview `/switching/openflow` | 局部 | OpenFlow pipeline observation provider | [#47](https://github.com/sampsonlor/ovs-webui/issues/47)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G6 UX |
| SW-17 | Flows `/switching/openflow/flows` | 局部 | Observe; separate conditional local flow operation | [#47](https://github.com/sampsonlor/ovs-webui/issues/47)、[#53](https://github.com/sampsonlor/ovs-webui/issues/53)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 G6 UX |
| SW-18 | Groups & Meters `/switching/openflow/groups-meters` | 未实现 | groups/meters Observe only | [#47](https://github.com/sampsonlor/ovs-webui/issues/47)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G6 UX |
| SW-19 | Controllers `/switching/openflow/controllers` | 局部 | controller endpoint/role/ownership Observe | [#47](https://github.com/sampsonlor/ovs-webui/issues/47)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G6 UX |

### Visibility


| Page ID | 批准页面和 route | main 原型 | 正式服务责任 | 议题 | 验收 |
| --- | --- | --- | --- | --- | --- |
| VI-01 | Endpoints Overview `/visibility/endpoints` | 未实现 | endpoint evidence aggregation and one-hop links | [#24](https://github.com/sampsonlor/ovs-webui/issues/24)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#52](https://github.com/sampsonlor/ovs-webui/issues/52)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G6 UX |
| VI-02 | FDB `/visibility/fdb` | 未实现 | bounded FDB queries and export | [#24](https://github.com/sampsonlor/ovs-webui/issues/24)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G6 UX |
| VI-03 | Neighbors `/visibility/neighbors` | 未实现 | rtnetlink neighbor source and identity | [#25](https://github.com/sampsonlor/ovs-webui/issues/25)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 UX |
| VI-04 | LLDP `/visibility/lldp` | 未实现 | optional LLDP source with explicit absence | [#25](https://github.com/sampsonlor/ovs-webui/issues/25)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 UX |
| VI-05 | Statistics `/visibility/statistics` | 局部 | shared counters, reset and short RAM trends | [#26](https://github.com/sampsonlor/ovs-webui/issues/26)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 UX |
| VI-06 | DPDK `/visibility/dpdk` | 局部 | DPDK Observe provider; no enable/tuning | [#47](https://github.com/sampsonlor/ovs-webui/issues/47)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G4 UX |
| VI-07 | Hardware Offload `/visibility/offload` | 局部 | hardware/offload Observe; no switchdev/SR-IOV mutation | [#47](https://github.com/sampsonlor/ovs-webui/issues/47)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G4 UX |
| VI-08 | Telemetry `/visibility/telemetry` | 未实现 | sFlow/NetFlow/IPFIX native exporter operations | [#26](https://github.com/sampsonlor/ovs-webui/issues/26)、[#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |

### Operations


| Page ID | 批准页面和 route | main 原型 | 正式服务责任 | 议题 | 验收 |
| --- | --- | --- | --- | --- | --- |
| OP-01 | Health `/operations/health` | 独立 | deterministic Health and management component state | [#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G5 UX |
| OP-02 | Events `/operations/events` | 局部 | persistent coalesced Event resource | [#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G6 UX |
| OP-03 | Diagnostics `/operations/diagnostics` | 独立 | typed read/active/disruptive diagnostics and capture Job | [#46](https://github.com/sampsonlor/ovs-webui/issues/46)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 G6 UX |
| OP-04 | Jobs `/operations/jobs` | 局部 | persistent Job list, ownership and cancellation policy | [#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G5 G6 UX |
| OP-05 | Job Detail `/operations/jobs/{id}` | 局部 | authoritative Job detail and linked resources | [#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G5 G6 UX |
| OP-06 | Audit `/operations/audit` | 局部 | manager-authoritative immutable Audit query/export | [#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G2 G3 G6 UX |
| OP-07 | Support Bundle `/operations/support-bundle` | 未实现 | bounded redacted OVS Support Bundle Job | [#46](https://github.com/sampsonlor/ovs-webui/issues/46)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G6 UX |

### Change Control


| Page ID | 批准页面和 route | main 原型 | 正式服务责任 | 议题 | 验收 |
| --- | --- | --- | --- | --- | --- |
| CH-01 | Candidate Workspace `/changes/workspace` | 独立 | webd persisted per-user Candidate and handoff | [#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G2 G5 G6 UX |
| CH-02 | Diff & Validation `/changes/workspace/diff` | 独立 | mgrd validation and immutable semantic Diff | [#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G2 G3 G6 UX |
| CH-03 | Apply / Safe Apply `/changes/apply/{id}` | 独立 | mgrd journal, Applied, health and protected rollback | [#39](https://github.com/sampsonlor/ovs-webui/issues/39)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 G6 UX |
| CH-04 | Drift & Conflicts `/changes/drift` | 局部 | candidate conflict and Profile Drift services | [#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 UX |
| CH-05 | Checkpoints `/changes/checkpoints` | 未实现 | single rolling LKG and explicit restore-to-Candidate | [#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#48](https://github.com/sampsonlor/ovs-webui/issues/48)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 G6 UX |

### Administration


| Page ID | 批准页面和 route | main 原型 | 正式服务责任 | 议题 | 验收 |
| --- | --- | --- | --- | --- | --- |
| AD-01 | Capabilities `/admin/capabilities` | 独立 | field capability registry and evidence/maturity gates | [#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G2 G3 G6 UX |
| AD-02 | Management Network `/admin/management-network` | 未实现 | typed management-path migration with reachability | [#49](https://github.com/sampsonlor/ovs-webui/issues/49)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 G6 UX |
| AD-03 | OVS Lifecycle `/admin/ovs-lifecycle` | 局部 | OVS-only package/service lifecycle provider | [#49](https://github.com/sampsonlor/ovs-webui/issues/49)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G4 G5 G6 UX |
| AD-04 | Backup & Restore `/admin/backup-restore` | 未实现 | dual-DB management backup and generation reconciliation | [#48](https://github.com/sampsonlor/ovs-webui/issues/48)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G4 G5 G6 UX |
| AD-05 | Configuration Export / Import `/admin/configuration` | 未实现 | deterministic OVS export, Merge and Exact Restore | [#48](https://github.com/sampsonlor/ovs-webui/issues/48)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 G6 UX |
| AD-06 | Users & Roles `/admin/access/users` | 未实现 | mgrd principal/role/capability authority | [#20](https://github.com/sampsonlor/ovs-webui/issues/20)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G2 G6 UX |
| AD-07 | AAA `/admin/access/aaa` | 未实现 | Local/TACACS+ chain, test/confirm and SecretStore | [#27](https://github.com/sampsonlor/ovs-webui/issues/27)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34)、[#35](https://github.com/sampsonlor/ovs-webui/issues/35)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G3 G5 G6 UX |
| AD-08 | API Tokens `/admin/access/api-tokens` | 未实现 | scoped token create/revoke/expiry and one-time secret | [#28](https://github.com/sampsonlor/ovs-webui/issues/28)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G2 G6 UX |
| AD-09 | TLS & Certificates `/admin/access/tls` | 未实现 | TLS candidate validate/activate and key partition | [#35](https://github.com/sampsonlor/ovs-webui/issues/35)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G4 G5 G6 UX |
| AD-10 | System Settings `/admin/settings` | 未实现 | web preferences vs mgrd retention/risk/resource policy | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G2 G6 UX |
| AD-11 | Management Plane `/admin/management-plane` | 局部 | self-health, audited expiring Debug and own-service actions | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) | G1 G4 G5 G6 UX |
| AD-12 | About & Support `/admin/about` | 未实现 | build/platform/schema/license and versioned help metadata | [#50](https://github.com/sampsonlor/ovs-webui/issues/50)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54)、[#55](https://github.com/sampsonlor/ovs-webui/issues/55) | G2 G4 UX |


## Scope 第 10 节的共同 UI 要求

下列 UX 编号仅为本稿核对用，不声称是 Scope 原文 ID。


| 本稿索引 | 要求 | 责任 |
| --- | --- | --- |
| UX-01 | Standard/Expert 仅改变信息深度，权限/风险/成熟度相同 | [#34](https://github.com/sampsonlor/ovs-webui/issues/34)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) |
| UX-02 | Physical/Virtual/Logical/Tunnel 分类，自然排序，Bridge/state/Profile/Label filter | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#43](https://github.com/sampsonlor/ovs-webui/issues/43)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) |
| UX-03 | 全局搜索、一跳关联、pin/favorite 和持久用户偏好 | [#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#52](https://github.com/sampsonlor/ovs-webui/issues/52)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) |
| UX-04 | 前面板仅 OEM manifest 或显式用户布局，不猜机箱物理口 | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) |
| UX-05 | English canonical + Simplified Chinese，日期/时区/变量/复数处理 | [#54](https://github.com/sampsonlor/ovs-webui/issues/54) |
| UX-06 | desktop 完整，tablet 按当前审阅职责，mobile incident companion | [#41](https://github.com/sampsonlor/ovs-webui/issues/41)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) |
| UX-07 | Unknown/Unsupported/Unavailable/Read-only/Truncated/Degraded 有不同呈现 | [#33](https://github.com/sampsonlor/ovs-webui/issues/33)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) |


## 文档差异与明确处置

以下处置供本轮审阅；未把草案变成新的批准事实。


| 编号 | 差异 | 本稿处置与验收 |
| --- | --- | --- |
| D-01 | Scope 正文为 Draft；IA 引用 Scope v1.0 为 delivery basis | 保留原状态与 SHA-256；设计/Scope 接受记录纳入 #30，未标 Approved。 |
| D-02 | IA VI-06/VI-07 有 conditional enable；Architecture §11 与 Scope P1-SW-12/13 明确 Observe | 以上位约束为准，#47/#54 保持 Observe；不开发 DPDK enable、switchdev 或 SR-IOV restructuring。 |
| D-03 | 当前已接受 OpenFlow 原型仅 Observe；Architecture/Scope/IA SW-17 另列有限 local Expert Manage | 以 #53 独立跟踪；ownership、native operation、恢复和安全 Gate 通过前不开放写入，#47 不被扩大。 |
| D-04 | lab VLAN 缺 native-untagged/all-VLAN/QinQ/cvlans，原型不能代表 Manage 完成 | 在 #22/#42 补齐原生语义，#38/#39/#40 完成安全闭环；高级能力依 provider/validator。 |
| D-05 | #26 等原型条目只明确 sFlow/IPFIX，Scope 和 Architecture 还包括 NetFlow | #26/#43 明确 NetFlow，三类 exporter 分别验收；不建设 collector。 |
| D-06 | IA Tunnels/Telemetry 写 Basic Manage，Scope/Architecture 对原生常用域为 Manage | Basic 表示界面开放的已闭环子集；#43 的 GA 仍验证承诺的静态 Tunnel 和原生 exporter，不以页面等级静默删功能。 |
| D-07 | IA CH-05 有创建 checkpoint 入口；Architecture 仅一个成功事务推进的 rolling LKG | #40/#48 保持唯一 LKG；比较/导出与 restore-to-Candidate 可用。手动创建不能绕过成功/确认门槛或变成多版本配置库。 |
| D-08 | IA tablet 可包含单对象普通 Manage；当前 AGENTS/接受原型职责更窄 | #54 先保持 Standard 审阅、推荐诊断和已有 Safe Apply；扩大 tablet 发起配置需独立 UX 审阅记录。 |
| D-09 | lab 单库、节点级阻塞、递增 fixture generation 与批准架构不同 | 正式采用双库、grant enforcement、字段/依赖级保护和多证据 generation；旧 fixture 不导入真实身份。 |
| D-10 | 首切片 #41 不包含全部页面、双语、搜索或管理员手册 | 新增 #52 搜索/一跳拓扑、#54 全页面/双语、#55 操作/API 文档；不扩大 #41 引入依赖循环。 |
| D-11 | Scope 安全闭环概括为所有持久配置；Architecture 对 metadata、安全域和 OVS 配置有不同操作语义 | OVS 交换配置经过共享 Candidate/Validation/Apply；TLS 用证书 candidate，AAA 用 test/confirm，用户/授权由 mgrd 强制策略与 Audit；不把这些操作伪装成 OVSDB 写。 |
| D-12 | Scope AI-01/02 的故障不影响 dataplane 与已授权安全回滚可能同时发生 | 进程故障本身不得 stop/restart OVS 或改配置；已入场事务的恢复只能按其持久安全策略执行受保护 rollback。 |


## Architecture 第 11 节逐域对照

Architecture §11 的 Bridge/Port/Interface、VLAN、Bond/LACP、STP/RSTP、Multicast、Profile/Drift、Mirror、QoS/Queue、Tunnel、Isolation、OpenFlow、DPDK、Offload、Telemetry、Endpoint/Local Topology、OVS Lifecycle、Management Interface 共 17 个能力域，分别落在 P1-SW-01–16 与 P1-LIFE-01/02。§11.2 六组原生 validator 的责任为：VLAN #22/#42，Bond/LACP 与 SLB #42，STP/RSTP #23/#43，Management IP #49，OpenFlow ownership #47/#53。以上均有 G1/G3 和相关 G5/G6 验收，未从现有表单反推或缩减产品范围。

## 交付物与退出条件


| Scope 交付物 | 责任 |
| --- | --- |
| D1 Source Repository | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#33](https://github.com/sampsonlor/ovs-webui/issues/33)、[#41](https://github.com/sampsonlor/ovs-webui/issues/41)、[#50](https://github.com/sampsonlor/ovs-webui/issues/50)、[#51](https://github.com/sampsonlor/ovs-webui/issues/51)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) |
| D2 Runtime Binaries | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#41](https://github.com/sampsonlor/ovs-webui/issues/41)、[#50](https://github.com/sampsonlor/ovs-webui/issues/50) |
| D3 Packages | [#50](https://github.com/sampsonlor/ovs-webui/issues/50)、[#51](https://github.com/sampsonlor/ovs-webui/issues/51) |
| D4 Public API | [#33](https://github.com/sampsonlor/ovs-webui/issues/33)、[#34](https://github.com/sampsonlor/ovs-webui/issues/34)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37) |
| D5 WebUI | [#41](https://github.com/sampsonlor/ovs-webui/issues/41)、[#54](https://github.com/sampsonlor/ovs-webui/issues/54) |
| D6 State and Recovery | [#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#35](https://github.com/sampsonlor/ovs-webui/issues/35)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#48](https://github.com/sampsonlor/ovs-webui/issues/48) |
| D7 Diagnostics and Support | [#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#46](https://github.com/sampsonlor/ovs-webui/issues/46) |
| D8 Configuration Portability | [#48](https://github.com/sampsonlor/ovs-webui/issues/48) |
| D9 Testing and CI | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#51](https://github.com/sampsonlor/ovs-webui/issues/51) |
| D10 Documentation | [#55](https://github.com/sampsonlor/ovs-webui/issues/55) |

| Scope Exit ID | 验收归属 |
| --- | --- |
| E1 Scope Completion | #51 汇总本映射全部 Required/Manage 与 conditional/Observe 处置 |
| E2 Security | #34/#35/#53 和 G6，无 P0/P1 安全缺陷 |
| E3 Transaction Safety | #38–#40/#49，G1/G3/G5 |
| E4 Platform | #50/#51，原生 amd64/arm64、发行版及 x86 RC 硬件 |
| E5 Packaging | #50/#51，deb/rpm/tarball 与不影响 OVS 的安装/升级/卸载 |
| E6 API | #33/#54，正式发布 OpenAPI baseline 与 UI/API parity |
| E7 Recovery | #32/#40/#48/#49，实例/备份/重启/降级 |
| E8 Documentation | #55，经实际环境按文档走通并由 #51 引用证据 |


## 必测不变量


| Scope AI | 场景 | 责任 |
| --- | --- | --- |
| AI-01 | webd crash | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#50](https://github.com/sampsonlor/ovs-webui/issues/50)、[#51](https://github.com/sampsonlor/ovs-webui/issues/51) |
| AI-02 | mgrd crash and journal recovery | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#51](https://github.com/sampsonlor/ovs-webui/issues/51) |
| AI-03 | uninstall preserves OVS | [#50](https://github.com/sampsonlor/ovs-webui/issues/50)、[#51](https://github.com/sampsonlor/ovs-webui/issues/51) |
| AI-04 | unknown native fields preserved | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#42](https://github.com/sampsonlor/ovs-webui/issues/42)、[#43](https://github.com/sampsonlor/ovs-webui/issues/43) |
| AI-05 | concurrent different fields succeed | [#39](https://github.com/sampsonlor/ovs-webui/issues/39) |
| AI-06 | same-field deterministic conflict | [#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#39](https://github.com/sampsonlor/ovs-webui/issues/39) |
| AI-07 | rollback conflict after external write | [#40](https://github.com/sampsonlor/ovs-webui/issues/40) |
| AI-08 | OVS missing remains manageable | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45) |
| AI-09 | new generation blocks old state | [#36](https://github.com/sampsonlor/ovs-webui/issues/36)、[#38](https://github.com/sampsonlor/ovs-webui/issues/38)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40)、[#48](https://github.com/sampsonlor/ovs-webui/issues/48) |
| AI-10 | manager.db corrupt fails safe | [#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40) |
| AI-11 | provider unavailable preserves Unknown | [#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#45](https://github.com/sampsonlor/ovs-webui/issues/45)、[#47](https://github.com/sampsonlor/ovs-webui/issues/47) |
| AI-12 | resource pressure protects safety | [#31](https://github.com/sampsonlor/ovs-webui/issues/31)、[#32](https://github.com/sampsonlor/ovs-webui/issues/32)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#44](https://github.com/sampsonlor/ovs-webui/issues/44)、[#46](https://github.com/sampsonlor/ovs-webui/issues/46) |
| AI-13 | central SECRET redaction | [#35](https://github.com/sampsonlor/ovs-webui/issues/35)、[#37](https://github.com/sampsonlor/ovs-webui/issues/37)、[#46](https://github.com/sampsonlor/ovs-webui/issues/46)、[#48](https://github.com/sampsonlor/ovs-webui/issues/48) |
| AI-14 | OutcomeUnknown reconciliation | [#39](https://github.com/sampsonlor/ovs-webui/issues/39)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40) |
| AI-15 | Commit differs from Applied | [#39](https://github.com/sampsonlor/ovs-webui/issues/39)、[#40](https://github.com/sampsonlor/ovs-webui/issues/40) |
| AI-16 | management migration rollback | [#49](https://github.com/sampsonlor/ovs-webui/issues/49) |


Architecture §17 另有 webd 被攻破不能自行扩大 Auth Grant 的硬不变量，由 #34 的篡改/伪造/撤销测试覆盖；原生 strong reference/local internal interface 不变量由 #42 覆盖。不能只验 Scope 的 16 行而漏掉上位架构要求。

## 审阅结果

范围映射完成为待审稿：58 个 Scope ID、53 个 Page ID、17 个 Architecture 能力域、10 项交付物、8 项 Exit 和 16 项 Scope AI 均有责任与 Gate。没有在本轮宣布任何正式模块完成或改变已接受 Observe 边界。#30 保持 In Progress，直到[设计审阅](../reviews/PHASE1_DESIGN_v0.1.md)接受后再关闭。
