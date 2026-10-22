# OVS 3.3.9 STP/RSTP 隔离只读原型 v0.1

日期：2026-10-21。范围：已认可方案的 **P1 隔离实现与测试**；无生产部署或生产 caller。[ADR 0002](../adr/0002-optional-spanning-tree-runtime-witness.md) / [完整阶段计划](SPANNING_TREE_RUNTIME_WITNESS_PLAN_v0.1.md)。具体提交是否通过资格测试，以该提交的两架构独立报告及完整 CI 为准；本文定义实现和验收条件，不用既有 M2c3 结果替代新扩展测试。

## 实现位置与调用边界

[固定源码扩展](../../extensions/ovs-witness/3.3.9/README.md)由 3.3.9 patch、overlay 和输入摘要注册表组成。仅适用上游提交 `1a3fefbcd0b0f70ced621b92e286fef3dffaac09`；下载 archive 在解包前校验，修改前再次核对原始文件摘要。默认关闭；显式 `--enable-ovs-webui-witness` 后在原有 unixctl socket 注册唯一新命令 `ovs-webui/stp-witness-v1`，恰好接收小写 UUID 和 64 位小写十六进制 nonce。

命令只读取已配置绑定、IDL 已处理状态和真实 engine local 参数，不运行 IDL、重配、事务、锁请求、重连或 setter。结果保持计划中的两种闭合对象：known 完整采样，或仅 version/nonce/state/reason 的 withheld。实际响应经原有 JSON-RPC string 封装，内层上限 16 KiB；私有 Go decoder 对完整 wire 另限 64 KiB，先拒绝重复键、错误 Unicode、尾随数据，再执行[闭合 schema](../../internal/ovswitness/witness.schema.json)、类型与数值语义校验。失败只返回全零 Sample。

Go codec、摘要复现及显式独占检查位于 [internal/ovswitness](../../internal/ovswitness/decode.go)，唯一命令 caller 是 [CI decoder probe](../../tests/ovs_witness/decoder/main.go)。webd/mgrd 不导入该包；没有新 HTTP/IPC/CLI 生产入口、通用 RPC 或授权开关。`Exclusive` 只检查观察到的协议/标志关系和无损毫秒转换；既有参数、原值、setter 双向约束和单位 caveat 仍是独立门槛。

## 身份、配置与锁状态

| 证据 | 实现及边界 |
| --- | --- |
| daemon instance | bridge 初始化时生成 UUID，额外由测试 harness 的实际 SO_PEERCRED PID/UID、打开的 `/proc/PID/exe` FD 和构建摘要核对；JSON 自述不能证明来源 |
| runtime instance | 实际 ofproto 对象创建时生成 UUID；同名新 Bridge UUID 可能复用该实例，但不能复用旧配置绑定 |
| binding/configuration epoch | daemon 范围的 uint64 单调计数，创建/重新配置/绑定更换时记录；Bridge 销毁重建不重置全局计数。达到 UINT64_MAX 后该进程永久 withheld，不回绕 |
| authority epoch | 在 ovsdb-cs 的 session/FSM/monitor 创建或重置、实际条件更新请求、锁请求/通知/状态变更路径递增，并由 IDL 只读 accessor 暴露；没有通过 handler 获取或试探锁 |
| configured input | 在配置 pass 完成及 provider 最后处理后复制 root/Bridge UUID、runtime instance、next_cfg、IDL seqno、七键原始值/null、两标志和输入摘要；任一相关错误使整个采样 withheld |
| 再核对 | handler 检查当前 root/UUID、seqno/cfg、authority epoch、完整输入摘要和 runtime instance；不把当前 cfg 指针或 MAC 当作历史安装身份 |
| native local | 主线程检查两个真实 engine 指针；各协议库一次持锁复制 local identity/timers，两个库不嵌套持锁。STP 使用本地 `bridge_*` 与既有 timer-to-ms，RSTP 从本地秒值转毫秒 |

禁用/启用、Port setter 的可报告错误和混合手工 STP port number 错误均进入证据失败记录，原有返回流程、算法、setter 顺序、定时器单位和默认值不改变。无错误返回的 setter 仍可能保留或裁剪输入，必须报告实际 local；`completion=complete` 仅为已完成相关配置流程，绝不是 Applied。

IDL 当前 seqno 只用于关联；其原生 uint32 回绕不能替代新 epoch。锁状态仅是 **IDL 最近已处理的 server 状态**，可能存在通知延迟。该接口没有外部 OVSDB 事务与锁归属的原子保证，也不保证返回后状态不变。后续执行仍需独立设计预约、dispatch 检查、原生 CAS、实际安装证明及 OutcomeUnknown/Safe Apply。

## 固定输入清单与规范摘要

[C 字段清单](../../extensions/ovs-witness/3.3.9/input-columns.json)和 [Go 原子类型/基数清单](../../internal/ovswitness/input-types.json)固定 3.3.9 的 **52 个字段**：Open_vSwitch 5、Bridge 17、Port 16、Interface 14。CI 对照实际源 schema 校验两份清单和 C overlay。它是所选图的 IDL runtime 输入摘要，**不是库存依赖 hash 或全数据库证明**。

固定表顺序为 Open_vSwitch → Bridge → Port → Interface，root 和目标 Bridge 各一行，Port/Interface 按规范 UUID 升序；列按注册表名称升序。UTF-8 文本原样编码，不做 Unicode 归一化。规范字节如下，行尾都是 LF：

```text
ovs-webui.stp-input.v1\n
@表名:规范UUID\n
列名=排序后的原子条目,排序后的原子条目\n
...
```

