# STP/RSTP 可选运行态只读扩展：实现与测试方案 v0.1

日期：2026-10-21。状态：**方案已认可，已授权 P1 隔离实现与测试，未部署**。用户已选择可选只读扩展，部署时使用对应 OVS 构建。P1 的具体清单、编码和验收边界见[原型说明](SPANNING_TREE_WITNESS_PROTOTYPE_v0.1.md)；本文件保留完整阶段计划。决策记录见 [ADR 0002](../adr/0002-optional-spanning-tree-runtime-witness.md)。关联 #23 / #43；M2c3 基础为 PR #98 / `09d1347876b356d51e1011e15e2c63c650201014`。

## 1. 目标与边界

为既有私有预检补充三个缺口：已配置 Bridge UUID 与真实运行对象的绑定；同一次采样中的 STP/RSTP 实际启用状态；该 ovs-vswitchd 已处理的 IDL 连接、锁和配置阶段状态。原型只读取证据，先验证这些证据能否稳定、完整地建立。

继续保留[受保护预检](SPANNING_TREE_FENCED_PREFLIGHT_v0.1.md)的当前专用权限、原凭证权限上限、两份独立 root 策略、不可变管理 ID/UUID/generation、保守 system Bridge 图、完整配置围栏、进程及数据库连接验证、两秒总期限和全量失败清空。不能用新接口替代这些检查。

P1 不增加生产 caller、执行操作或配置写入。`bridge.spanning-tree.prepare` 的无条件阻断保持不变。`known` 仅表示扩展给出了符合其契约的采样，不表示 Authorized、Reserved、Applied 或 Safe Apply 成功。

## 2. 源码证据与补丁位置

首次原型固定上游 **OVS 3.3.9 / `1a3fefbcd0b0f70ced621b92e286fef3dffaac09`**。以下均检查了该提交的实际源码；源码文件摘要见[来源清单](../reviews/evidence/SPANNING_TREE_WITNESS_SOURCES_v0.1.json)。表中的新增函数和字段是拟议补丁，不是已有接口。

