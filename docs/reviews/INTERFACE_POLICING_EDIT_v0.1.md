# Interface ingress policing 编辑审阅 v0.1

2026-10-07 · #21 / #42 E8 候选。最终 PR/main 完整门禁、报告、视觉与同树核对成立后记录 `phase1-interface-policing-edit-v0.1`；本文件不预先宣称验收成功。

| 门禁 | 所需证据 |
| --- | --- |
| 输入与原值 | Candidate 封存/精确补偿、三模式边界、单 intent、拒绝 raw/原值/内核证据注入，禁止 Rebase 替换原值 |
| 策略与授权 | 独立 root exclusive ingress grant；拒绝继承 MTU/Port 授权；schema、options、graph、offload、custom burst、当前账户权限与 credential ceiling |
| 内核协议 | 沿用 E7 格式/中断/取消/预算测试；新增窄软件 profile、extra selector/action/chain/clsact/shared ingress/hardware 阻止写入，burst 指纹且忽略瞬态引用计数 |
| 三 schema × 两架构 native | 带宽、packet、禁用确认；模式切换/精确回滚；晚到 native CAS；foreign ingress/clsact；Prepare 后规则变动；补偿冲突；暂停 daemon 原值恢复；正向/补偿丢回复；同名替换；撤销 root 授权 |
| 正式浏览器 | 输入错误、原生单位、暂存无 live write、Standard/Expert、tablet/mobile review 与直接编辑阻止、真实安装证明、回滚、确认、共享 Interface audit/events、漂移与 Reader 权限 |
| 回归 | 既有 Bridge/Port/Bond/MTU/default/QinQ/native execution/Safe Apply/storage/systemd/auth/TLS/inventory，全量 race、10 次 admission 与 15 次暂停恢复保持 |

保留原始 CI 失败及原因；修复后需新提交的完整 PR CI 和合并 main 的独立完整 CI。不得通过跳过场景、提高重试、放宽权限/不确定结果门禁获得通过。

初轮 [CI 37616861046](https://github.com/sampsonlor/ovs-webui/actions/runs/37616861046) 的共享构建/单元、集成和 31 项原型浏览器通过；双架构 native 首版在外部 ingress 补偿状态分类与暂停 daemon 恢复两项失败。其余 12 项 native 正常/例外均通过。保留原始报告；修复将已确认的内核冲突映射为 rollback-conflict，并增加暂停恢复的原生 after-image 等待及只读补偿预检诊断。

新增截图前缀 `interface-policing-edit-`：bounds、standard、expert、tablet-review、mobile-review、awaiting、rolled-back、confirmed、drift、tablet-blocked、mobile-blocked、reader。最终交付检查双架构共 24 张；PR 逐张视觉审阅，main 复核代表模式、窄屏及例外。

原始 burst=0 的策略范围之外继续 Observe；内核安装读证明不能替代实际流量效果验收。#21/#42 和 #71 保持各自完整范围开放。
