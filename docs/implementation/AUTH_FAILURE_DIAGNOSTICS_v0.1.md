# 正式认证失败诊断 v0.1

2026-10-08 · #71 诊断子批次 M1。以已接受 E8 为基线；历史登录异常及会话读取 503 的根因仍待确定，完整 #71 不因本批验收而关闭。

## 已知证据与交付边界

[36402095950](https://github.com/sampsonlor/ovs-webui/actions/runs/36402095950) 的 arm64 后续登录停留于 Sign in，缺少当时 POST/GET 状态；[37331755262 attempt 1](https://github.com/sampsonlor/ovs-webui/actions/runs/37331755262/attempts/1) 的失败发生于已登录、等待 Safe Apply 确认期间，测试助手读取 session 返回 503，同刻 mgrd 仅记录 AUTH_UNAVAILABLE。两种现象尚无同因证明。原始与复测证据分别保留。

本批交付固定词汇的私有 IPC 日志、覆盖整个正式浏览器测试的受限请求摘要，以及分类、脱敏、原生 Unix 传输和真实登录/退出回归。它能为下一次失败提供分层证据，不宣称已定位或修复历史根因。

## 私有日志

mgrd 的 ipc_request_rejected 保留原公开 code，增加以下字段：

| 字段 | 来源与含义 |
| --- | --- |
| operation | 编译内的 IPC 操作名；未知路径只记 unknown |
| phase | protocol、queue 或 operation；表示拒绝边界，不伪装为底层 SQL 执行阶段 |
| error_class | errors.Is/As 派生的固定分类：存储 busy/canceled/unavailable/not-found/commit-unknown，调用取消/期限、IPC 队列满、domain rejection、transport timeout 或 unclassified |
| request_state | 记录时当前请求为 active、canceled 或 deadline_exceeded；与底层错误类别独立 |

分类识别 wrapped errors，但不输出 Error()、SQL、URI、请求体、账号、grant、Cookie 或 CSRF。生产统一脱敏器只允许这四个字段的编译内固定值，其余字符串、类型和 group 仍被遮蔽；handler 和 Unix 回归均经过生产脱敏器。公开状态码、响应结构、Connection close 和队列 Retry-After 保持原行为。新增字段不更改授权判断、状态转换、存储降级或未知提交处置；unclassified 表示仍需进一步调查。

## 浏览器与助手摘要

正式 Playwright 流程从每个测试开始观察默认 context，并在额外 context 的登录/助手查询前接入。每个测试在 afterEach 写入 auth-diagnostics.json；现有 frontend artifact 目录保留它，失败测试同样执行保留。

只识别同源 create-session、read-session、read-workspace、read-ports。browser 与 helper 分开标记，helper 包括登录后 command/get 中的 session 查询。按请求开始顺序编号，响应可逆序完成；pending、network_error 和实际 response 保持不同。每个 context 仅保留最新 64 条及 observed/dropped 数，旧请求完成不能改写新条目。缺失与丢弃明确保留，不据摘要猜测未观察到的成功。

持久内容只有固定操作、来源、序号、状态、白名单错误码和容量信息。成功响应不为诊断读取；错误只提取白名单 code，其余记 OTHER_ERROR。原始 URL/query、headers、payload、账户、cookie、CSRF、错误文本和 HAR/trace 均不写入。日志与浏览器摘要只能结合各自观察顺序和运行范围分析，不能视作跨进程原子快照或完整因果追踪。

## 验收边界

本地执行完整契约/单元、类型、lint、集成、Go scoped packages、Linux 双架构 vet/build 与完整 pnpm build。Windows 不提供 Linux Unix socket 或真实 OVS 验收；最终接受仍须最终 PR 与同树 main 各自通过完整六项 CI，并核对双架构原始报告和诊断文件。

分类测试在 IPC handler 边界注入已定义的错误，并验证公开响应及脱敏；原生 Linux 测试再验证真实 Unix transport 的私有分类与远端公开错误。它们不构成历史故障或真实 SQLite 争用的复现。正式浏览器新增真实 Go 登录、Cookie 入场、helper 查询、退出后的 401 检查；既有撤权、丢回执、安全期限及 native 恢复矩阵全部保留。

接受标签为 phase1-auth-failure-diagnostics-v0.1。完整 #71 保持 Open / In Progress，下一阶段根据新的失败证据定位实际原因，再实施相应修复及针对性回归。
