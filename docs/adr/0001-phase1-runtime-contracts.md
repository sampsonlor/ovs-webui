# ADR 0001 Phase 1 运行时与协议选择

日期：2026-09-09。状态：Accepted for implementation。关联 #30 / 已合并 PR #56，接受标签 `phase1-implementation-design-v0.1`。未修改批准 Architecture v1.0.1 的产品或安全边界；版本窗口仍需后续资格测试。

## 背景

已接受原型使用 React/Vinext 与 Node SQLite lab。批准架构规定正式 Go 双进程、Svelte 静态前端、双库权威分离和 OVS 原生语义，需要把驱动、wire format、超时和版本窗口具体化。Scope 原文已经归档，其正文状态为 Draft for Review。

## 决定

采用 Go 1.27.1、`modernc.org/sqlite v1.58.0` 的 CGO-free Repository Adapter，migration 为校验和保护的 embedded 顺序 SQL。WAL/FULL、每库单 writer、有界 reader；不建立跨库事务协调器。密码、roles、grants 和 privileged journal 位于 manager.db。sqlite driver 与其 libc 依赖按模块自身要求锁定，不单独随意覆盖传递版本。

webd/mgrd 的 IPC 采用 Unix socket 上 HTTP/1.1 + typed JSON。标准 Go HTTP framing、context、超时和 body 限制可复用；协议自身仍必须实现 peer UID 校验、握手、operation registry、拒绝重复/未知请求字段、grant enforcement 与 fuzz 测试。初版只接受相同 release/schema digest 的 daemon 组合。公共 API 使用 OpenAPI 3.1.1 与 snake_case，公开 API major 与私有 IPC major 独立。

采用字段/语义依赖范围的保护与 OVSDB `wait` CAS。lab 全节点互斥不进入正式实现。默认 30 秒 Applied 等待与 120 秒确认窗分离，确认从 Applied 和 Health 证据建立后开始。具体表、身份和恢复规则见[实现设计](../implementation/PHASE1_IMPLEMENTATION_DESIGN_v0.1.md)。

OVS 初始 qualification 窗口为 3.3.9、3.7.1、4.0.0，功能以实际 schema/capability 判断；正式支持声明必须等真实矩阵通过。不同 minor/major 引入的未知字段继续保留。后续补丁更新通过普通依赖 PR 和回归，不把版本字符串当能力授权。

## 替代方案和取舍

| 方案 | 处置 | 原因 |
| --- | --- | --- |
| CGO SQLite driver | 本版不选 | 批准架构优先 CGO-free，双架构打包减少本地编译依赖；若现代驱动不能满足恢复/性能要求，再用证据提出 ADR |
| gRPC/Protobuf IPC | 本版不选 | 两个固定进程的接口规模有限；当前不引入 protobuf 工具链和多套错误模型。JSON 的严格解码成本由显式 decoder/fuzz 承担 |
| 任意 JSON-RPC/命令代理 | 不接受 | 会破坏 typed operation 和特权攻击面边界 |
| 两进程共写单个 SQLite | 不接受 | webd 可写安全状态将破坏认证权威；跨库最终一致性需要显式 handoff，不能以共享 DB 消除设计问题 |
| 原样沿用 lab 契约 | 不接受为生产契约 | 缺强 identity、真实权限、原生 VLAN、未知响应兼容与正式 generation；保留 lab 作为语义回归参考 |
| 独立第三个 watchdog daemon | 本版不选 | 架构第一版规定两个 daemon；mgrd safety scheduler 与 systemd supervision 共同恢复 |

## 验证与变更控制

#31 验证 IPC framing/version/peer/超时和双架构；#32 验证 WAL、故障、迁移、备份和恢复；#34 验证授权不变量；#39/#40 验证真实 CAS、OutcomeUnknown、Applied 和 deadline races；#51 验证完整系统矩阵。任何上列失败都需要修复或有记录的设计变更，不能把未测试的目标标成正式支持。

来源：[Go release history](https://go.dev/doc/devel/release)、[SQLite driver](https://pkg.go.dev/modernc.org/sqlite@v1.58.0)、[SQLite WAL](https://www.sqlite.org/wal.html)、[OVS releases](https://www.openvswitch.org/download/)、[OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)。
