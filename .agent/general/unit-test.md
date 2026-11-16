# 09 · 模块单元测试强制规范（新增规则二）

> **定位**：把"开发的模块要有单元测试"落成**逐模块可检查**的规则，并与项目已有的覆盖率门禁（整体 / 核心包 ≥85%）、`-race`、五大场景断言对齐。
> **触发**：新建模块、往模块里加源文件、改模块行为、修 bug、补功能点。
> **配套**：`scripts/check_module_tests.py`（存在性检查）、`scripts/coverage_gate.py`（覆盖率门禁）、`general/feature-gate.md`（闸门）。

## 1. 一句话规则

**每个被开发的模块都必须有单元测试；没有 `*_test.go`（或前端等价断言）的模块，功能点闸门直接 FAIL，不允许"先写实现、测试以后补"。**

## 2. 模块清单与对应测试文件（本项目现状）

| 模块包 | 职责 | 必须存在的测试文件 | 现有测试覆盖要点 |
| --- | --- | --- | --- |
| `internal/model` | 领域模型、7 态状态机、流水、预置账号 | `model_test.go` | `CanTransition` / `Transition` / `IsTerminal` / `AppendLedger` |
| `internal/app` | 配置解析、启动时装与生产守卫 | `app_test.go` | `LoadConfig` / `Build` / 生产模式拒绝内存存储 |
| `internal/store` | `StateStore` 接口、MySQL 真相源、内存回退、写失效 | `store_test.go` + `mysql*_test.go` | 原子更新不超卖、唯一索引、CAS、事务回滚、缓存写失效 |
| `internal/cache` | Redis 缓存 / 会话 / 去重 / Noop 降级 | `cache_test.go` | Cache-Aside、`SETNX` TTL、降级、关闭后行为 |
| `internal/service` | 8 条业务规则唯一落点、五大场景 | `service_test.go` + `order_test.go` + `refund_test.go` + `service_extra_test.go` | 不超卖、幂等、退款×发货互斥、迟到支付、越权、流水只增 |
| `internal/api` | REST/JSON、身份解析、四角色权限、错误码 | `server_test.go` + `security_test.go` | 403 权限矩阵、买家数据隔离、渠道回调真实身份、生产守卫 |
| `web/`（前端） | 拼多多风原型、四角色交互 | **等价要求见第 5 节** | `scripts/acceptance_check.py` 48 项 HTTP 断言 |

> 豁免：`main.go`（仅入口装配）、`migrations/*.sql`（DDL）、`docs/`、纯生成文件。豁免清单**只允许这一条**，新增模块一律纳入检查，不得自行扩充。

## 3. 单元测试写法（沿用项目既有风格）

| 规则 | 要求 |
| --- | --- |
| T1 一一对应 | 每个含业务源码的包至少 1 个 `*_test.go`；新增源文件要补对应测试文件，不能只堆在一个文件里 |
| T2 命名 | `Test<模块>_<行为>` 或 `TestUnit_<行为>` / `TestScenario<n>_<场景>` / `TestRule<n>_<规则>`，见名知义 |
| T3 表驱动 | 状态机合法/非法迁移、错误码、角色权限矩阵用表驱动用例，一条断言一个用例名 |
| T4 零外部依赖 | 默认单测不得依赖真实 MySQL/Redis；用 `sqlmock`、`miniredis` 或内存 `StateStore`。真实 DB 集成测试须读 `MYSQL_TEST_DSN`，未设置自动 `t.Skip` |
| T5 测试隔离 | 并行包不共用同一物理库；`SERVICE_MYSQL_DSN` 与 `MYSQL_TEST_DSN` 指向不同库（已沉淀在 `issue-log.md` 2026-11-11 条） |
| T6 竞态 | 涉及并发不变量（不超卖、CAS 互斥、重复通知）的测试必须可用 `-race` 跑通 |
| T7 固定命令 | `go test -race -count=1 ./...`（`-count=1` 禁用缓存，避免假绿） |
| T8 覆盖率 | 提交前 `make coverage`：整体与 `api/cache/model/service/store` 每包 ≥85%，由 `scripts/coverage_gate.py` 强制执行 |

## 4. 必测不变量矩阵（每个功能点自己认领）

| 不变量 | 至少要有的一条断言 | 对应测试文件 |
| --- | --- | --- |
| 不超卖 | 20 并发抢 5 件，成功占用数 = 5，第 6 起报"可售不足" | `store/mysql_test.go`、`service/service_test.go` |
| 关闭/释放幂等 | 取消 + 超时同到，可售只加回一次 | `service/order_test.go` |
| 支付幂等 | 同渠道交易号二次通知只记流水、不动账 | `service/order_test.go`、`store/mysql_test.go` |
| 退款 × 发货互斥 | 并发只出一种结果，输方明确失败 | `service/service_test.go` |
| 退款恢复看来源 | `presale` 恢复库存、`late` 不恢复 | `service/refund_test.go` |
| 越权 | 买家访问他人订单 403；渠道回调不接受买家身份 | `api/server_test.go`、`api/security_test.go` |
| 迟到支付 | 已关闭订单不复活、不重占库存、异常流水可见 | `service/refund_test.go`、`service/service_test.go` |
| 流水只增 | 历史流水无 UPDATE/DELETE | `service/service_test.go` (`TestRule8_LedgerAppendOnly`) |

> 复杂不变量先写断言再写实现（`principles.md` 第 2 节 TDD 倾向）；断言可以先红，但闸门必须最终全绿。

## 5. 前端模块的等价要求

当前前端是 `web/` 下 HTML+JS 单文件原型，项目未引入 JS 测试框架，**不为了合规硬塞 Jest**。等价要求：

1. `web/app.js` 中每个写操作（下单 / 支付 / 取消 / 退款 / 发货 / 上/下架）必须在 `scripts/acceptance_check.py` 中有对应断言。
2. 每个写操作函数的 `requireRole` 守卫必须被"越权演示"断言覆盖（验收场景 5）。
3. 金额展示换算（`分 → ¥`）不得参与算术；若新增格式化函数，须在前端模块内提供可调用的纯函数并补断言。
4. 后续落地 React/Vue 时，按 `frontend/code-style.md` 目录新增 `*.test.ts/tsx`，并把新模块纳入 `scripts/check_module_tests.py` 的 `--include-frontend` 检查。

## 6. 检查命令

```bash
python scripts/check_module_tests.py                 # 模块单测存在性（退出码 1 = 缺失）
python scripts/feature_gate.py --feature F3.1       # 功能点闸门，内部会再查一次
make test                                           # go vet + go test -race ./...
make coverage                                       # 覆盖率门禁 ≥85%
```

## 7. 与闸门的关系

`feature-gate.md` 的 S2/S3 检查项直接读本文件的模块清单与豁免规则。两者是同一件事的两面：**本文件规定"什么算有单测"，闸门规定"没有就不许往前走"**。
