# AI 开发工作流 · 阶段与功能点清单

> 机器可读版本：`.agent/workflow/features.json`（`scripts/feature_gate.py` 读取它）。
> 本文件负责**逐字段解释**：每个功能点为什么这么拆、对应哪条状态机迁移、必须有哪些单测、闸门怎么算通过。
> 每次开发只取**一个**功能点；`PASS` 之前不得取下一个（见 `general/feature-gate.md`）。

## 一、阶段划分（按依赖顺序，不可跳阶段）

| 阶段 | 主题 | 功能点 | 进入条件 | 阶段出口 |
| --- | --- | --- | --- | --- |
| 0 | 基线 | F0.1 | — | `gofmt` / `go vet` / 全量单测全绿 |
| 1 | 领域模型与配置 | F1.1 → F1.2 → F1.3 | 阶段 0 PASS | 状态机守卫、金额 int64 分、启动守卫可验证 |
| 2 | 存储与缓存 | F2.1 → F2.2 → (F2.3 可选) → F2.4 | F1.1 PASS | 原子更新、CAS、唯一索引、Cache-Aside 可验证 |
| 3 | 业务规则（service） | F3.1 → F3.2 → F3.3 → F3.4 → F3.5 → F3.6 → F3.7 | 阶段 1/2 相关点 PASS | 8 条规则 + 五大场景单测全绿 |
| 4 | 接口层 | F4.1 → F4.2 → F4.3 | F3.7 PASS | 权限矩阵、数据隔离、生产守卫可验证 |
| 5 | 验收与交付 | F5.1 → F5.2 → F5.3 | F4.2 + F4.3 PASS | 覆盖率 ≥85%、48 项断言、全量回归 |

> F2.3 是**可选**集成点：仅在设置 `MYSQL_TEST_DSN` 时执行；无环境时脚本标 `SKIP`，不计入阶段闸门，但不得把它写成 `PASS`。

## 二、功能点逐字段解释

字段含义：`ID` 唯一编号｜`阶段` 所属阶段｜`功能点` 本次要交付的最小能力｜`规则锚点` 对应需求/铁律｜`模块` 本次允许改动的包｜`必写单测` 闸门 S3 检查的文件｜`断言` 判定"没问题"的依据｜`前置` 必须先 PASS 的功能点。

### 阶段 0

| ID | 功能点 | 规则锚点 | 模块 | 必写单测 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F0.1 | 开发基线自检 | `acceptance.md §1` | 无（不写业务代码） | 沿用现有 14 个测试文件 | `gofmt -l` 无输出、`go vet ./...` 0、`go test -race -count=1 ./...` 全绿 | — |

> 为什么先做：基线不绿就开发，后面任何一次闸门失败都分不清是"原有问题"还是"新改动引入"。

### 阶段 1 · 领域模型与配置

| ID | 功能点 | 规则锚点 | 模块 | 必写单测 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F1.1 | 状态机守卫与 7 态迁移 | 需求第四节状态机 / 铁律-状态机 | `internal/model` | `model_test.go` | 合法迁移通过、非法迁移明确报错、终态不可再迁移 | F0.1 |
| F1.2 | 领域模型、流水只增、预置账号 | 铁律-流水不可变 / 规则 8 | `internal/model` | `model_test.go` | 金额 int64 分、流水只增不改、账号按角色隔离 | F1.1 |
| F1.3 | 配置解析与启动/生产守卫 | `tech-stack.md §3` | `internal/app` | `app_test.go` | 生产拒绝内存存储、生产要求 Redis、未知 store 报错 | F1.2 |

> 状态机是"唯一裁判"（`principles.md` 第 1 节），所以必须先于业务规则落地；找不到迁移的改动一律禁止。

### 阶段 2 · 存储与缓存

| ID | 功能点 | 规则锚点 | 模块 | 必写单测 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F2.1 | 内存存储事务语义与缓存写失效 | `tech-stack.md §2` / issue-log 2026-11-12 | `internal/store` | `store_test.go` | 写提交失效变化的 `stock:{id}`、回滚不误失效、Reset 失效全部 | F1.1 |
| F2.2 | MySQL 原子更新 / CAS / 唯一索引（sqlmock） | 铁律-库存、幂等 / `tech-stack.md §1` | `internal/store` | `mysql_mock_test.go`、`mysql_mock_errors_test.go` | `WHERE 可售>=?`、CAS 失败即幂等、事务整体回滚 | F2.1 |
| F2.3 | MySQL 真实集成（可选） | `tech-stack.md §1.2` | `internal/store` | `mysql_test.go` | 20 并发抢 5 件成功数 = 5、重启数据一致 | F2.2 |
| F2.4 | Redis 缓存 / 去重 / 会话 / 降级 | `tech-stack.md §2` | `internal/cache` | `cache_test.go` | Cache-Aside 读、`SETNX` TTL、Redis 不可用降级 | F1.1 |

