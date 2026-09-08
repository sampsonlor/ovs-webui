# System Health 审阅 v0.1

日期：2026-09-08（Asia/Shanghai）。起点：`90533df`；分支：`codex/feat-p1-batch-05-system-health`。前置 Batch 04 已获用户接受，PR #14 已合并，注释标签 `prototype-acceleration-v0.1` 已推送。

**Disposition 更新：2026-09-08 用户接受 System Health 并要求继续下一批；PR #15 合并后以注释标签 `prototype-system-health-v0.1` 记录接受基线。** 本记录覆盖 P1-11 交互原型，不构成真实主机健康、生产授权或数据库恢复验收。

## 依据与范围

遵循 Architecture Baseline v1.0.1 §10 的健康、State Cache、Event / Audit 语义，P1 Prototype Plan v0.1 §10 的 Batch 05 范围及批准 IA 的 Operations / Health（OP-01）。计划路径 `/operations/health` 在当前原型中对应共享 View `system-health`，正式 SPA URL 尚未实现。

入口：`顶栏 Health / 首页共享健康卡片 → Operations → System Health → 组件责任与证据 → 相关对象、预选诊断或原事务恢复`。顶栏、首页和完整页使用同一控制器；跨页、切换模式或筛选不重新创建健康资源。只读健康元数据允许手机刷新；新诊断保留平板 / 桌面门禁，配置仍经 Candidate → Diff / Validation → Apply / Safe Apply。

## 可解释的状态模型

| 域 | 本批证据与边界 |
| --- | --- |
| OVS | ovsdb-server、ovs-vswitchd、schema / generation、Configured / Applied 样本、进程重启证据；不把 Applied 或进程存活等同于全部业务健康 |
| Datapath | br-fabric 的受限包路径样本和原生 Port / Interface 关系；已有 OpenFlow 采集覆盖沿用原查询与 30 秒 TTL，不推断转发成功 |
| Providers | Linux、DPDK、offload、telemetry；已捕获的加速观察覆盖同名样本，保留其来源、时间、generation、未知和降级语义 |
| Management plane | webd / API、mgrd、web.db、manager.db、SecretStore、State Cache、队列压力；API 故障不自动声明数据面故障 |
| Change safety | 直接读取共享事务状态、原事务 ID、checkpoint 是否保留和原 deadline；OutcomeUnknown、rollback conflict、degraded、needs-attention 优先为 Recovery Required |
| Jobs | 读取当前诊断 Job；失败 / 部分结果与缺失 / 过期结果分开；预览不是已执行 Job，完成不是系统健康证明 |

判定顺序为：Recovery Required → Critical → Degraded → Unknown → Recovering → Healthy。已确认故障保留优先级，Unknown 数量单独显示；没有数据的域为 Unknown。默认样本保留 server-08 Down 且意图未确认、offload 证据缺失。其他 review 样本是声明过范围的合成观察，不改写原生库存页的固定样例。

每个组件保留 reason、impact、source、observed value、observedAt、since、freshness、generation 和相关资源。健康观察 60 秒后转为历史 / Unknown；未来时间和 generation 不一致同样失效。OpenFlow 旧采集器没有记录 generation，详情明确显示未捕获，不伪造当前身份。

读失败保留历史值，重试期间也不恢复为当前健康；只有新读成功才清除失败。提交与完成均检查共享服务 / 权限；重复读取被拒绝。权限撤销隐藏并清除缓存，延迟回调不能补回数据；恢复权限需要重新读取。

同一组件、资源和 generation 的健康变化追加共享 Event；相同状态刷新不重复发事件，也不重置故障持续时间。不同对象或实例的新样本不会生成“旧故障已恢复”。页面保留最近 12 条健康变化，完整共享 Event / Audit 和诊断结果不因健康恢复而删除。健康观察本身不追加 Audit。

