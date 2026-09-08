# 全量 lint 与浏览器 CI 验收 v0.1

日期：2026-09-08。对应议题 [#6](https://github.com/sampsonlor/ovs-webui/issues/6)、
[#7](https://github.com/sampsonlor/ovs-webui/issues/7)。Disposition：2026-09-08 用户接受，经
[PR #16](https://github.com/sampsonlor/ovs-webui/pull/16) 合并；接受检查点为 `prototype-ci-browser-v0.1`。
基础提交为已接受的 System Health `b08e0a3`；本批属于工程回归保护，不接受新的 P1 功能。

## 实现

- `lint:ci` 改为完整 `pnpm lint`，通用 UI、hook、配置、服务和测试采用同一规则。
  原有 19 处错误在实现中修复，没有关闭规则或扩大忽略范围。
- Button / Input / Field 分组采用原生语义；输入附加区通过 label / htmlFor 支持聚焦，
  Command 和 Combobox 保留自定义 ID；分页保持链接的原生键盘导航与现有按钮样式。
  当前面包屑项保留 aria-current，装饰性 OTP 分隔符不再声明交互语义。
  Spinner 使用可访问状态容器，Chart 明确处理系列键的类型。
- Carousel 和移动断点通过 `useSyncExternalStore` 订阅外部状态，保留 SSR 初始值，
  同时清理 select / reInit / media-query 订阅。Node 测试注册显式交给 test runner 等待和汇总。
- 锁定 Playwright 1.63.0；新增浏览器 job 并纳入现有 `CI Gate`，任一检查失败、取消或跳过均阻止通过。
- 三个测试 job 均显式执行冻结 lockfile 的依赖安装；工具链 Action 只负责固定 pnpm / Node。

## 浏览器覆盖

| 路径 | 必须观察的结果 |
| --- | --- |
| Ports → VLAN → Candidate → Diff / Validation | Candidate 和验证刷新恢复；保存意图后 running VLAN 仍为原值 |
| Standard / Expert | 信息深度改变，验证结果与权限不改变 |
| Read-only | 两种模式都无法编辑、丢弃或验证；刷新后仍为只读 |
| Stale | generation 变化令旧验证过期；显式 rebase 后重新验证 |
| Conflict | 同屏 Base / Current / Mine；显式选择后仅改变 Candidate |
| Candidate OutcomeUnknown | 服务持久化后丢弃应答；禁止新写入，通过原 requestId 恢复 |
| Provider / permission | 不可用检查阻止 Safe Apply，权限撤回后保留证据并禁用动作 |
| Safe Apply OutcomeUnknown | 刷新保留原请求恢复线索；恢复同一事务，不生成第二个命令 |
| Drift | 有操作理由仍禁止确认和直接回滚；检查既有证据并保持节点锁，外部 VLAN 保留 |
| 平板 / 手机 | 保留既有 Candidate 的审阅，隐藏验证 / 丢弃动作 |
| 通用模板 | label 聚焦、附加按钮键盘操作、分页链接、list / status、OTP、Chart |
| 外部订阅及组合控件 | Carousel 首尾 / reInit、767 / 768px 断点、Command / Combobox 键盘操作 |

测试使用实际页面的角色、名称和标签定位，等待状态而非固定 sleep。每项测试使用全新
SQLite 数据库和浏览器上下文；Vite 服务及 worker 复用已存在的 lab 实现。
应答丢失由测试进程的私有 IPC 控制，仅丢弃真实成功应答；浏览器网络栈重发同一幂等请求
时按唯一 requestId 计数，避免把传输重试误当作另一次业务命令。

## 证据与范围

本地验证通过：`pnpm typecheck`、全量 `pnpm lint:ci`、145 项回归测试、3 项进程 / HTTP
集成测试、12 项 Chromium 浏览器测试及 `pnpm build`。通用模板另做浅 / 深色截图复核，
测试专用入口采用应用的 Arial / Helvetica 系统字体回退，不依赖远程字体加载。
实现提交 `3009dcd` 的云端四项门禁全部通过；接受记录提交的检查结果见关联 PR 的 Checks。

浏览器附件限于失败截图、可访问性
快照和操作 / 断言追踪，保留 7 天；不上传数据库、浏览器 profile、storageState、源码包
或网络会话凭据。追踪禁用 DOM / 网络快照，已在本地失败样本中检查该边界。

自动化范围为合成 lab 的 Chromium 工作流及受影响模板交互；并不替代整站视觉对比、
Firefox / WebKit、真实 Linux OVS、生产身份认证或正式 Go / Svelte 的 Release Gate。
后续继续 Capabilities；既有用户文档、托管配置与站点访问保持原状。