> 库存真相只在 MySQL；Redis 只做读缓存与短期去重，**不得用 Redis INCR 当库存**（`tech-stack.md §2.3`）。

### 阶段 3 · 业务规则（8 条规则唯一落点）

| ID | 功能点 | 规则锚点 | 模块 | 必写单测 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F3.1 | 下单占用库存与参数/权限校验 | 规则 1 | `internal/service` | `order_test.go` | 可售不足报错、原子占用不超卖、上架仅运营 | F1.1/F2.1/F2.4 |
| F3.2 | 取消 / 超时关闭幂等、库存只释放一次 | 规则 2 / 铁律-幂等-关闭 | `internal/service` | `order_test.go` | 取消+超时同到只释放一次、CAS `WHERE 待付款` | F3.1 |
| F3.3 | 支付通知幂等 / 拒收 / 迟到通知 | 规则 3、7 | `internal/service` | `order_test.go` | 同交易号只记流水、金额/验签不符拒收、已发货忽略、迟到不复活 | F3.2 |
| F3.4 | 售前退款、审核守卫与失败重试 | 规则 4、5 | `internal/service` | `refund_test.go` | 退款中挡发货、失败保留待处理、`presale` 退款恢复库存 | F3.1 |
| F3.5 | 渠道通知身份 / 异常支付 / 发货守卫 | 铁律-模拟渠道回调 | `internal/service` | `refund_test.go` | 买家不能确认渠道退款、迟到支付不重占、发货唯一 | F3.4 |
| F3.6 | 五大场景与规则 8 回归 | `acceptance.md §2` | `internal/service` | `service_test.go` | 5 个场景 + 流水只增全绿 | F3.3/F3.5 |
| F3.7 | 越权与访问边界、缓存一致性 | `permission.md` / 铁律-越权 | `internal/service` | `service_extra_test.go` | 买家只看自己的单、四角色分支、商品读 Cache-Aside | F3.6 |

> 退款恢复库存必须按**退款单来源**判定（`presale` 恢复 / `late` 不恢复），禁止用订单当前状态 `if` 猜——这是 P4 预防性坑。

### 阶段 4 · 接口层

| ID | 功能点 | 规则锚点 | 模块 | 必写单测 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F4.1 | 会话 / 身份解析 / 四角色权限矩阵 / 错误码 | `permission.md` | `internal/api` | `server_test.go` | 403 矩阵、400/403/404/409 映射、静态托管 | F3.7 |
| F4.2 | 下单流程 / 数据隔离 / 渠道越权回归 | `permission.md §3` | `internal/api` | `server_test.go` | HTTP 端到端、越权 403 且无状态改动 | F4.1 |
| F4.3 | 生产环境安全守卫 | README 部署节 | `internal/api` | `security_test.go` | 生产必须密码、禁演示端点、会话要求 Redis | F4.1 |

> 前端 `requireRole` 只是体验层，**后端必须独立校验真实请求身份**；`channelActor()` 内部构造身份绕过鉴权是已沉淀的越权 bug（issue-log 2026-11-11）。

### 阶段 5 · 验收与交付

| ID | 功能点 | 规则锚点 | 断言 | 前置 |
| --- | --- | --- | --- | --- |
| F5.1 | 覆盖率门禁 ≥85% | `unit-test.md T8` | 整体 + `api/cache/model/service/store` 每包 ≥85% | F4.2/F4.3 |
| F5.2 | 五大场景 HTTP 验收 | `acceptance.md §2、§3` | 48 项断言全 PASS、无四类硬错误（需服务已启动） | F5.1 |
| F5.3 | 全量回归 | `acceptance.md §1` | `go test -race -count=1 ./...` 全绿 | F5.2 |

## 三、闸门命令（每个功能点完成后立刻执行）

```bash
python scripts/feature_gate.py --feature F3.1        # 单点闸门（最常用）
python scripts/feature_gate.py --stage 3             # 整阶段闸门（按顺序逐点跑）
python scripts/feature_gate.py --feature F3.1 --dry-run   # 只看会执行什么
python scripts/check_module_tests.py                 # 模块单测存在性
```

退出码 `0 = PASS`（才允许推进）、`1 = FAIL`（先写 issue-log 再修）、`2 = 用法/环境错误`。
证据自动写入 `.agent/workflow/gate-records/<ID>.json`；`progress.md` 的状态必须与证据一致。

## 四、清单维护规则

1. 新增模块 → 先在 `features.json` 加功能点（含 `modules`、`test_files`、`run`、`requires`），再写实现。
2. 新增测试 → 同步更新对应功能点的 `run` 正则，保证闸门能命中。
3. **禁止**为了让闸门变绿而删除功能点、放宽 `run`、删断言或调低覆盖率阈值；规则变更走 `issue-log.md` → 更新约束文件的闭环。
