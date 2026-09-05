# Ports / Candidate 本地持久化联调 v0.1

用户确认目前没有后端服务，先完成本地持久化联调。本轮将现有 HTTP 适配层接到
同一个应用中的开发服务，Ports / Port Detail / VLAN Edit / Changes / Diff 沿用
原有导航、组件和视觉语言。默认 `pnpm dev` 保持原型演示。

## 运行与评审

在当前仓库运行 `pnpm dev:lab --port 3001`，打开启动日志中的 Local 地址。
Node 需支持内置 SQLite，项目最低版本为 22.13；本轮使用 Node 24.19。
本地模式只绑定 loopback，服务端同时校验连接来源和 Host。

1. 选择 **Editor A**，从 Ports 进入 server-07，修改 VLAN 并保存到 Candidate。
2. 在 Changes / Diff 检查 base 和 mine。刷新页面，或关闭并重新启动服务，
   再选择 Editor A；同一份 Candidate 及 request 账本仍在服务端。
3. 选择 **Editor B**，应看到独立的空 Candidate；切回 Editor A 可恢复其修改。
   **Read-only** 能查看本角色的数据，不能保存、discard、rebase 或调整观察场景。
4. 同一用户打开两个标签页，以同一 ETag 编辑；第二次旧版本提交返回 412，
   不会覆盖第一次。刷新后需再次审查。页面重新获得焦点时会先核对会话并恢复最新状态。
5. 保存一条意图后，在 **Simulate observation** 选择 Generation change 或 VLAN
   conflict。Stale 必须 rebase；Conflict 展示 base/current/mine 并要求明确选择。
   若选择后外部 generation 又变化，提交会再次被拒绝。
6. Provider unavailable 保留错误与未知语义；Unresolved node operation 阻止新写入。
   Restore provider / permissions / node 恢复观察条件，不撤销外部 VLAN 修改。

页面保存成功只表示 Candidate 写入本地服务。后续已接入的服务端 Diff/Validation 见
[验证联调](LOCAL_VALIDATION_v0.1.md)。本地模式仍不提供真实 OVS 修改、Safe Apply 调度
或确认/回滚；联调模式在相应入口明确显示未接入，且禁用原型
WebMCP 配置工具，避免服务端 Candidate 与内存模拟事务混用。其他 P1 页面保留演示数据。

## 实现边界

- SQLite 在 `.ovs-lab/state.sqlite`；Candidate 与 request 账本在同一事务内提交。
  WAL 和 busy timeout 支持并发连接；每用户 Candidate 主键与每用户 requestId
  联合主键形成数据隔离。运行数据、Cookie、CSRF、SQLite sidecar 全部留在忽略目录。
- 两个编辑者和一个只读演示角色由开发会话提供。会话是随机 Cookie，带 HttpOnly、
  SameSite=Strict 和过期时间；CSRF 和 epoch 绑定当前会话。登录选择器不属于生产认证。
- 该服务没有被部署到 Sites，也不把 SQLite 选型宣布为已冻结生产架构。
  Vite 只在显式开发模式加载服务插件；普通生产构建不注册 `/__ovs_lab/*` 或数据库服务。
- 浏览器使用生成的 JSON Schema 校验器、固定同源 API、每次保存唯一 requestId、
  If-Match 和 CSRF。请求失败不自动重放；Unknown 通过原请求账本及最新 Candidate 恢复。
- 会话变更先清空私有客户端状态；失效的 controller 拒绝晚到响应。分页必须保持同一
  snapshotId、generation、节点及唯一 Port ID；混合快照会失败并要求刷新。
- ETag 覆盖完整 Candidate 表示，包括外部 freshness。revision/generation 都是不透明
  equality token；浏览器不按数字解析、排序或递增。
- Standard / Expert 只切换字段深度；新配置意图继续限桌面操作。表格状态数取自实际
  API 返回值，Unknown 不转换为 Down，缺失速度不显示为零。

## 验证结果

58 项自动测试通过，包括新增 12 项持久化与 HTTP 联调测试。覆盖数据库关闭重开恢复、
独立连接并发、幂等重放与 key 冲突、用户隔离、只读角色、三方 rebase race、provider
失败、节点锁、真实 loopback HTTP、丢失保存应答后不重复 PATCH、会话失效、CSRF、
分页失效与晚到响应隔离。生成文件一致性、TypeScript、范围内 lint 和生产构建均已通过。

这证明本地 SQLite/HTTP 链路的行为，尚未执行浏览器交互验收、进程强杀故障恢复、真实
认证集成或真实 OVS 测试。预览只做了页面编译和 HTTP 成功响应检查。

本页保留 Candidate 持久化切片的验收记录。下一切片的版本/世代/策略绑定、验证过期
及原生能力检查已记录在 [LOCAL_VALIDATION_v0.1.md](LOCAL_VALIDATION_v0.1.md)。
