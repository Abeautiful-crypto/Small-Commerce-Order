# 问题沉淀 · AI 自主学习库（dev-log）

> **定位（重要）**：本文件是 AI 的**自主学习经验库**，不是一次性记录。它的目的是让 AI **跨会话持续积累**——
> 把每次踩过的坑、根因、解决方式、是否上升为约束沉淀下来，**下次遇到同类问题先检索本库复用经验、轻松解决**，而不是从零排查。
> 每条问题务必打**分类标签**（如 `#超卖 #幂等 #越权 #缓存一致性 #并发`），便于检索。
> **机制**：开发中遇到的任何 bug、竞态、设计冲突、需求歧义，**先写本条再动手修**；修完回填"解决"与"是否上升为约束"。
> 规则详见 `general/principles.md` 第 3 节。新条目按下方模板追加在"开发问题记录"区。

---

## 预置：已识别的预防性坑（来自需求分析第六节，开发时对照规避）

| # | 坑 | 防御（已写入 `backend/constraints.md` 第 2 节铁律） |
|---|---|---|
| P1 | 超卖 | 库存原子条件更新，禁止先查后扣 |
| P2 | 重复释放（取消+超时同到） | 关闭用 CAS：`WHERE status='待付款'`，失败即幂等命中 |
| P3 | 重复收款（同交易号二次通知） | 渠道交易号唯一索引，重复只记流水 |
| P4 | 退款恢复库存的两条路径混淆 | 退款单带来源（售前/迟到）；只售前恢复，迟到不恢复 |
| P5 | 退款与发货竞态双成功 | CAS 先到者赢，输方收明确失败；已发货禁受理售前退款 |
| P6 | 越权 | 买家接口强制 `order.Owner == currentUser`（后端 `backend/constraints.md`）＋前端 `requireRole`（`frontend/permission.md`） |
| P7 | 迟到支付被误当正常支付复活订单 | 关闭后到达的成功通知 → 异常支付列表 → 人工退款，不复活、不重占 |

> 前端专属防御：金额浮点（`frontend/code-style.md` 第 4 节）、状态多副本（`frontend/code-style.md` 第 3 节）、越权渲染（`frontend/permission.md`）。

---

## 开发问题记录（模板）

### [YYYY-MM-DD] 问题简述
- 分类标签：（如 #超卖 #幂等 #越权 #缓存一致性 #并发，便于下次检索复用）
- 现象：
- 复现步骤：
- 根因：
- 解决：
- 是否上升为约束：是 / 否（若"是"，摘一句补进 `backend/constraints.md` 第 2 节，或 `frontend/code-style.md`/`frontend/permission.md` 的前端约束）
- 关联需求：需求文档第 X 节 / 状态机迁移 Y

---

## —— 以下为实际开发中追加的问题（按时间倒序）——

### [2026-11-11] 技术栈约束（MySQL+Redis）与开箱可运行性冲突（已闭环）
- 分类标签：#技术栈 #设计冲突 #存储一致性
- 现象：`.agent/backend/tech-stack.md` 指定 MySQL 8.0+ 为真相源、Redis 7+ 为缓存（标注「用户指定」）；首版后端为保证零依赖、`go run .` 开箱即用，采用 Go 标准库事务式状态存储（单写锁 + 状态副本失败回滚 + JSON 快照持久化）。
- 复现步骤：对照 `.agent/backend/tech-stack.md` §0/§1 与首版 `internal/store/store.go`；首版开发时本机 3306 / 6379 端口均为 CLOSED。
- 根因：需求文档正文（docx）未指定数据库，项目约束补充材料才指定 MySQL+Redis；首版为可立即运行而选了内存态，属于交付形态与约束的冲突。
- 解决：经用户确认并启动 MySQL/Redis 后完成改造——`StateStore` 接口抽象 + `internal/store/mysql.go`（`tx_guard FOR UPDATE` 串行化、库存 SQL 条件更新、订单状态 CAS、`payments.channel_txn_id` 与 `shipments.order_id` 唯一索引、流水只增、事务整体回滚）+ `migrations/001_init.up.sql` 版本化建表 + `internal/cache/cache.go`（`stock:` Cache-Aside 写失效、`pay_dedup:` SETNX、`sess:` 登录态、Noop 降级）。保留 `-store memory` 作为离线回退。证据：MySQL/Redis 集成测试、五大场景在 MySQL 后端 `-race` 全通过、HTTP 端到端冒烟、重启前后数据一致（持久化）、浏览器四角色回归。
- 是否上升为约束：否（既有约束已明确，按约束完成改造；新增测试隔离经验见下条）
- 关联需求：需求文档第三节 8 条规则 / 第四节状态机 / `.agent/backend/tech-stack.md` §1

