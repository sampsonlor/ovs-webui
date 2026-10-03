# Interface 明确 MTU 请求审阅 v0.1

2026-10-03 · #42 E2a / #21。范围见[实现说明](../implementation/INTERFACE_MTU_v0.1.md)。接受须 PR 和 main 完整 CI、双架构原生证据、实际浏览器截图及 annotated tag `phase1-interface-mtu-v0.1`。

| 路径 / 例外 | 保留的验收依据 |
| --- | --- |
| 正常 stage、校验、确认 | 原生 MTU 请求仍为 1500，stage 不写现场；Diff 1500 → 2000，真实设备值达标后才可确认；身份、父关系不变化 |
| 精细回滚 | 2000 → captured 1500；原生 request、observed mtu、Linux 设备值一致，保留外部新增 metadata 和未知 other_config |
| Authority / 权限 | 独立 root Interface 身份 allowlist、明确字段 capability、Validation 撤权、旧 credential ceiling；Reader、手机直接编辑地址禁用 stage |
| 原生边界 | 空请求/缺失列/不支持 schema、Bridge 本地 Interface、Bond、其他 type、raw options 未知/非空、外部控制拒绝；公开空安全 map 不证明 raw 为空 |
| 并发与旧对象 | 原生 dispatch 之前外部修改 mtu_request 导致 CAS 原子失败；补偿之前请求/options/外部控制变化阻断；同名重建不能继承旧恢复 |
| Applied / Unknown | 暂停实际 ovs-vswitchd 时不开始确认窗；丢失提交回复不重放/不生成 target；丢失补偿回复保留 recovery-required |
| Standard / Expert | 同一 Diff 和权限，Expert 补充身份来源，MTU 单位和管理路径提示在两种模式均可见 |
| 响应式与异常 | 桌面编辑；900px 平板、390px 手机审阅禁止新验证；配置漂移需重新 stage；空安全子集的禁用边界、深色、Reader、手机直接表单截图 |
| 回归 | E1 全部截图/分页/provider 故障/身份退休、既有所有三 schema 原生事务、安全恢复、管理连接 VM、认证/TLS和原型浏览器继续通过 |

本批新增每架构三份真实 schema 的 11 项 MTU 原生场景（`go-interface-mtu-{3.3.9,3.7.1,4.0.0}.json`）。此原生字段矩阵使用隔离 OVS dummy provider 的 system 映射；正式浏览器额外在真实 system kernel Bridge 上核对 OVS 报告和 Linux `ip link` 设备 MTU。不能混称三种实际 OVS 二进制版本或把局部 fixture 当作一般物理硬件支持。

目标完整结果：每架构 203 个唯一 Go race 顶层测试、16 项正式浏览器流程、10 次额外 Safe Apply 准入重复和所有既有原生矩阵；共享 230 项单元、3 项隔离集成和 31 项原型浏览器。预期数字不是完成证明，须以实际报告、非跳过结果和截图核验。

本批截图：interface-mtu-editor、interface-mtu-standard、interface-mtu-expert、interface-mtu-tablet-review、interface-mtu-mobile-review、interface-mtu-awaiting-confirmation、interface-mtu-rolled-back、interface-mtu-confirmed、interface-mtu-drift、interface-mtu-unavailable-dark、interface-mtu-mobile-editor、interface-mtu-reader；保留于双架构 frontend-evidence。

## Review disposition

核验 required tests、完整 PR CI 和上述证据后合并 tested head，main 同树复验和完整 CI 通过后创建接受标签，最后删除本地/远程特性分支。#21 / #42 保留 Open / In Progress；本批覆盖 E2a，默认 MTU 请求的受控设置/清空、其他类型与字段及完整图生命周期仍未接受。#71 的 arm64 一次性登录问题继续独立跟踪。
