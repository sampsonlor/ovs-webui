# OVS 3.3.9 witness 组合只读预检（P2a）

本阶段把 P1 的固定 UUID/nonce 扩展接入现有私有预检。没有生产 caller、公开 API、CLI 开关、部署或写入。`bridge.spanning-tree.prepare` 仍返回 `SPANNING_TREE_EXECUTION_PENDING`；#23 / #43 保持开放，#71 继续延期。3.7.1 / 4.0.0 实际源码移植及双架构资格测试仍是 P2 后续工作。

## 组合顺序和结果

`ReadSpanningTreeWitnessPreflight` 与既有名称型 M2c3 预检共用 reviewed before/after、专用当前权限、原凭证上限、两个独立 root grant、完整配置/依赖/绑定/generation 及原始值检查。名称型接口保留原有语义，不能以其结果代替 witness 资格。

整个调用共用原有两秒截止时间：当前授权/私有 scope → 固定 DB peer/file/schema → 第一个原子只读围栏 → witness A → witness B → 第二个原子只读围栏 → DB peer/file、当前授权及 scope 复核 → 本地参数和正反向 setter 验证。没有失败重试、轮询刷新、缓存或未知值默认补齐。隔离测试为等待自己引起的配置/独立 monitor 更新使用的 barrier 不在预检内部。

两个 DB 事务各保留原有四个 whole-table、零超时 wait 和四个 `_uuid` / `_version` select，再加入固定 52 字段及 Interface 实际 ofport 的四个 select。请求上限继续为 256 KiB，围栏结果另限 1 MiB；完整持久配置/UUID 集合和行版本继续围住不属于输入摘要的字段及其他拓扑引用。独立输入按已审查的 Root→Bridge→Port→Interface 图提取，缺字段、额外字段、缺/重复/外来行及不受支持的引用拒绝。实际 `get_schema` 经 Go 原生 schema codec 编码的全 schema 摘要必须与固定 3.3.9 profile 一致（OVS 会重编码 schema；源码文件编码另行固定），不能由相同版本字符串或三个 fixture 推导支持。

每次 witness 只发送 `ovs-webui/stp-witness-v1`、固定绑定的规范小写 Bridge UUID 和新生成的 32 字节密码学随机 nonce。沿用现有实际 SO_PEERCRED、boot/PID/start、真实 DB 打开文件、schema/root 和双向 established Unix socket-pair 来源检查，并在来源/构建读取前后重验授权。完整 wire 限 64 KiB，接收时间在帧完成时记录，后续鉴证不能刷新时间。

A/B 除 nonce 外的完整已解析证据必须相同：daemon/runtime 实例、Bridge UUID/name、绑定/configuration/authority epochs、IDL seqno、root/next_cfg、原始九字段、输入摘要和原生 local 状态。相同 nonce、先后时间异常、文件身份变化也拒绝。两围栏的行版本和独立输入摘要必须一致，且摘要等于 C 捕获的历史输入，原始九字段等于 reviewed original。随后要求原生 STP/RSTP 恰好独占、毫秒可无损转为秒，再用既有纯验证器检查 local before 和修改/补偿两个方向；STP hello 单位 caveat、RSTP timer 约束均不放宽。

任何失败只返回全零结果。成功仍是瞬时、私有只读证据，不是 Applied、预留、密封计划或 server 此刻的锁权威。排队中的 stolen/EOF 通知和采样后变化仍属于后续执行竞态模型，不能据此写入。

## 构建信任与资源边界

root 调用者必须显式提供 executable 路径、manifest 路径和 manifest SHA-256，没有自动发现。闭合 manifest 只有五个字段：`format=ovs-webui.stp-build.v1`、`profile_sha256`、`architecture`、`variant`、`executable_sha256`。profile 固定 ABI、上游 commit/archive、patch/overlay、输入 registry、schema、构建参数；仅允许已资格测试的 enabled/sanitized 变体与当前 amd64/arm64 架构。CI 验证 profile 对应的实际源码字节，构建清单保留 profile 摘要。

manifest 必须是 root-owned、不可被 group/world 写入的普通文件，最多 4096 字节；实际 peer 的 `/proc/PID/exe` 打开 FD 必须是同样受保护的普通可执行文件，最多 128 MiB，并与 root 指定路径的身份一致。每次采样前后重新哈希实际 FD，按 64 KiB 分块检查截止时间，比较 dev/inode/size/mode/owner/mtime/ctime；读取中变化、换包、清单替换和权限变化拒绝。A/B 之间继续比较来源及这两个文件的身份。不缓存文件内容信任，超出预算直接失败，不扩大两秒期限。

这仍是隔离构建 pin 原型：未交付签名发布包、系统动态依赖的逐文件鉴证、可复现发行包或生产启动缓存方案。普通文件 I/O 的截止检查是协作式；本次只资格测试 CI 的本地文件系统，不声称任意远程/故障文件系统都可及时中断内核读取。OVS 动态/shared 库构建、其他 provider、发行版 patch 及实际主机安装需要独立审查。没有安装或重启用户的 OVS。

## 验证范围

保留 P1 每架构默认关闭，以及 enabled / ASan+UBSan 各 23 个真实 kernel system Bridge 场景、5 个 C 检查和默认/启用的上游 STP/RSTP 测试；保留旧 Go race、正式 browser、认证/TLS/恢复/预算及原生预检基线。

新增 Go 单元检查固定方法/独立 nonce、wire 错误全零、逐项 epoch/实例变化、完整原子围栏与缺字段、清单/可执行文件预算和取消、profile 字节固定。实际补丁版 3.3.9 的两个架构、enabled / sanitized 各运行 20 个组合场景：STP/RSTP 正向只读、旧凭证上限、8 类构建/清单错误、两次采样间角色撤销/两个 root grant ABA/配置 ABA/清单替换/取消，以及整个预检截止、原生进程暂停和 RSTP 补偿方向。完整 test2json、构建 provenance、失败历史和官方 artifact digest 与对应提交一起保留；源提交与合并 main 分开验收，不拼接不同运行的通过项。

前端与公开协议没有变化，既有截图回归继续独立校验，不增加人工截图验收结论。
