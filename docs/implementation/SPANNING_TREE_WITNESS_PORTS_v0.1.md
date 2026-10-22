# STP/RSTP witness P2b：实际版本移植

范围：OVS 3.7.1 和 4.0.0 的可选只读扩展及原生 amd64/arm64 隔离资格测试，保留 3.3.9 回归。关联 #23 / #43。运行记录以对应 PR 的完整源码与 main CI 为准；未经完整通过的组合不能声明已支持。未部署，无生产调用者，未开放写入。

| 版本 | 固定官方提交 | 上游归档 SHA-256 |
| --- | --- | --- |
| 3.3.9 | `1a3fefbcd0b0f70ced621b92e286fef3dffaac09` | `64e2d425eb34cafc405b641ca6ecacf2453ee37b9aa50b4b17dfb88aa9e7a52e` |
| 3.7.1 | `7921d9c6924b8934ea1de9481891ac1172649280` | `d2496304f92f4a423534424a92579db8656aec1780590de8bf0deb8f7e8a7e9f` |
| 4.0.0 | `b89dc80f0143f5bf3f58c48d54e82cc7f8d29da8` | `b51a8497fa4b93bf446345d87f4806c394bb5babcdb512aa62112aa6fff61dbd` |

每个版本有独立的 `extensions/ovs-witness/<version>/`：修改前的逐文件摘要、对应补丁、三个 overlay 文件和固定输入列清单。应用前验证归档、原文件和 overlay 目标；不能将旧版本补丁应用成功等同于新版本资格。原生 CI 分别运行默认、显式启用、ASan+UBSan 三种实际源码构建。默认构建必须没有扩展命令。

移植核对范围包括 bridge 配置开始/完成/销毁、同名 UUID 替换、STP/RSTP setter 返回路径、ofproto 生命周期、两协议库的本地参数及互斥锁、IDL FSM/monitor/条件更新/锁事件、Unixctl 主线程调用顺序。4.0.0 新增的 clear-condition/reset-last-id 接口额外推进 authority epoch，直接覆盖尚未发出下次 monitor 请求的失效窗口，并以无 session/remote 的 C 单元验证这些调用不会产生 I/O。两版仍保持现有调用顺序；hook 只记录观察身份、失效和错误，不驱动 IDL、重新请求锁或执行 setter。共享 ABI 仍为 `ovs-webui.stp-witness.v1`，四表 52 个持久输入列及其原子类型/基数由每版实际 schema 对照共同 C/Go 注册表验证。原生 CI 独立读数据库后计算摘要，与 C 输出逐次对照。

三份编译进 Go 的构建档案固定各自源码、补丁、overlay、类型注册表、wire schema、数据库 source schema、构建选项。只允许精确的档案摘要和 amd64/arm64 的 enabled/sanitized 变体。无版本范围匹配、默认发现或未知版本回退。3.7.1 和 4.0.0 使用相同 8.8.0 数据库结构，但源码及档案摘要不同。档案身份不能由 schema 版本、ABI 字符串或命令存在性替代。

数据库 source schema 与实际服务器 `get_schema` 的 libovsdb 原生编码分别固定；上游会排序 enum 等内容，不能直接使用 source 编码替代服务器编码。CI 必须读取每版实际运行的服务器 schema 并命中档案摘要。两个 witness 都携带私有构建/schema 身份，组合预检要求两次身份一致，并匹配同一受保护数据库连接发现的实际 native schema。不存在部分成功或跨 schema 档案回退。

每个版本/架构均保留 P1 的 23 个原生场景、两个启用变体各 5 个既有 C 单元场景（4.0.0 另加 monitor schema 重置失效场景）、默认/启用的上游 STP/RSTP 测试，以及 P2a 的 20 个组合场景。新增第 21 个组合场景：使用另一已知但数据库结构不匹配的档案，独立重新固定 manifest 摘要，仍须返回零证据。Go 回归保留既有名称并新增闭合构建集合测试；正式浏览器回归及旧的 source/fence、权限、故障诊断和其他功能原生门禁继续执行。

证据按 `ovs-witness-<version>-<arch>` 分开保留，包含源码/归档/补丁/overlay/档案/实际可执行文件摘要、三个构建和 native 报告、实际服务器 schema、逐场景 Go 组合日志、C 单元和上游套件日志。六个 witness job 全部属于 CI Gate；不改变既有截止时间或重试策略，不将仅通过旧 schema fixture 的测试记为实际版本移植成功。

边界延续 [P2a 组合预检](SPANNING_TREE_WITNESS_PREFLIGHT_v0.1.md)：root 显式提供的实验 manifest 并非签名发行包或可复现二进制供应链证明；动态依赖、安装升级、生产 startup 缓存和执行截止时间仍需后续资格验证。该扩展只提供有界只读观察，既不证明远端此刻仍持锁，也不产生 Applied、执行许可或持久预约。`bridge.spanning-tree.prepare` 继续无条件返回 `SPANNING_TREE_EXECUTION_PENDING`；#23 / #43 保持开放，#71 继续延期。

下一阶段是 P3：独立密封执行操作、当前专用权限和两个 root grant、持久图域预约、SafeApply 与恢复门禁；任何生产安装仍需要独立的具体部署范围。
