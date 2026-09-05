# Core HTTP adapter v0.1

本轮在已合并的 `integrated-prototype-v0.1` 之上实现第一段 HTTP 接口边界，入口为
`lib/api/http-client.ts`。默认原型保持演示模式；`pnpm dev:lab` 已将 Ports / Candidate
接到本地持久化服务，见 [本地联调说明](LOCAL_PERSISTENCE_v0.1.md)。

## 已实现

- Workspace 启动读取；Ports 分页、搜索、单对象读取；Candidate 读取及强 ETag 更新。
- 接受必填 JSON Schema 校验器，每个入站响应和出站命令按现有生成契约校验。
  调用方必须将 schema 名绑定到 `contracts/core-v0.1.mjs` 的对应 schema；不能以
  TypeScript 强制转换或恒真函数替代运行时校验。已生成不使用运行时 eval 的浏览器
  校验器，并在本地模式接入。校验器随契约生成和一致性检查更新。
- 固定同源 `/api/v1`、会话 Cookie、CSRF、`no-store` 和禁止跳转；不接受客户端
  `userId`、Expert 权限、任意 OVS 命令或隐式替换后的 ETag。
- 校验节点、对象身份和单页 generation 一致性；保留空、部分可用、权限失败和
  Provider 不可用的区别。分页调用方还须核对跨页 `snapshotId`，不能混入新快照。
- Candidate PATCH 只发送一次。412 / Conflict 不自动 rebase；网络中断、无效应答
  或身份不匹配返回带原始 requestId 的 Unknown，供读取账本和 Candidate 后人工处理。
- Safe Apply 的 POST、原 requestId 账本读取和原 transaction 读取已实现
  `ChangeControlGateway`，可复用之前完成的提交和恢复逻辑；确认/回滚写入尚未接线。

## 验证

接口测试使用现有 JSON Schema 校验器，包含一次真实 loopback HTTP 读取和受控传输
故障注入。覆盖版本标记、CSRF、幂等键、412、401/403/503、错误节点/对象/世代、
失去应答、超时、404 不证明未提交，以及找回 Safe Apply 时不重复 POST。

这些测试没有连接真实 OVS，也没有实现或证明用户身份系统、磁盘持久化或服务重启恢复。
测试服务器和响应仅是合成数据。执行 `pnpm test`、类型检查及构建以复核这一边界。

原 HTTP 切片验证结果为 46 项测试；本地持久化切片的新增验证记录见联调说明。

## 下一接线门禁

需由已批准的服务端提供会话和 CSRF 来源、节点绑定、每用户持久 Candidate、强 ETag
语义与持久请求账本。随后将 Ports 和 Candidate 页面切换到该客户端，处理启动未决请求、
退出登录后的缓存清理、分页快照、并发读取与真实权限变化；后端未就绪时继续清楚标注原型。
以上基础接线已在后续本地切片完成；Safe Apply 决策、核对 Job、证据和恢复见
[Safe Apply 联调](LOCAL_SAFE_APPLY_v0.1.md)。Bridge/Bond 原生写入类型与真实设备 watchdog
仍需后续 provider 集成。