manager.db Recovery Required 提供原证据与操作员恢复提示；没有假 Restore 或自动创建空库的动作。健康样本不替代 mgrd 最终授权，正式恢复准入、持久化 Health Provider 和 lab API 接线均在本批范围之外。

## 浏览器审阅

环境：Windows Codex 内置浏览器、`http://localhost:3001/` 合成预览；通过可见控件和页面公开 WebMCP 工具操作，无真实网络设备写入。

| 场景 | 实际结果 |
| --- | --- |
| 12 个样本 | Healthy、Degraded、Critical、管理面 Critical、Unknown、Stale、generation mismatch、Recovering、Recovery Required、Empty、Failed 和 Normal 均得到预期状态；Candidate 与 idle 事务未改变 |
| 组件责任 / 筛选 | 域卡片限制列表；搜索无匹配时显示空结果；清除筛选恢复列表；详情有来源、时间与相关资源 |
| Standard / Expert | 相同状态与权限；Expert 增加组件代码、身份和判定说明；模式切换不产生 Audit |
| 事件与时间 | 相同健康样本连续刷新新增 Event 为 0，原 since 保持；恢复转换保留事件时间线 |
| 诊断预选 | member-down 从健康页携带 `Port/bond-storage` 与 link / LACP 模板；不自动运行 |
| 活动 Job | 活动 link / LACP Job 存在时，再从健康页准备 br-fabric 包路径诊断，仍打开原 Job，原模板、参数和 Port scope 保持 |
| 共享 Provider | 加速页捕获 Unknown 后，健康页沿用相同 acceleration snapshot ID 和 observedAt；Healthy 健康样本不能覆盖它 |
| 重复读取 / 权限竞争 | 紧邻第二次读取被 Busy 拒绝；读取中撤销权限后健康组件为空、快照隐藏，恢复权限不复活原缓存 |
| OutcomeUnknown | 合成 VLAN 事务进入 Unknown 后，Healthy 观察仍得到 Recovery Required；刷新前后 Candidate、事务、Audit 完全相同；Confirm 被原流程拒绝 |
| 桌面 / 平板 / 手机 | 1440、820、390、320 px 宽度无页面横向溢出；手机优先显示现有事务及最高优先级事件，不显示新的高风险动作 |
| 深色与 200% 文字 | 临时 dark + 32 px 根字号，1100 / 820 / 390 / 320 px 检查；修复长健康状态的顶栏换行、徽标和窄屏事件按钮后，健康详情与页面无横向溢出 |
| 键盘 | 搜索框 Tab 到组件按钮，观察到清晰的 2 px 实线焦点；所有域和组件选择使用原生按钮 |

临时 `app/layout.tsx` 已按 SHA-256 原样恢复。根字号测试不是浏览器缩放或完整读屏器验收；多浏览器与全站矩阵仍待完成。

## 工程检查与限制

- `pnpm test:ci`：145 项通过，其中 18 项健康模型检查覆盖规则、缺失 / TTL / generation、管理面影响隔离、原事务不变性、共享采集来源、诊断状态及 Event 去重 / 时间。
- `pnpm test:integration`：3 项通过，包括隔离 HTTP 服务和进程重启后原事务恢复。
- `pnpm typecheck`、`pnpm lint:ci`、`pnpm build` 通过。构建仍有已有 Vinext 路由静态分类 / 插件耗时提示，不是构建失败。
- 原型使用合成 Probe 与会话 RAM 状态；未连接真实 OVS、Go 双进程或生产数据库。刷新浏览器会重置本原型会话；P1 健康与本地 SQLite lab 的健康 / 事务尚未统一。
- 既有 Bond WebMCP 代表意图的名称 / 成员检查差异继续保留于后续工作，未在本批扩大修改范围。
- 浏览器 CI #7、通用模板 lint #6 保持开放。`.openai/hosting.json`、部署及站点访问设置未修改。

Batch 05 已获接受，下一批按计划进入 Capabilities。
