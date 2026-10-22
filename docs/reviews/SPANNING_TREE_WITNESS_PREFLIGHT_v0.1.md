# P2a 只读 witness 组合审查

- [x] 新接口限私有 provider；无生产 caller、通用 RPC、环境自动启用、写入或部署。
- [x] 复用 reviewed original、完整图围栏和当前独立授权；旧名称型预检保留自身语义。
- [x] 固定 UUID/新 nonce，实际 peer 可执行 FD + root 清单 pin，大小/上下文预算、无缓存。
- [x] 两次 witness 完整比较，独立原子输入摘要、全行版本/配置/原始值比较，失败全零。
- [x] 原生独占/local 及既有正反向 setter 验证；不将 known 声称为 server 即时锁权威或 Applied。
- [x] 默认关闭、P1 native/C/上游测试及所有旧回归继续执行；新场景与日志独立保留。
- [x] 未移植 runtime 3.7.1/4.0.0；签名发布、动态依赖、部署及 P3/P4 写入/预约/恢复仍待完成。

实际通过结果以对应 source/main 的独立完整 CI 和官方 artifact digest 为准。本文件列实现审查范围，不替代运行证据。
