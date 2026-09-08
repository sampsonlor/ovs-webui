# GitHub 开发与自动测试基线 v0.1

现有原型、Design System 和本地 Candidate/Validation 联调作为基线。
2026-09-08 扩展全量 lint 和浏览器回归，处理议题 #6、#7；P1 批次顺序保持原基线。

## 每次提交的检查

`.github/workflows/ci.yml` 在指向 main 的 PR、main/ci 分支推送和手动触发时运行。
工具链固定为 Node 24.19.0 / pnpm 11.19.0；依赖必须按现有 lockfile 安装，Actions
固定到已核对的提交。默认 GITHUB_TOKEN 只读代码，不保留 Git checkout 凭据。

| 检查                             | 执行内容                                                                                 |
| -------------------------------- | ---------------------------------------------------------------------------------------- |
| Quality and build                | TypeScript、全仓库 lint、契约生成一致性、完整回归测试、生产构建                        |
| Isolated integration environment | 独立服务进程、真实 HTTP、数据库隔离、权限和安全能力、强杀后的会话/Candidate/验证任务恢复 |
| Browser workflow and UI templates | Chromium 中的 Ports / VLAN → Candidate → Diff / Validation、刷新恢复、安全异常及通用组件交互 |
| CI Gate                          | 以上三项必须全部成功；失败、取消或跳过均不能满足合并门禁                                     |

测试输出为 JUnit，作为 Actions artifact 保留 7 天。浏览器失败截图、可访问性快照和
操作 / 断言追踪也保留 7 天。追踪关闭 DOM / 网络快照和源码附件，避免记录 Cookie、
CSRF 等会话凭据；数据库、浏览器 profile、storageState 和服务运行目录不上传。
每次运行有超时；同一 PR 的新提交替换旧运行。

## 自动测试环境

GitHub Environment 名称为 `ci-integration`。每次运行使用 GitHub 提供的临时
Ubuntu 24.04 runner，测试服务仅监听 runner 自己的 loopback 随机端口。
每个场景创建独立 `ovs-ci-*` 临时目录。测试结束后停止自己创建的进程并清理自己的数据，
不访问开发者的 `.ovs-lab/state.sqlite`。第二个干净实例验证不同运行之间没有状态残留。

环境不配置生产凭据；所有 Port、角色和安全能力均为合成数据。Environment 关联采用
`deployment: false`，不把一次测试运行记为已部署网站。它是自动测试环境，并非长期
在线 Staging。共享页面验收与真实 OVS Linux 环境仍是后续独立交付项。

运行同一批检查：

```sh
pnpm install --frozen-lockfile
pnpm typecheck
pnpm lint:ci
pnpm test:ci
pnpm test:integration
pnpm exec playwright install --with-deps chromium
pnpm test:browser
pnpm build
```

`test:integration` 启动测试专用服务入口，要求位于系统临时根目录内的独立目录。
服务复用当前 HTTP middleware、SQLite store 和验证 worker，测试没有替换这些实现。
测试通过不代表真实 OVS rollback、设备断连或生产认证已通过验收。

`test:browser` 使用锁定的 Playwright 1.63.0。每个 worker 启动应用本身的 Vite
配置和真实 lab HTTP middleware / SQLite store / validation / transaction worker，
仅监听随机 loopback 端口。每项测试创建新的数据库和浏览器上下文；数据库重置
只通过测试父子进程的私有 IPC 完成，没有 HTTP 重置入口。结束时先停止服务，
校验临时目录确实位于系统临时根目录且名称为 `ovs-browser-*`，再清理测试数据。
开发者的 `.ovs-lab` 和浏览器登录状态不参与测试。

浏览器检查使用角色、名称和标签定位，并等待可见状态或操作条件。网络失败用例
让真实服务接受请求后丢弃响应，再通过原请求恢复；不会用假成功响应替代服务。
当前覆盖桌面 Chromium、820px 平板和 390px 移动端的相关职责；Firefox、WebKit、
整站视觉对比、真实 OVS provider 和正式 Go / Svelte 服务仍是独立验收范围。

## main 门禁与项目管理

先获得实际成功的云端运行，再把 `CI Gate` 设置为 main 的必要检查并要求 PR 合并。
单维护者阶段不要求另一个人批准自己的 PR；要求对话处理完成，禁止强推与删除 main。
失败的测试修复后重跑，不通过关闭检查或改变预期结果取得绿色状态。

项目看板关联仓库、Issue 和 PR。保持现有 Phase 1 顺序，并追踪 Core API / Safe Apply、
测试与基础设施、UI 验收等工作。当前 PR 进入审查状态；未完成能力保留明确验收标准。

`pnpm lint:ci` 与 `pnpm lint` 执行同一套全仓库规则，包含通用 `components/ui`、
`hooks/use-mobile`、构建配置及浏览器测试。原有 19 处模板错误已在组件实现中修复，
没有关闭规则或新增排除。Node 测试注册显式使用 `void test(...)`：执行、等待和
失败汇总仍由 Node test runner 负责。自动生成代码继续由契约一致性检查验证。

参考：[pnpm setup](https://github.com/pnpm/setup)、
[GitHub 测试环境关联](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/control-deployments)。
浏览器追踪选项见 [Playwright TestOptions](https://playwright.dev/docs/api/class-testoptions#test-options-trace)，
CI 浏览器安装见 [Playwright CI](https://playwright.dev/docs/ci)。