### [2026-11-11] go test ./... 并行时共享 MySQL 测试库导致场景断言误失败
- 分类标签：#测试隔离 #并发 #CI
- 现象：同时设置 `MYSQL_TEST_DSN` 与 `SERVICE_MYSQL_DSN` 指向同一库时，`go test -race ./...` 出现「读取订单失败：订单不存在」「取消失败：无权访问（未登录）」。
- 复现步骤：`$env:MYSQL_TEST_DSN=...; $env:SERVICE_MYSQL_DSN=$env:MYSQL_TEST_DSN; go test -race ./...`。
- 根因：`go test ./...` 按包并行，`internal/store` 集成测试与 `internal/service` 场景测试共用同一个物理库，任一方 `Reset()` 会清空另一方刚写入的订单；场景测试里由 `GetOrder` 反查订单归属时拿到空 `Actor`，错误表现为权限失败而非数据丢失。
- 解决：`service_test.go` 新增 `mysqlBackendDSN()`，未显式设置 `SERVICE_MYSQL_DSN` 时从 `MYSQL_TEST_DSN` 自动派生 `<库名>_service`，两包各用独立库；同时保留显式 `SERVICE_MYSQL_DSN` 供自定义。修正后两套套件（外部依赖全量 / 零依赖默认）均通过。
- 是否上升为约束：是（已写入 README「测试」节：并行跑集成测试时每个包必须使用独立库，或显式指定不同 DSN）
- 关联需求：`general/acceptance.md` §3 自动化验收

### [2026-11-11] 买家可越权触发「渠道退款成功」自助批准退款（验收发现，已修复）
- 分类标签：#越权 #退款 #接口鉴权 #验收
- 现象：以买家身份 `POST /api/refunds/{rid}/channel-notify {"success":true}` 返回 200，退款单 `AWAITING→SUCCEEDED`、订单转 `REFUNDED`、可售 7→8（库存被非法恢复），等于买家可自助批准退款。
- 复现步骤：reset → u002 下单 P2 并支付 → 申请退款 → 客服同意（状态 AWAITING）→ 用 u002 的 `X-Token` 直接调用 channel-notify 且 success=true。
- 根因：`api` 层用内部构造的 `channelActor()` 调用 service，绕过真实请求身份；service 的 `requireRole` 又放行 `RoleChannel`，于是任何调用方都被当成可信渠道。前端 `requireRole` 只约束页面按钮，直接调 API 时失守（违反 `frontend/permission.md` §5「禁止把一个角色的写操作接口暴露给其他角色的调用方而不校验」）。
- 解决：`api` 层改传 `s.actorFrom(r)` 真实身份；`service.HandleRefundChannelNotify` 收紧为 `RoleCS/RoleOps`（演示态由客服/运营触发模拟渠道结果），删除 `channelActor()`；新增单测 `TestRule_ChannelNotifyPermission` 与验收脚本断言 `buyer_cannot_confirm_channel_refund`、`unauthorized_channel_no_state_change`；修复后重跑全套 `-race` 测试与 HTTP 一键验收。
- 是否上升为约束：是（已在 `backend/constraints.md` 第 2 节补「模拟渠道回调」铁律）
- 关联需求：需求文档第二节角色能力边界 / 第三节规则 6 / `general/acceptance.md` 场景五越权断言

