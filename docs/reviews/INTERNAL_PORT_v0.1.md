# #42 C2：独立 internal Port 审阅 v0.1

日期：2026-09-28。[实现边界](../implementation/INTERNAL_PORT_v0.1.md)。本记录列出验收要求；最终结果以对应 PR/main CI 和接受标签 `phase1-internal-port-v0.1` 为准，未通过前不视为接受。

| 责任 | 验收证据 |
| --- | --- |
| 正常创建 | 真实既有 Bridge → 独立授权 → 草稿/Diff/Validation → Safe Apply → Applied/probe → 显式确认；暂存无原生写入，access VLAN 正确 |
| 原有对象 | Bridge/local Port/local Interface 和其他成员身份不变，仅加入两行；回滚准确清理且不删除父图 |
| 并发变化 | 名称抢占、父配置/成员改变在原子事务内拒绝；补偿晚到成员、Mirror 弱引用及未监控 policing 变化均拒绝 |
| 权限及身份 | 独立根目标/能力；旧 credential ceiling 不扩大；撤权补偿；旧 UUID tombstone，同名重新创建新身份 |
| 未知结果 | 创建回复丢失后重启、清理回复丢失；无重复派发，无推测 target/Applied，保留恢复保护 |
| Linux 内核 | 实际 internal 接口创建/清理，父接口保留；新接口主机地址阻止清理 |
| 正式页面 | 真实 API 暂存；Standard/Expert 同一 Diff，Expert 身份可审阅；平板/手机禁用新验证；回滚后新 Port 404、父 Bridge 仍可读 |
| 完整回归 | 三份 schema，amd64/arm64 原生执行；所有 Go race、既有 VLAN/Bond/Bridge 安全矩阵、真实管理网络、存储/认证/TLS/服务、契约、前端构建与原型浏览器 |

`TestNativeInternalPort` 每 schema 14 个叶子场景（每架构 42 个），报告 `go-internal-port-{schema}.json` 随 runtime artifacts 保留。正式浏览器共 10 个场景，新截图 `frontend-evidence/internal-port-*.png`，不留存凭据、cookie、CSRF 或网络 trace。

本批不关闭 #42/#54；一般端口删除和物理接口生命周期继续独立交付。#71 登录间歇失败仍单独跟踪，当前 CI 成功不足以证明其根因已修复。原始 DOCX、部署配置及站点访问不在本批修改范围。
