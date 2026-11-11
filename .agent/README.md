# 订单交易系统 · AI 开发约束（总索引）

> 本目录是本项目 AI 辅助开发的"宪法"合集，已按 `general` / `backend` / `frontend` 分文件夹存放，按需加载。
> 配套加载入口：项目 Skill `order-dev-rules`（触发词见各文件头），它会先读本索引再跳转对应文件。

## 目录与何时读

| 文件 | 负责什么 | 什么时候读 |
|---|---|---|
| `general/principles.md` | 不分前后端的总纲：状态机是唯一裁判、闭环优先、问题沉淀机制、AI 行为准则 | 每次开发任务开始前都要过一遍 |
| `backend/constraints.md` | Go 工程结构、本题专属编码铁律（金额/库存/幂等/状态机/退款/流水）、并发、静态检查 | 写后端代码、加接口、改状态机时 |
| `backend/tech-stack.md` | 技术栈约束：MySQL 持久化、Redis 缓存、各组件职责与一致性策略（Cache-Aside/写失效） | 选型、落库、加缓存、接 DB/Redis、写迁移、调连接池时 |
| `frontend/code-style.md` | 前端目录/命名/状态单一来源/金额显示/防越权渲染/禁止项 | 写页面、写组件、改交互时 |
| `frontend/ui-style.md` | 拼多多视觉风格、移动端优先、组件化、用户反馈、响应式 | 调样式、加页面、做视觉时 |
| `frontend/permission.md` | requireRole 守卫、数据隔离、越权演示 | 涉及角色/权限/登录时 |
| `general/acceptance.md` | 提交前通用门槛 + 五大场景断言清单 + 自动化验收 | 完成功能自测、提交前 |
| `general/git-convention.md` | `[类型] [版本号] [编号] 描述`（feat/fix/hotfix/opt/docs/test） | 每次提交前 |
| `general/issue-log.md` | bug/竞态/歧义先写再修；顶部预置 P1–P7 预防性坑 | 遇到任何问题，先打开它 |

## 一页速查（开发前默念）

1. 这次改动对应状态机哪条迁移？——没有就不做（见 `general/principles.md` + `backend/constraints.md`）。
2. 金额是 `int64` 分吗？库存是原子条件更新吗？——不是就打回（`backend/constraints.md`）。
3. 幂等靠唯一索引 / CAS 吗？退款恢复库存看"来源"还是"猜状态"？（`backend/constraints.md`）
4. 前端涉及角色操作，过 `requireRole` 了吗？买家只能看自己的单？（`frontend/permission.md`）
5. 写完了立刻测这个功能了吗？遇到坑先写 `general/issue-log.md` 再修？（`general/principles.md` + `general/acceptance.md`）
6. 提交格式 `[类型] [版本号] [编号] 描述`——中文、一类型一提交（`general/git-convention.md`）。

## 前提假设

- 后端：**Go 1.21+**；金额 `int64` 分，全程禁浮点。
- 前端：当前为 `prototype/` 下的 HTML+JS 单文件原型；若后续落地框架（React/Vue）以 `frontend/code-style.md` 的目录约定为准。
- 存储与缓存：**MySQL 8.0+ 持久化 + Redis 7+ 缓存**（用户指定）；演示原型期可用内存态，真实后端以 MySQL 为真相源、Redis 仅加速/去重，详见 `backend/tech-stack.md`。正确性靠 MySQL 约束 + 事务 + 原子更新。
- 需求锚点：`订单交易系统需求分析.docx` + 状态机 + 8 条业务规则。

> 题目明确：**必须执行第一部分全部要求，不能只交付通知接口**。任何"只做接口"的倾向都被本合集禁止。
