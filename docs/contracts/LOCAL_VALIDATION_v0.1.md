# 服务端 Diff / Validation 本地联调 v0.1

本轮继续已批准的本地联调方向，在现有 Ports → VLAN → Candidate → Diff 页面接入
持久化验证任务。导航、Standard / Expert、按钮和状态组件沿用现有设计系统。
这是合成数据开发服务；生产认证、真实 OVS provider 和 Safe Apply 执行器尚未接入。

## 可审查路径

运行 `pnpm dev:lab --port 3001`，保留当前 `.ovs-lab/state.sqlite` 即可升级，无需重置 Candidate。

1. 选择 Editor A，在 server-07 保存一条 VLAN 修改，进入 Changes → Review diff →
   **Run validation**。服务端保存捕获的 Before / Proposed、请求记录和验证 Job。
   Pending / Running 由服务端推进；页面关闭后任务继续，重启后恢复未完成任务。
2. 默认安全能力未接入，因此可看到 VLAN 等配置检查通过，同时 checkpoint、probe、
   compare-before-rollback 阻塞。**Job succeeded 表示检查完成，不等于验证 Passed。**
3. 用 **Simulate observation → Safety available (synthetic fixture)**，重新验证，
   审查 Passed 路径。这个选项只模拟准备能力，既不创建 checkpoint，也不修改 OVS。
   `startSafeApply` 权限始终为 false，不能把模拟通过当作真实事务入场许可。
4. 保存新的 Candidate、模拟 Generation change / VLAN conflict / Validation policy
   change / Expire latest validation，旧结果应变为 Expired，原始 diff 保持不变。
   Stale / Conflict 继续使用现有三方选择与 rebase，再运行新验证。
5. Safety unknown / Safety unavailable / Provider unavailable 显示明确的未知或阻塞项。
   Provider 失败仍可读取已保存 Candidate 和验证证据，不把库存失败显示为真正无端口。
6. Revoke current editor permission 撤销当前编辑者权限。旧验证失效，新请求被拒绝；
   Standard / Expert 均相同。可切到另一个 Editor，选择 Restore provider / permissions /
   node 恢复测试条件；恢复权限不会复活旧验证。外部 VLAN 修改不会被该操作撤销。
7. Expert 额外显示 Candidate revision、generation、policy revision、requestId、jobId
   及逐项检查代码。Standard 使用相同结果和权限。平板/手机可审查已保存结果，桌面发起新验证。
   Request evidence 展示最新验证 Job 并能返回对应验证视图。

## 服务端与客户端约束

- POST `/api/v1/validations` 接受绑定 Candidate ID/revision/generation 的类型化请求。
  服务端在同一个 SQLite 事务中核对版本、权限与幂等键，保存捕获的 diff、policy revision、
  两分钟有效期、验证资源、Job 和原请求账本，然后返回 202。
- GET `/validations/{id}`、`/jobs/{id}` 和 `/requests/{id}` 都按当前用户隔离。
  Workspace 增加 `latestValidation`，Validation 增加 `nodeId` / `requestId`，用于恢复与
  绑定校验。接口仍是 v0.1 提案；生成类型、OpenAPI 和运行时校验器同步更新。
- 服务端工作器每次推进有界数量的 Pending / Running 记录；进度保存在 SQLite，
  不依赖浏览器轮询、页面倒计时或 GET 触发执行。当前检查是本地合成观测的有限步骤。
- 每次执行、读结果和恢复时核对版本、世代、策略、权限和服务端有效期。Expired 被保存，
  之后恢复能力、恢复权限或系统时间回退都不能使该记录重新 Passed。
- 任意 block / unknown 检查都会使结果 Blocked。SafetyPlan.available 只表示合成准备条件，
  尚未创建真实安全资源。确认窗口 90 秒与验证有效期两分钟均为开发参数，不是冻结的生产策略。
- Candidate、Validation 共用每用户 requestId 命名空间；改变内容或跨操作复用键返回 409。
  完全相同的重放返回最初接受响应，之后读资源获得最新进度，不制造新的验证或延长有效期。
- 应答丢失后客户端保留原 requestId 与原始版本，仅查询账本和对应 Validation；缺失、
  错对象或错版本证据保持 Unknown，不自动重复 POST。401/403 清除私有状态并关闭操作。
- 页面轮询服务端状态；VLAN 编辑时暂停自动刷新，避免覆盖尚未保存的输入。
  网络失败将结果标为上次读取的数据，不把缓存绿色状态当作当前证据。

## 验证与下一步

71 项自动测试通过，其中本轮增加 13 项验证持久化/HTTP 检查。覆盖排队和执行期间数据库
关闭重开、原始 diff 保留、幂等与用户隔离、服务端过期、Candidate/世代/策略变化、
Stale/Conflict/rebase、原生写权限、provider/安全能力阻塞、权限撤销、真实 loopback HTTP
工作器、丢失接受应答且没有第二次 POST、错版本与错 Job 拒绝。

生成文件一致性、TypeScript、修改范围 lint、生产构建与本地页面编译检查通过。
这些检查不替代浏览器交互验收、强杀进程的恢复测试、生产身份集成或真实 OVS 验证。

下一切片为持久化 Safe Apply 事务与服务端确认/回滚调度；继续本地合成执行器联调，
再通过 provider 边界接真实 checkpoint、probe、compare-before-rollback 与再认证。
真实安全资源接入之前，不能开放对真实设备的 Apply。
