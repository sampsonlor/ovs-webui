# Interface 原生类型与 patch 配对审阅 v0.1

2026-10-05 · #21/#42 E5。当前交付候选；只有最终 PR/main 完整检查、保留报告、截图和同树验证成立后才能记录接受标签。完整主单继续开放。

| 范围 | 所需证据 |
| --- | --- |
| 原生 schema | `TestInterfacePatchActualSchemasAndUnknownShapes`：3.3.9 / 3.7.1 / 4.0.0 的列形状识别及不兼容 shape；不按版本猜测 |
| 关系与权限 | `TestInterfacePatchPeerSnapshotPermissionAndContract`：同快照稳定三项引用、当前配置权限、运行时契约 |
| 明确例外 | `TestInterfacePatchPeerExceptionsNeverInventLinks`：缺失/歧义/self/type/单向/父关系/未知 datapath/退休或缺失绑定/stale/partial/schema；所有不成立情况为空引用 |
| 同名重建 | `TestInterfacePatchPeerDatapathsAndReplacementIdentity`：不同 datapath 拦截、空 system 默认和重建后的新身份 |
| 真实 OVS/IPC/API | `go-inventory-<schema>.json` 的 `metrics.interface_patch_peer`：6 项检查、configuration 来源、只读、无转发宣称；三份 schema 均保留 |
| 正式浏览器 | 原生类型/配对断开/重建和权限/provider 停止恢复两个新增流程；既有 MTU、库存、Linux、配置观察和所有执行/安全回归均保留 |
| 模式与响应式 | `interface-native-types-standard` / `expert` / `dark` / `tablet` / `mobile`：同一权限、平板 Standard、手机只读、键盘进入 peer、无页面横向溢出 |
| 例外截图 | `interface-patch-one-way` / `missing` / `datapath-mismatch` / `retired` / `withheld-standard` / `withheld-expert` / `stale`；三个引用不以名称替代 |
| 原生类型边界 | `interface-native-types-unknown` / `dpdk` / `tunnel`：未知类型保留、DPDK 配置不证明硬件/运行能力、flow-valued tunnel 不推断固定远端 |

本地要求：242 项单元/契约、Go 包测试、双架构 Linux 静态检查、类型检查、lint 与 `pnpm build`。正式 Gate 仍需 PR/main 的六项 CI、原生 amd64/arm64 各 225 项唯一 Go race 顶层测试及 24 项完整正式浏览器；共享 3 项集成、31 项原型浏览器，三 schema 各域执行、实际管理断链、身份/TLS/存储与额外 admission/MTU 恢复矩阵全部保留。数目是本批期望，最终以下载报告实际核对为准。

证据存入忽略的 `outputs/interface-native-types-{pr,main}/<run>/`，最终汇总 `outputs/interface-native-types-acceptance.json`。复核必须检查具体 reason/ref/source、终态报告无 fail/skip、模式/异常截图及 PR/main 的精确代码树；CI 全绿不能替代这些记录。本批不宣称实物 DPDK NIC、Tunnel 数据面转发、patch 无环策略、类型编辑或一般对象图生命周期完成。

最终接受后追加 #21/#42 完整验收记录并维持 In Progress；记录 annotated tag `phase1-interface-native-types-v0.1`，删除已接受的唯一特性分支。#71 登录根因保持独立开放。

首轮 PR CI `37312321010` 在新增配对流程的初次库存读取失败：OVSDB 创建已返回，但异步 inventory 尚未发布该 Interface，测试 helper 在有界 poll 内直接抛错，未等待后续真实状态。修正仅让该 poll 的 pending absence 保持未满足，继续等待 `PATCH_RECIPROCAL_CONFIGURATION`；之后的对象读取仍严格要求精确存在。保留首轮报告和截图，原超时/零 retry/所有用例/生产门禁不变，接受必须基于修正后完整 PR/main 验收。

第二轮 PR CI `37315198894` 已越过初次库存读取；新增流程在要求 Expert reason 的断言失败。按钮显示的是当前模式，测试误将点击当前模式当作选择该模式，导致实际 Standard 内容被当作 Expert 检查。修正仅在实际模式不符时切换，并核对显示文字和 aria-pressed；Standard/Expert 截图以实际状态为准。第二轮原始日志/报告/截图保留，生产逻辑、超时、零 retry 和完整用例不变。
