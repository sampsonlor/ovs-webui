# #42 C3：独立 internal Port 删除审阅 v0.1

日期：2026-09-29。[实现边界](../implementation/INTERNAL_PORT_DELETE_v0.1.md)。本记录列出验收要求；最终接受以对应 PR、main 完整 CI 和注释标签 `phase1-internal-port-delete-v0.1` 为准，未通过前不视为接受。

| 责任 | 验收证据 |
| --- | --- |
| 正常删除 | 先由真实 C2 流程创建并确认，再独立授权删除；暂存不修改 OVS，Applied 后确认；原两行和未用恢复身份均退休 |
| 补偿恢复 | 两个新身份恢复同名、同 VLAN 的 access Port/internal Interface；父 Bridge/local 身份及原成员不变，原身份永不复用 |
| 并发删除 | 晚到父成员、父配置、access VLAN、Mirror 弱引用及未监控 policing 改变，均由同一原生事务拒绝 |
| 并发恢复 | 同名抢占、父成员与 local Port 配置改变阻止恢复，保留六项保护，不覆盖外部对象 |
| 权限与所有权 | 私有两行所有权、独立根目标与能力；伪造原生标记/部分所有权拒绝；旧 credential ceiling 不扩大；撤权触发补偿 |
| 未知结果 | 删除回复丢失后重启、补偿回复丢失；不重发、不猜测 target/Applied，恢复身份保持 unverified |
| Linux 内核 | 真正删除并恢复 internal 网卡；主机地址阻止删除，父对象始终保留 |
| 正式前端 | 真实 API 暂存 → Standard/Expert 同一 Diff → Safe Apply → 两项身份映射；未完成不出现恢复链接，完成后新 Port 可打开、旧 Port 404 |
| 响应式 | 桌面配置，900px 平板与 390px 手机禁用新验证；无页面横向溢出，窄屏可审阅共享 Diff |
| 完整回归 | amd64/arm64、三份 schema、原有 Bridge/Port/Bond/VLAN/Safe Apply、真实管理网络与存储故障、认证/TLS、全部 Go race、契约与构建、原型浏览器 |

`TestNativeInternalPortDeletion` 每 schema 17 个叶子场景，每架构 51 个；报告 `go-internal-port-delete-{schema}.json`。正式服务浏览器增加一项端到端场景，总计 11 项，截图 `frontend-evidence/internal-port-delete-*.png`。不留存凭据、cookie、CSRF 或网络 trace。

本批保留 #42/#54 的剩余范围及 #20–#28 的独立功能验收；#71 间歇登录失败继续单独跟踪，不能因一次 CI 成功判定根因已解决。最终 PR/main 运行编号和接受提交记录于 #42 及注释标签，原始 DOCX 和站点访问未修改。
