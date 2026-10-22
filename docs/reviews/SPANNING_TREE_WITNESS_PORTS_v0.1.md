# P2b OVS 3.7.1 / 4.0.0 只读扩展审阅

核对两份官方固定提交的实际源码；补丁分别生成并验证全部修改前文件。STP/RSTP 本地字段仍由各自协议库的 mutex 保护，ofproto 身份在创建时生成；bridge 配置周期及 IDL authority 事件保持原有饱和计数和全失败拒绝语义。OVS 4.0 的 schema 驱动 condition removal/reset 路径独立推进 authority epoch，并有不连接远端的 C 单元验证。没有新增写操作、锁操作、后台线程、生产调用者或默认启用入口。

每版实际 schema 的 52 个输入字段与共享原子类型/基数一致；3.7.1 / 4.0.0 schema 相同不代表构建身份相同。Go 仅精确匹配三个编译档案，组合预检将档案 schema 与当前数据库 native schema 关联，并要求两个样本的构建/schema 身份一致。未知版本/档案、架构或变体拒绝。两版实际 C ABI、生命周期及 wire 输出须由完整原生矩阵证明。

验证：本机只执行 Go 及补丁/注册表静态检查，Linux 的实际三构建、kernel namespace、sanitizer 和完整组合场景由 GitHub 原生 amd64/arm64 CI 执行。完整源码与 main CI、官方 artifact 摘要及失败历史必须在交付记录中核对；不能将 Windows 交叉编译当作 Linux 运行通过。

没有前端/public API 改动，未声称新的人工截图验收。生产安装、签名/可复现供应链、动态依赖、最终密封执行和恢复资格仍未完成；预检不授予写权限。父任务 #23 / #43 不关闭，#71 不修改。