| 已核实行为与主来源 | 拟议修改和验证重点 |
| --- | --- |
| [bridge.c:438](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/vswitchd/bridge.c#L438) 设置 IDL 锁；随后的初始化有 `omit` / `omit_alert` 列 | 不能声称 IDL 已保存全部配置；逐项列出 runtime 消费字段和监控模式，验证未告警变化也使证据失效 |
| [bridge.c:1860](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/vswitchd/bridge.c#L1860) / 1938 的 Bridge/Port setter 有日志后返回或继续；禁用路径也调用 setter | 记录相关配置阶段和所有可报告错误，保留原有顺序、返回流程及算法；任一失败整份证据 withheld，不能以“函数走完”证明安装成功 |
| [bridge.c:2007](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/vswitchd/bridge.c#L2007) 两配置标志都真时优先 RSTP | 报告实际两个 engine 状态；Go 另行拒绝不独占的配置/运行态，不能把隐式优先级当作协议切换实现 |
| [bridge.c:2065](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/vswitchd/bridge.c#L2065) 现有对象按名称更新 `br->cfg` | 在替换配置指针前失效旧绑定；完成相关配置后显式记录 UUID、运行实例和绑定 epoch，不能直接返回当前指针的 UUID |
| [bridge.c:3290](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/vswitchd/bridge.c#L3290) IDL 未持锁可直接返回；配置和 `cur_cfg` 更新后异步提交 | 捕获连接/锁变化，配置开始即失效；配置结束、provider 最后处理和实际 local snapshot 都有效后才允许 known；计数相等只作必要条件 |
| [ovs-vswitchd.c:130](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/vswitchd/ovs-vswitchd.c#L130) 主循环先 `bridge_run()` 再处理 unixctl | handler 只取快照，不主动 `idl_run`、等待、重配、重连、提交事务或刷新时间戳；验证相关对象只在预期线程创建/销毁 |
| [ovsdb-cs.c:623](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/lib/ovsdb-cs.c#L623) 处理 session 变化；[1297–1398](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/lib/ovsdb-cs.c#L1297) 处理 lock reply、locked/stolen 和锁状态 | 在实际连接重置、监控重置、锁状态事件路径增加只读可导出的 epoch；覆盖直接赋值/重置路径，不能只在两次采样之间比较布尔值 |
| [ofproto-dpif.c:2651](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/ofproto/ofproto-dpif.c#L2651) / 2799 通过真实 engine 指针报告 enabled | 为 ofproto 增加有明确 unsupported 结果的只读组合 snapshot；同一次主线程采样检查两个 engine，避免两个文本请求之间的 ABA |
| [ofproto.h](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/ofproto/ofproto.h#L106) 的既有 status 没有完整本地 timers | 新增私有 snapshot 类型和 provider 回调，不把 EOPNOTSUPP 解释为 disabled |
| [stp.c:550](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/lib/stp.c#L550) 的 getter 返回本地 bridge timer 的毫秒值；[rstp.c:536](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/lib/rstp.c#L536) 等维护本地 bridge 参数 | 分别在各库一次持锁复制本地 identity/timers；不使用 learned root-port timers；检查单位换算、锁顺序、对象生命周期和无副作用 |
| [unixctl.c:132](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/lib/unixctl.c#L132) 将命令结果作为 JSON-RPC string，必要时附换行 | 使用现有外层 framing，内层为严格 JSON 文本；Go 对两层分别解码并计入完整 wire 预算 |

OVSDB-server 3.3.9 已检查的 [unixctl 注册表](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/ovsdb/ovsdb-server.c#L823) 没有提供此完整证据的命令。原型不向 server 增加写入或抢锁接口。

## 3. 构建和调用契约草案

拟增加构建选项 `--enable-ovs-webui-witness`，默认关闭。开启后才注册私有命令 **`ovs-webui/stp-witness-v1`**，恰好两个参数：规范小写 Bridge UUID，以及由 mgrd 每次生成的 32 随机字节、64 位小写十六进制 nonce。lookup 必须使用已配置绑定；不接受 Bridge 名称、路径、操作名、原始事务或额外参数。命令名/可用性本身不是能力授权。

本机 Unix socket 仍由已有 peer UID、PID/start、boot、可信 executable、实际 DB 文件和双向连接验证保护。nonce 只绑定本次请求，不提供认证。保留既有 socket 访问策略；不把 bearer、密钥、路径、argv、内存地址、全局配置或逐端口流量写入返回值。

拟议内层 JSON 是以下两个**互斥且封闭**的对象之一。以下 known 示例为合成数据，只展示字段布局，不是实际 OVS 响应或通过验证的证据：

```json
{
  "schema_version": "ovs-webui.stp-witness.v1",
  "nonce": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "state": "known",
  "daemon_instance_id": "11111111-1111-4111-8111-111111111111",
  "database": {
    "root_uuid": "22222222-2222-4222-8222-222222222222",
    "connected": true,
    "lock_name": "ovs_vswitchd",
    "lock_owned": true,
    "lock_contended": false,
    "authority_epoch": "9",
    "idl_seqno": "17"
  },
  "bridge": {
    "ovs_uuid": "33333333-3333-4333-8333-333333333333",
    "name": "br-test",
    "runtime_instance_id": "44444444-4444-4444-8444-444444444444",
    "binding_epoch": "3"
  },
  "configuration": {
    "epoch": "12",
    "idl_seqno": "17",
    "root_next_cfg": "5",
    "completion": "complete",
    "setters_reported_error": false,
    "runtime_input_format": "ovs-webui.stp-input.v1",
    "runtime_input_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "source": {
      "stp_enable": true,
      "rstp_enable": false,
      "parameters": {
        "stp-priority": null,
        "stp-hello-time": null,
        "stp-max-age": null,
        "stp-forward-delay": null,
        "rstp-priority": null,
        "rstp-max-age": null,
        "rstp-forward-delay": null
      }
    }
  },
  "native": {
    "stp": {
      "enabled": true,
      "local": {
        "priority": 32768,
        "system_id": "02:00:00:00:00:01",
        "hello_time_ms": 2000,
        "max_age_ms": 20000,
        "forward_delay_ms": 15000
      }
    },
    "rstp": { "enabled": false, "local": null }
  }
}
```

```json
{
  "schema_version": "ovs-webui.stp-witness.v1",
  "nonce": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "state": "withheld",
  "reason": "IDL_LOCK_UNOWNED"
}
```

| 字段/规则 | 拟议含义和限制 |
| --- | --- |
| `daemon_instance_id` | 进程启动时生成随机实例 ID；不能从 PID 推导；仍须外部 SO_PEERCRED/start/boot 绑定 |
| `runtime_instance_id` | 实际 ofproto 对象创建时生成的随机实例 ID；同一对象重配不冒充新对象；对象重建必须改变 |
| `authority_epoch` | 新增 uint64，连接/监控重置、锁请求/释放/丢失/获得/争用等相关事件均推进；不是现有 IDL seqno 的改名 |
| `binding_epoch` / `configuration.epoch` | 绑定失效/替换以及相关配置 pass 开始时推进；完成后记录对应已复制配置，不读取当前 `br->cfg` 冒充旧 pass 输入 |
| `idl_seqno` | 保留现有 unsigned sequence 供关联；允许其原生回绕，不能独立作为防 ABA 标识。配置捕获与当前 seqno 不匹配时保守 withheld |
| epoch 与 cfg 计数 | 使用规范十进制字符串，避免 JSON number 精度丢失；epoch 范围 0..2^64−1，cfg 范围 0..2^63−1；禁止符号/前导零/溢出，epoch 耗尽后永久 withheld 至新进程实例 |
| `source` | 配置 pass 实际读取的两个标志和完整七键原始字符串/null；null 表示缺键，不填默认，不改写 inactive 键 |
| `runtime_input_digest` | P1 已固定 52 字段、实际 Interface 关联和跨语言规范编码，见[原型说明](SPANNING_TREE_WITNESS_PROTOTYPE_v0.1.md)；不等于库存依赖 hash，不能只哈希名称、七键或当前配置指针 |
| `native` | 两 engine 的真实存在状态；禁用时 local 必须 null；启用时所有 local 字段必须完整。未知 provider/getter 使整份 withheld；known 仍可报告不独占状态，由 Go 拒绝执行 |
| 本地 timers | STP 在库内复制 local `bridge_*` 并转换为毫秒；RSTP 从秒做检查溢出的 ×1000。Go 若不能无损转换到现有秒语义则拒绝，不舍入、不采用 learned 值、不放宽已有 STP 单位 caveat / RSTP 双向 setter-order 检查 |
| `withheld` | 仅保留版本、nonce、state、固定 reason；不得夹带部分 Bridge、配置或 native 数据 |

固定 reason 初拟：`IDL_NOT_CONNECTED`、`IDL_LOCK_UNOWNED`、`IDL_LOCK_CONTENDED`、`CONFIGURATION_IN_PROGRESS`、`BRIDGE_NOT_CONFIGURED`、`BINDING_UNPROVEN`、`PROVIDER_SNAPSHOT_UNSUPPORTED`、`SETTER_REPORTED_ERROR`、`WITNESS_BUDGET`、`EPOCH_EXHAUSTED`。wire 错误、无此命令或未知版本由 Go 转为静态不可用原因，不根据宽泛错误字符串推导 engine 不存在。

拟议新响应上限：内层 UTF-8 16 KiB，包含 JSON 转义、外层和空白的完整 wire 64 KiB，JSON 深度 8，Bridge 名称沿用现有 15 字节基本范围，原始参数每值至多 128 字节；原型计算摘要至多 128 Port / 128 Interface。更大的原生状态整份 withheld，不截断，不返回“部分已知”。所有对象拒绝未知/重复键、非法 UTF-8/NUL、缺失/多余字段、尾随非空白、错误 nonce 和版本。原型必须将这些限制实现为闭合 schema、严格解码器及语义范围校验；本方案不新增公开 schema。

## 4. 原型内部实现顺序

1. **事件与生命周期。** 在 ovsdb-cs 的真实事件和重置路径推进 authority epoch，通过只读 accessor 经 IDL 暴露。Bridge/ofproto 销毁或替换、同名新 UUID、配置开始即失效 witness。审计所有直接状态赋值、监控取消/重建、回绕和对象复用路径；两次 bool 相同不能掩盖中途丢锁再获锁。
2. **复制配置输入。** 在相关配置 pass 开始保存 root UUID/next_cfg、Bridge UUID、运行实例、两标志/七键和有界输入摘要；不持有会被 IDL 变更替换的借用指针。setter 报错、混合手工 STP port number 导致禁用等分支记录失败，不改变 native 行为。最后的 provider 处理完成后才记录配置完成。
3. **定义跨语言摘要。** 列出实际消费字段、默认/缺失/集合/映射/UUID 的规范编码、排序和 domain separator，在 C 与 Go 固定相同 golden vectors。将实际拓扑关联和相关 ofport 生命周期纳入绑定；保留现有外部完整 persistent CAS 和管理策略校验。IDL 被 omit 的管理元数据不能冒称已观测，omit_alert 的 runtime 相关变化必须主动失效/比较；未知列/版本不默认为兼容。完整输入清单和编码是原型必须完成的退出条件，示例中的摘要不是算法规格。
4. **真实本地快照。** 新增 lib/stp、lib/rstp 只读 local snapshot 和 ofproto 组合回调；验证对象指针生命周期由哪个线程保护，并在各库一次持锁复制完整 local。避免跨 STP/RSTP 库嵌套持锁；若不能证明同一次主线程采样期间 engine 存在性稳定，则原型失败，不能拿两次 show 代替。
5. **纯只读 handler。** 查找已配置 UUID，检查 IDL/绑定/配置状态，采样前后复核 epochs 和实例一致后生成 JSON；任何失败只生成 withheld。不得执行 setter、IDL run、事务、reconnect、锁请求或缓存刷新；时间戳由 Go 在实际收帧时记录，不能由扩展自称 fresh。
6. **隔离的 Go 解码与组合。** 第一阶段仅测试 harness 调用；验证受信构建、当前权限/策略、source 和两层 JSON，再把捕获输入和 runtime 与原生围栏、reviewed original 对比。任何拒绝输出零值；不回退到同名文本读数以补足执行证据。

建议后续组合顺序为：当前权限/策略 → 固定 DB 围栏 A → witness A → witness B → 相同 DB 围栏 B → 当前权限/策略及来源复核。两 witness 除 nonce 外的关联实例、epochs、输入和 local 必须一致；DB 版本及配置也必须一致，仍在同一两秒期限内。具体布局由原型测量并用断点故障验证；预算不足就拒绝，不能加重试或延长期限掩盖。它只证明这一段采样的一致性，不预约返回之后的状态。

## 5. 已知限制与必须解决的问题

| 问题 | 退出条件/处理 |
| --- | --- |
| IDL 可能尚未处理 server 的 stolen/断连通知 | 契约明确为“最近已处理状态”，不能称实时 server 权威；测试延迟通知和排队消息，不在 handler 偷跑 IDL。若后续执行要求原子锁归属保证，这个只读接口不足，另行设计；在此之前不开放写入 |
| witness 返回后，锁、运行对象或配置仍可能变化 | 后续单独设计 dispatch recheck、持久图预约、原生事务 CAS、安装确认和 OutcomeUnknown；应用图锁不能锁住外部 OVS 客户端，预检通过不能直接标记 Applied |
| native setter 可保留/裁剪输入且无错误返回 | 必须比较真实 local 原值和预期值并保持既有双向参数限制；`completion` 和 `setters_reported_error=false` 都不是安装成功证明 |
| runtime 输入范围仅限固定 3.3.9 保守子图 | P1 已列出字段/排除项和共同编码；扩展范围或更换版本需重新审查并资格测试，无法证明完整时保持 withheld |
| 新锁顺序、摘要计算、两次 witness 可能超过预算 | 双架构压力/取消/无死锁测试；保守失败可接受，不放宽正式截止时间；需要新预算必须另提具体证据和变更 |
| 原版 OVS 和不匹配发行版构建 | 不进入该执行资格；既有观察/草案独立保留，不将方法存在、版本字符串或三个 schema fixture 当作实际版本测试 |

## 6. 实施阶段与验收门槛

| 阶段 | 可交付结果 | 通过后允许的下一步 |
| --- | --- | --- |
| P0：已认可方案 | ADR、补丁位置、接口草案、来源、测试与部署条件 | 已进入 P1；P0 本身没有扩展测试结论 |
| P1：3.3.9 只读原型 | 默认关闭的源码 patch、完整输入规范、构建 manifest、隔离 C/Go 测试、amd64/arm64 实际 kernel system Bridge 证据；无生产 caller | 评审并固定证据契约，决定是否接入私有预检；不能开放写入 |
| P2：组合预检与版本移植 | 将 witness 与既有 source/fence/权限组合；分别完成 3.7.1 和 4.0.0 移植及实际版本双架构资格测试 | 确定可支持的实际构建集合；未通过组合不宣称支持 |
| P3：执行与恢复设计/实现 | 独立密封执行 op、当前专用权限与 root grant、持久图预约、dispatch 复核、固定原生 CAS、精确原始值/unset 补偿、durable journal | 仅对同一 active protocol 的基本参数开展 High-risk Safe Apply 验收；prepare 仍不可执行 |
| P4：故障验收与发布审查 | 真实管理路径损失、超时/失联/崩溃/重启/丢回复、OutcomeUnknown、恢复与回退证据，源提交及合并 main 独立 CI | 满足单独接受条件后，才考虑具体安装和显式启用 |

启用/禁用/切换 STP 与 RSTP、per-port 配置及更复杂图各自需要额外资格测试，不包含在“基本参数同协议修改”通过结论中。#23 / #43 及 Phase 1 不会因 P0 或 P1 完成而关闭，#71 继续延期。

## 7. 测试矩阵与预期结果

以下是完整阶段的**测试要求**，不能整体算作 P1 已通过项。P1 的实际场景、覆盖层次与限制见原型说明及对应 CI 报告；P2/P3/P4 项目仍需分别实现。故障注入仅在可销毁 CI 环境使用。

| ID | 覆盖场景 | 必须观察到的结果 |
| --- | --- | --- |
| W01 | default-off / opt-in 构建、帮助/注册表、错误参数/额外参数 | 默认无命令；仅固定命令和两个强类型参数；无通用命令代理 |
| W02 | 两层重复/未知键、版本/nonce/类型错误、非法 UTF-8、尾随数据、截断、空白撑大、深度/大小超限 | 严格拒绝，无部分数据、重试或默认补值 |
| W03 | uint64/uint32/int64 边界、前导零、负数、溢出、epoch 耗尽、IDL seqno 回绕 | 规范解析；seqno 回绕不能绕过新 epoch；耗尽后 withheld |
| W04 | STP/RSTP root 与 non-root，本地 timers 和 learned timers 不同 | local 来自真实本地字段，毫秒单位准确；不把 learned 值当配置 |
| W05 | 两协议均禁用、异常双启用、provider/getter 不支持、对象不存在 | 准确报告实际状态或 withheld；Go 的独占门槛不通过，未知不变成 disabled |
| W06 | 原始七键字符串/unset、inactive 键、STP hello 单位 caveat、RSTP 保留/裁剪和两个 setter 方向 | 原值无损；原有限制不放宽；实际 local 不匹配即拒绝 |
| W07 | 同名 Bridge 删除/新 UUID、相同 UUID 下 ofproto 重建、类型变化、运行对象复用 | 旧绑定失效；实例/epochs 正确改变；名称或 MAC 不授予身份 |
| W08 | 进程重启、PID 复用、boot/DB 文件/root/schema 变化、另一个同名数据库、代理/错误 executable | 原有来源绑定拒绝；旧 witness 不可复用 |
| W09 | IDL 未连接、未持锁、争用、锁被抢、释放后重获、连接/monitor 重置、相同布尔值 ABA | 所有相关真实事件推进 epoch；未完整重配前 withheld |
| W10 | stolen/断连通知延迟或排队、采样后丢锁 | 不宣称 server 实时锁权威；执行阶段的竞态模型必须单独验证，不能仅凭 known 写入 |
| W11 | 配置开始/未完成、Bridge setter 错误、Port setter 错误、禁用 setter 错误、混合手工 port IDs | 完成标记不误报；任一相关失败 withheld；原生行为/顺序不改变 |
| W12 | 相关 omit_alert 字段变化、输入改回、部分 IDL 视图、C/Go map/set 排序和 null/default vectors | 不拿当前指针替代历史输入；跨语言 digest 完全相同；缺字段拒绝 |
| W13 | 两次 witness 之间配置/锁/绑定 ABA、配置围栏前后变更、ephemeral row version 变化 | 复合预检拒绝并返回全零；不重试掩盖不稳定源 |
| W14 | 重复只读调用、并发配置压力、取消、handler 锁顺序、内存/对象生命周期 | 无死锁/UAF/超时放宽；调用自身不发 setter/事务/重连/锁请求，不触发协议重置或拓扑变更；计数/日志变化与背景活动区分 |
| W15 | 原版/错误 ABI/架构/源码/patch digest/可执行哈希、manifest 或 executable 可被非 root 写入 | 明确不可用，执行保持关闭；不以版本字符串或命令可用性替代校验 |
| W16 | `/proc` / SOCK_DIAG 受限、来源复核失败、两秒耗尽、128 对象/原始参数/response 超限 | 原有限制失败关闭；无提升权限、缓存或截断 fallback |
| W17 | 旧凭证上限、内置 Admin、仅 prepare root grant、当前角色撤销、两 root 策略撤销/恢复 | 不取得新权限；每次 I/O 边界重验当前专用授权，失败不泄露配置 |
| W18 | P3/P4：与 VLAN/Bond/MTU/policing/topology 图预约冲突、过期/取消/崩溃重启、dispatch 瞬间撤权 | 复用现有 durable 安全权威；不能把 prepare 或过期预检变成可执行计划 |
| W19 | P3/P4：事务丢回复、安装未完成、实际管理链路中断、补偿前原图改变、确认/截止竞态 | 未证明安装不得 Applied；持久 OutcomeUnknown/恢复，原始 raw/unset 精确补偿；不盲目重放 |

P1 要求实际 OVS 3.3.9 × amd64/arm64；P2 的目标矩阵如下，每格必须有独立源码/build manifest、native 报告和失败历史。3.3.9 以本次独立源码构建 CI 的完整结果为准，其余版本尚未移植或测试扩展。

| 实际 runtime 源码 | amd64 | arm64 |
| --- | --- | --- |
| 3.3.9 / `1a3fefbcd0b0f70ced621b92e286fef3dffaac09` | P1 独立 CI 报告 | P1 独立 CI 报告 |
| 3.7.1 / `7921d9c6924b8934ea1de9481891ac1172649280` | 待 P2 移植 | 待 P2 移植 |
| 4.0.0 / `b89dc80f0143f5bf3f58c48d54e82cc7f8d29da8` | 待 P2 移植 | 待 P2 移植 |

kernel system Bridge 是正向运行资格证据；dummy 仅用于明确的单元/负向边界。同一 Ubuntu OVS 二进制下的三个 schema fixture 不等于三个 runtime 版本。启用扩展和默认关闭构建都要保留上游相关测试；新增 C 代码做 sanitizer/锁与生命周期检查，适用测试的能力限制需在报告列明。

既有基线保持独立：M2c3 源提交和 main 已分别有双架构 305 个顶层 Go race 通过项、42 个正式 browser 场景及每 schema 16 个 native preflight 场景。它们证明既有实现，不证明本扩展；后续保留旧场景名集合、断言和门槛，再增加扩展场景。不得合并不完整运行的通过结果、忽略 arm64 失败、扩大重试/测试期限或将 #71 写成已修复。保存完整日志、源码/构建摘要和官方 artifact digest；实现合并后按现有流程独立验证 main。

## 8. 部署、信任和维护要求

扩展位于 **OVS 用户态构建**，不计划修改内核模块。实际发行包仍须验证与本机 ovsdb-server、schema、内核和服务单元的配合，不能假定只替换一个正在运行的 ovs-vswitchd 文件即可部署。

拟议 root-owned manifest 固定上游 commit、patch SHA-256、witness ABI、架构、构建参数及实际可执行文件 SHA-256。发布物需有可验证签名/摘要、补丁与许可证说明、依赖清单和可复现构建记录。manifest 和 executable 都不得由非 root 写入；nonce、版本字符串及 JSON 里的自述不能证明运行构建。

Go 必须核实实际 peer 进程的 executable，而非只哈希同名路径。原型评估启动时对打开的真实 executable FD 完整哈希，并把结果与进程实例、FD identity、受信 manifest 绑定，在每次采样前后复核；若允许的文件系统/部署方式不能防止哈希后文件内容变化，就不缓存信任结果。不能每次在两秒预检内无界读取大二进制，也不能把 dev/inode 相等声称为内容相等；具体 FD、换包、内容变更和预算策略须以测试固定后才能接入生产。

生产安装前需要具体主机/包版本、隔离验收、维护窗口、现有服务/配置/数据库备份以及关闭执行的回退步骤。未完成 Safe Apply、预约或恢复任务时不得直接替换/重启 OVS；先按既有 durable journal 处理。回退原版或不匹配构建后立即失去新执行资格，保留审计和恢复记录，不重放旧计划。

每次 OVS 安全更新或发行版 patch 都需重建、重验对应矩阵；源码不匹配时默认拒绝。用户当前已授权 P1 隔离实现与双架构测试，尚未批准任何具体生产安装。P1 完整通过后再开展 P2 组合预检与版本移植。