每个原子先加类型前缀，再对完整字节串做小写 hex：字符串 `s` + UTF-8，整数 `i` + 规范有符号十进制，布尔 `b0`/`b1`，UUID `u` + 规范 UUID。set 条目为 `hex(key):`；map 条目为 `hex(key):hex(value)`。按完整 ASCII 条目排序，不依赖 C 内部 datum 顺序、JSON escaping 或 map 顺序；重复 key/set 成员拒绝。空集合编码为空条目串，类型由固定列清单确定。Interface 行最后增加 `ofport=实际16位OpenFlow端口号\n`。完整规范字节使用 SHA-256；独立 Go 复现从同一实际数据库的固定只读 select 获取输入，使用数据库报告的 ofport 与 C 的实际 iface 对照。

摘要包含实际 UUID 引用及原始 maps，不只包含七键或名称。root 必须唯一且恰好引用该 Bridge 一次；图中最多 128 Port、每 Port 一个独有 Interface，且必须对应实际已配置 iface/ofport，类型限 system/默认 system/internal。未知或重复关联拒绝。Open_vSwitch 的 datapaths/manager_options/ssl、Bridge 的 controller/mirrors/netflow/sflow/ipfix/auto_attach/flow_tables、Port 的 qos 引用必须为空，因为原型没有证明这些关联表的配置。

有意排除的管理元数据及输出：各表 external_ids；root 的 cur_cfg/next_cfg（单独检查）和版本/平台/能力输出；Bridge datapath_version；Port bond_active_slave；Interface ofport（使用上述实际端口号单独关联）、error、cfm_flap_count，以及 schema 声明的 ephemeral 列。IDL omit 的列不能冒充默认观测；读取每一注册列前检查 monitor mode 与 committed old datum。omit_alert 输入变化会重新算摘要，不能沿用旧 pass 的证据。管理元数据/权限、整库版本围栏和更多保守图限制仍由后续组合阶段保留，不能从排除这些列推导“不必验证”。

原始七键每值最多 128 字节；摘要原子字符串最多 4096 字节，每列 datum 最多 128 项，规范输入最多 256 KiB。超限不截断。Go CI 输入 probe 额外限制 1 MiB JSON 和既有严格解析器的 token/深度预算。known payload 中没有全量输入、路径、argv、指针或密钥；CI 报告的完整输入只来自专用 namespace 的合成配置。

## 隔离测试与证据

独立 CI job 在 amd64、arm64 真实 runner 上分别构建：

| 构建 | 要求 |
| --- | --- |
| default | 实际编译且控制命令不存在；保留上游 STP/RSTP 相关测试 |
| enabled | `-Werror` 实际构建、上游 STP/RSTP 相关测试、C 单元检查及实际 kernel system Bridge 场景 |
| sanitized | 全量新 C 代码随 OVS 使用 ASan + UBSan 构建，重复 C 单元与实际 kernel 场景；C 单元额外启用 leak 检查 |

native daemon 退出时不运行 LSan；没有运行 ThreadSanitizer，也不把 mutex stress 称为 C race 检测通过。ASan/UBSan 的真实错误均使测试失败并保留日志。default 与 enabled 的上游相关测试不能跳过或以 sanitizer 结果代替。

C 单元检查覆盖 epoch 饱和、不支持 provider 的 EOPNOTSUPP、本地 STP 毫秒裁剪行为、并发 local snapshot/lifetime、RSTP setter 保留原值。Go 测试覆盖两层 framing/闭合 schema/Unicode、全字段原子返回、数值边界、无损单位检查、规范摘要 golden、集合顺序/单元素等价、真实图引用与大小限制，并进行有界 fuzz。

native fixture 只启动本次源码生成的工具，放入 root 私有目录与专用 network namespace；使用自己的 ovsdb-server、配置文件、unixctl socket、内核 system Bridge 和 veth，不安装或操作主机现有 OVS 服务。每次采样前后核对实际 peer executable FD 的摘要和身份，不缓存路径 hash；这是隔离测试的来源检查，尚不是已资格化的生产 build trust/预算方案。

真实场景包括 STP/RSTP 排他状态、禁用和双配置标志、root/non-root 本地与 learned timers 不同、重复读取不改变配置和 epochs、原始 active/inactive/unset 值、C/Go 摘要与不透明 Unicode map 值、原始值超限、未建模关联、同名 UUID 替换且实际复用 ofproto、已处理配置 ABA、omit_alert 变化、暂停 daemon 的传输超时、混合手工端口号失败、实际 IDL 锁抢占/重获、daemon 及数据库重启。等待仅用于建立明确的测试状态，不重试已失败的验收断言，不改变生产两秒期限。

每架构 `ovs-witness-*` artifact 保存完整构建/上游/C 单元/Go/native 报告和 build manifest，固定 source commit、实际 checkout、上游 archive/source/patch/overlay 摘要、编译器、构建选项及实际二进制 SHA-256。失败运行单独保留，只有完整报告通过才接受，不拼接不同运行的局部通过结果。既有全部 CI，包括双架构 Go/native/browser/recovery，继续独立强制；不修改 #71 的延期状态或相关门槛。

## 仍未包含的工作

P2 才接入既有受保护预检，复用当前专用权限及原凭证上限、双 root 策略、保守库存图、实际数据库连接与文件来源证明和两次原生配置围栏；并分别移植及验证 3.7.1/4.0.0。P1 没有替换当前 M2c3 的名称采样，也未宣称这些版本获得新资格。

P3/P4 才处理独立密封执行、持久图预约、受保护写入/补偿、High-risk Safe Apply 和真实管理路径丢失/崩溃恢复。生产签名包、SBOM、完整可复现发布与实际部署/回退仍需具体方案和验收。当前不部署、无新的生产执行权限；#23 / #43、Basic Manage 和 Phase 1 继续保持未完成。
