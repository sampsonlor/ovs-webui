# 正式认证失败诊断审阅 v0.1

2026-10-08 · #71 M1，范围见[实现说明](../implementation/AUTH_FAILURE_DIAGNOSTICS_v0.1.md)。接受须最终 PR/main 的完整 CI、原始 artifacts 和下表证据，不以复测通过推断历史根因修复。

| 范围 | 要求与证据 |
| --- | --- |
| 正常入场与退出 | 真实 Go/OVS 正式浏览器：匿名/helper 401、POST 201、browser/helper session 200、退出后 401；原授权与 Cookie 策略保留 |
| 已登录后失败 | 全测试期间捕获 browser 与 helper 的会话查询；明确请求顺序、来源、状态及白名单错误码 |
| 原因分类 | 确定性注入 wrapped 存储 busy/canceled/commit-unknown、撤销对应的 domain rejection、未知错误；保持既有公开响应 |
| 队列与取消 | 队列阶段、当前请求取消状态与底层类别独立；429/Retry-After、504 及预算保持 |
| 隐私与容量 | 密码/Cookie/CSRF/私有错误文本/URI/query 不进入摘要或日志；经过生产统一脱敏器验证固定词汇可见、任意值/类型/group 被遮蔽；64 条滚动边界、dropped、旧/重复完成、pending 与 network_error 回归 |
| Linux IPC | 真实 Unix socket 传播原有 503/AUTH_UNAVAILABLE，私有日志保留 security.read/storage_busy，credential 不泄露；注入 typed error 不等于真实存储争用复现 |
| 设备与页面 | 产品页面、Standard/Expert 权限和设备职责未修改；既有完整浏览器/原生异常矩阵继续执行，既有视觉基线须与本批生产资源保持一致 |
| 接受 | 完整 PR/main 六项 CI、各五组 artifacts 下载及 digest 校验、逐份报告和每个正式测试诊断结构核验；同树 main、annotated tag、分支清理 |

## Review disposition

M1 的最终 disposition 和准确 CI/head/artifact 证据记录于 phase1-auth-failure-diagnostics-v0.1 的 annotated tag 及 #71 的诊断子批次记录。标签创建前不视作已接受。#71 的历史根因验收项保持未完成；#21/#42 等功能范围继续各自验收。没有增加登录重试、放宽权限/限流、延长会话或跳过既有场景。
