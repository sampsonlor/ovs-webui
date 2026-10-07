# Interface 对象级共享证据 v0.1

2026-10-07 · #21 / #42 E6。补齐批准 IA 的对象详情 Events / Audit 深链，使已支持的 MTU 操作可以从 Interface 追溯至共享执行、Safe Apply 和 Job。完整主单继续开放。

## 身份与关联

Events/Audit 的主要对象仍是原来的事务；新增可选、最多 32 项的 `related_object_refs`。只有 mgrd 已授权并由 provider Prepare 的执行计划内，带 MTU 上下文的直接 Interface 目标生成关联。Bridge、Port、自动 MTU 贡献者和同名新对象不被推定为目标；浏览器不提交关联或名称映射。

Admission 的审计及 Job、执行状态、Safe Apply 状态和操作决定记录都携带关联。Job 将目标保存在自己的 durable document，后续事件沿用原值，支持恢复而不依赖当前库存。事务仍独立报告 commit、Applied、确认和补偿状态；关联本身不证明任何执行结果。Workspace/Validation、不带执行计划的协调器记录和外部库存事件的全量对象级覆盖不在本批宣称范围。

API 1.20.0 仅为 Resource / Job 读取模型增加可选字段，不改变冻结请求和既有主要对象。Manager migration 012 建立 `evidence_record_objects` 索引，既有记录只导入已知主要对象；不改写旧证据、不从名称或当前关系补猜历史目标。每条新记录的文档和索引与原执行写入同一事务；原子回滚与去重保持，删除记录时级联清理索引。SQL 的对象值均使用绑定参数。

`object_id` 匹配主要对象或显式关联目标，仍受相同集合权限、过滤条件、最大页数/字节预算、快照上界、保留期、Principal / 权限 / scope 绑定及 30 秒 cursor 到期限制。现有事务对象过滤保持有效；不因 Interface 已退休而删除历史引用。

## 页面与权限

Interface 详情在 Standard/Expert 均提供相同 Events/Audit 入口；缺少对应权限或会话无法核实时给出明确说明。旧 ID 的 NOT_FOUND 详情仍可进入该 ID 的共享证据，不跳转到同名替代对象。

正式前端保留路径与查询参数，刷新、深链、浏览器返回、分页和权限刷新都保持对象范围。延迟响应不能跨 scope 或重新填回已撤销数据。Cursor 失效明确提示，用户刷新时仅清除 cursor，不删除对象筛选；清除 scope 是独立、明确的 Show all 操作。详情带回原查询；若记录不含 URL 声称的对象引用，页面说明不匹配，不伪装为该对象历史。

共享列表显示 operation、result、reason、origin 和记录时间；Expert 增加 correlation、覆盖/保留信息。详情链接到精确 Interface、事务、Job 和已知共享资源，并说明旧生命周期记录不代表事务当前状态。桌面、平板 Standard 与手机均可只读追溯；本批不添加配置事务。

## 覆盖和验收

空结果只表示当前授权下保留的已关联记录，不证明没有发生过变更。升级前未带 Interface 关联的记录仍可从原事务/Job/相关性查询；本批不承诺完整历史。Manager 不可用时清空受保护内容并显示授权/连接状态，OVS provider 状态不被替换为 durable 记录的新鲜度。

Go 测试覆盖旧主要对象兼容、直接目标、同名替代身份隔离、对象快照/cursor、权限、Job 关联持久性、事务回滚、去重冲突、输入预算、migration 和级联清理。三份原生 schema 的现有 MTU 确认与精确补偿用例新增真实共享读取断言。正式浏览器覆盖实际 MTU 回滚/确认的 Audit→事务/Job/Interface 和 Events，翻页/刷新/返回/cursor 到期，Standard/Expert、深色、键盘和窄屏，以及拒绝权限、空历史、退休 ID 和 manager 故障恢复。

最终接受依赖 PR 和同树 main 的完整六项 CI、保留报告与截图复核，见[审阅矩阵](../reviews/INTERFACE_EVIDENCE_v0.1.md)。成立后记录 `phase1-interface-evidence-v0.1`。#21/#42 保持 In Progress，#71 仍保留既有偶发失败根因跟踪。
