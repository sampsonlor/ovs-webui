# Public REST v1 技术审阅

日期：2026-09-14。任务 #33 / [PR #59](https://github.com/sampsonlor/ovs-webui/pull/59)。前置 #32 已接受合并并关闭，标签 `phase1-sqlite-repositories-v0.1`。用户已接受本批；PR #59 合并至 `9f641dde03756101bd56151686aed540057273d3`，已建立注释标签 `phase1-public-api-v1.0`，#33 已关闭并移入 Done。以下保留本批接受时的范围与证据，后续认证接线见 [#34 审阅](AUTH_GRANTS_v0.1.md)。

## 结果与边界

- [实现说明](../implementation/PUBLIC_API_v1.0.md)记录正式 Go 接线、权限与双库责任、协议预算和后续业务 gate。
- [OpenAPI](../../contracts/v1.openapi.json)、[不可改写的发布候选基线](../../contracts/releases/v1.0.0.openapi.json)、[58 项 Scope / 53 页覆盖](../../contracts/v1.coverage.json)保持一致：113 个路径、130 个操作。
- [长期执行证据](evidence/PUBLIC_API_v1.0.json)记录实际代码 head、GitHub 测试 merge SHA、原生架构、测试名称、OVS 结果及 artifact SHA256。artifact 到期后仍可核对测试摘要。

认证尚未接入，生产 `ClosedAuthority` 拒绝受保护业务操作；测试用权限与 SQL Gateway 仅存在于测试文件。`authentication_ready/configuration_ready` 仍为 false。本批不宣称真实用户管理、OVS 配置执行或完整 Svelte 页面已交付。

## 验证矩阵

| 范围 | 正常路径与异常证据 |
| --- | --- |
| 实际 HTTPS 服务 | OpenAPI/contract/runtime 可查询；401 为完整 Problem；TLS-only、root mgrd / 非 root webd、Unix peer 权限仍生效 |
| 严格公共请求 | 强 If-Match、未知字段、重复键、Unicode、路径编码、重复/未知 query、错误媒体、超大 body、Origin 与 Cookie CSRF |
| 接收与恢复 | HTTP→真实 SQLite→响应；重启后相同请求返回原回执/ETag，原 key 改前置条件拒绝，GET 原回执可恢复；撤权后拒绝 |
| 幂等时效/隔离 | 并发相同 key 只有一次变更；跨 principal/domain/资源/方法拒绝或隔离；epoch 改变、新键过期/未来与时钟回退；历史回执仍可读取 |
| 持久副作用 | SQL 拒绝全部回滚；202 必须已有同事务 Job；敏感输入 HMAC、一次性输出不落 receipt/WAL；完成回执保留与未解决记录保护 |
| 响应恢复安全 | 不确定提交无自动重试；错误 Job/epoch/资源回执无法附着到当前请求；无效服务数据不对外转发 |
| 分页 | 主体、权限版本、操作、过滤、排序、generation、snapshot、到期与签名改变均使 cursor 失效；无 offset 翻页 |
| WebSocket | 实际 TLS socket 的 Origin 拒绝、首帧 resync、sequence、counter 合并、撤权关闭、RPC 拒绝、overflow resync/关闭、连接与字节预算、退出清理 |
| 客户端扩展/乱序 | Go DTO 保留未知响应字段/状态且联合类型为 JSON；TS gap/重复/重连与 ResponseFence 防止迟到响应覆盖，未知状态禁用动作 |
| 兼容性 CI | 从 base commit 验证既有 release 文件不可改写；拒绝接口移除、输入收紧、字段类型改变、权限/域改变；可选字段/新接口可扩展 |
| 原生持久化与数据面 | amd64/arm64 Go vet/race、CGO-free、协议 fuzz、真实 2 MiB tmpfs 满盘与恢复、systemd 重启/权限/双库降级；OVS 3.3.9 转发及配置保持一致 |
| 原型回归 | 208 项回归、3 项进程集成、26 项浏览器测试；类型检查、lint 与生产构建 |

管理 identity/generation 保持 UUIDv4，与 UUIDv7 request_id 分开。API 幂等解决管理平面接收问题；OVSDB OutcomeUnknown 与 Applied/回滚证明继续由 #39/#40 验收，404/410 不能证明 Not Applied。

## 模式、设备与审阅决定

本批新增传输层，不新增原型页面或移动端高风险动作。Standard/Expert 继续使用同一权限和验证规则；Desktop 配置、Tablet 审阅/既有 Safe Apply、Mobile 事件处理的职责保留。现有浏览器验收负责原型响应式与键盘路径，本批接口清单不视为 53 页已全部上线。

技术实现与执行证据已获用户接受并完成合并；现进入 #34 的 mgrd Auth Grant 与每操作 Capability Enforcement。#19 继续作为独立库存原型审阅，不并入本项。
