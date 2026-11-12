# Small-Commerce-Order · 小型电商订单交易系统

按 `订单交易系统需求分析.docx` 的 8 条业务规则与 7 态状态机实现的 Go 后端，
前端按 `拼多多风格原型.html` 的视觉与信息架构生成，四角色（买家 / 运营 / 仓库 / 客服）权限隔离。

存储按 `.agent/backend/tech-stack.md` 落地：**MySQL 8.0+ 是唯一真相源，Redis 7+ 只做缓存 / 辅助去重 / 登录态**。

## 运行

前置：Go 1.22+、MySQL（默认 `127.0.0.1:3306`，`root/root`）、Redis（默认 `127.0.0.1:6379`，无密码）。
启动时会自动建库、幂等执行 `migrations/001_init.up.sql` 并写入预置数据，无需手工建表。

```bash
cd Small-Commerce-Order
go run .
```

浏览器打开 <http://localhost:8090>。首次预置 2 个商品（单价 10000 分）、3 名买家、
运营/仓库/客服各 1 名、1 条待发货订单与 1 条收款流水。运营页「重置演示数据」可恢复初态。

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-addr` | `:8090` | HTTP 监听地址 |
| `-store` | `mysql` | `mysql`（真相源）/ `memory`（零依赖离线回退） |
| `-dsn` | `root:root@tcp(127.0.0.1:3306)/small_commerce_order?charset=utf8mb4&parseTime=true&loc=Local` | MySQL DSN |
| `-migrations` | `./migrations` | 版本化建表迁移目录 |
| `-redis` | `127.0.0.1:6379` | 留空则禁用缓存 |
| `-redis-password` | 空 | Redis 密码 |
| `-data` | `./data/state.json` | 仅 `memory` 模式使用的快照文件 |
| `-web` | `./web` | 前端静态目录 |
| `-secret` | `demo-channel-secret`（优先读 `APP_CHANNEL_SECRET`） | 模拟支付渠道验签密钥 |
| `-env` | `demo`（优先读 `APP_ENV`） | `demo` / `production`；生产模式启用认证并收紧接口 |
| `-passwords` | 空（优先读 `APP_PASSWORDS`） | 生产模式账号密码，格式 `用户ID:密码,...` |
| `-passwordless` | 关（优先读 `APP_LOGIN_PASSWORDLESS=1`） | 生产模式下免密码登录，仅用于演示/预览 |

所有参数都支持同名 `APP_*` 环境变量（`APP_ADDR` / `APP_MYSQL_DSN` / `APP_REDIS_ADDR` / `APP_REDIS_PASSWORD` 等），
便于容器化注入；生产模式强校验见「部署上线」。`/api/meta` 的 `data.env` 会返回当前运行环境。

例：`go run . -dsn 'root:pwd@tcp(127.0.0.1:3306)/order_db?charset=utf8mb4&parseTime=true&loc=Local' -redis 127.0.0.1:6379`

`go run . -store memory` 可切回零依赖内存模式（单写锁 + 副本回滚 + JSON 快照），
业务规则与断言完全一致，仅用于离线演示和单元测试。

## 部署上线（Docker Compose + 1Panel）

> **凭据纪律**：真实密钥只放服务器上的 `.env`（已被 `.gitignore` 忽略）；
> 本文件与 `.env.example` 只有占位符。若服务器面板地址、账号或密码曾明文出现在聊天/工单里，
> 先轮换密码、更换入口路径并限制来源 IP，再进行任何部署操作。

### 前置条件

- 服务器安装 Docker 24+ 与 Compose 插件（1Panel「容器」页可装），1Panel 已安装 OpenResty。
- 域名已解析到服务器；HTTPS 由 1Panel 申请 Let's Encrypt 证书。
- 国内服务器若无法访问 Docker Hub：在 1Panel「容器 → 配置 → 镜像加速」配置加速地址，
  或在 `.env` 里设置 `GO_IMAGE` / `RUNTIME_IMAGE` 指向可拉取的同版本基础镜像。
  本机曾因连不上 Docker Hub 导致 `docker build --check` 无法拉取镜像元数据，
  这**不是** Dockerfile 语法问题；`docker compose --env-file .env.example config` 已校验通过。

### 顺序：先提交，再部署

1. 本地：`go test -race ./...`，以及 `python scripts/acceptance_check.py`（仅 demo 环境，48 项断言）。
2. 按 `.agent/general/git-convention.md` 提交，例如
   `[feat] [v1.0.0] [题目01] 订单交易系统前后端与 Docker 部署`。
3. `git tag v1.0.0` 并 `git push origin main`（push 属于外部操作，需人工确认后执行）。
4. 服务器从 GitHub clone / checkout 指定 tag，保证线上版本对应一个确定的 commit，可复现、可回滚。

### 服务器部署步骤

```bash
git clone https://github.com/Abeautiful-crypto/Small-Commerce-Order.git
cd Small-Commerce-Order
cp .env.example .env
# 生成 4 组强随机值：openssl rand -hex 24
# 编辑 .env 填写 MYSQL_* / REDIS_PASSWORD / APP_CHANNEL_SECRET / APP_PASSWORDS（6 个账号）
# 演示/预览可加一行 APP_LOGIN_PASSWORDLESS=1 免密码登录（仍强制会话与角色隔离）
chmod 600 .env
docker compose --env-file .env up -d --build
docker compose ps
docker compose logs --tail=50 app
```

首次启动自动执行 `migrations/001_init.up.sql` 并写入预置数据。
`mysql` / `redis` 仅加入内部网络、不暴露宿主 3306/6379；`app` 只绑定 `127.0.0.1:8090`。

### 1Panel 反向代理与 HTTPS

1. 「网站」→ 创建网站 → 反向代理，代理地址填 `http://127.0.0.1:8090`。
2. 「证书」→ 申请 Let's Encrypt → 开启强制 HTTPS。
3. 自定义 header 参考 `deploy/1panel-proxy.conf.example`。

### 生产模式 vs 演示模式

| 项 | demo（默认） | production（`APP_ENV=production`） |
|---|---|---|
| 登录 | 选角色即登录，密码可留空 | 必须账号密码（`APP_PASSWORDS`）；设 `APP_LOGIN_PASSWORDLESS=1` 可免密码但仍走会话 |
| 身份 | `X-Token` 优先，可回退 `X-Role` / `X-User-Id` | 只认 Redis 会话 `X-Token`，请求头伪造一律 403 |
| 演示接口 | `/api/reset`、`/api/orders/{id}/expire` 可用 | 不注册（404），避免线上数据被清空 |
| 启动校验 | 允许默认弱口令便于本地演示 | 强验签密钥、非默认 DSN、Redis 可用、6 个账号密码齐全，否则拒绝启动 |
| 存储 | `mysql` / `memory` | 强制 `mysql` |
| 验收脚本 | 可跑（会重置数据） | 检测 `meta.env=production` 后**拒绝执行** |

### 生产验证（只读，切勿对生产跑验收脚本）

```bash
curl -s https://<域名>/api/meta                       # data.env 应为 production
curl -s https://<域名>/api/products -H 'X-Token: <登录后令牌>'
curl -s -X POST https://<域名>/api/reset             # 应为 404
```

随后在页面上人工走一条「下单 → 支付 → 发货」链路，并在运营页核对对账汇总。

### 备份与回滚

```bash
docker compose exec mysql sh -c 'mysqldump -u root -p"$MYSQL_ROOT_PASSWORD" small_commerce_order' > backup.sql
# 回滚：git checkout v1.0.0 && docker compose --env-file .env up -d --build
```

数据卷 `mysql-data` / `redis-data` 不会随容器删除而丢失；**不要**执行 `docker compose down -v`（会清空数据）。

## 存储架构

**MySQL（`internal/store/mysql.go`）——真相源**

| 表 | 关键约束 |
|---|---|
| `products` | `available INT`；占用走 `UPDATE ... SET available=available-? WHERE id=? AND available>=?` |
| `orders` | 金额 `BIGINT` 分、价格快照；状态迁移走 `WHERE id=? AND status=?` CAS |
| `payments` | `UNIQUE(channel_txn_id)` 防重复收款 / 防串单 |
| `shipments` | `UNIQUE(order_id)` 保证只能登记一次发货 |
| `refunds` | `source`（`presale`/`late`）决定退款是否恢复库存 |
| `notify_results` / `ledger` / `abnormal_payments` | 只增不改，运营可审计 |
| `tx_guard` | 单行 `SELECT ... FOR UPDATE`，串行化所有跨表写事务 |

每个写事务：① 锁 `tx_guard` → ② 从 MySQL 加载全量状态 → ③ 副本上执行业务意图 →
④ 差异写回（库存条件更新、状态 CAS、唯一索引 INSERT，按影响行数判定成败）→
⑤ 提交 → ⑥ `DEL stock:{商品id}` 写失效。任一步失败整体回滚。

**Redis（`internal/cache/cache.go`）——仅加速，正确性不依赖它**

| 键 | 用途 |
|---|---|
| `stock:{商品id}` | 热点可售数读缓存，Cache-Aside；写事务提交后 `DEL`（随机 TTL 防雪崩） |
| `pay_dedup:{交易号}` | `SETNX` + 24h TTL 辅助去重，MySQL 唯一索引兜底；命中会在通知结果中标注 |
| `sess:{token}` | 登录态与角色，接口按此还原身份；请求头 `X-Token` |

Redis 不可用时自动降级为 `Noop`，服务仍可运行（登录退化为 `X-Role`/`X-User-Id` 请求头身份）。

## 测试

```bash
go test -count=1 -race ./...                                # 默认零依赖模式；外部集成测试自动跳过
python scripts/acceptance_check.py --base http://127.0.0.1:8090   # 五大场景 HTTP 验收，PASS/FAIL 明细
```

**覆盖率门禁**：整体与核心业务包（api/cache/model/service/store）单元覆盖率均须 ≥85%，
CI 与本地用同一脚本强制执行；最新证据见 `docs/coverage/`（profile + 门禁输出）与 `docs/测试报告.md`。

```bash
go test ./... -count=1 -covermode=atomic -coverprofile=coverage.out
python scripts/coverage_gate.py coverage.out     # 不达标退出码 1
```

验收脚本仅用 Python 标准库，逐项打印断言的预期值与实际值，并扫描四类硬错误
（超卖 / 重复收款 / 重复释放 / 越权），全通过返回 0；`--no-reset` 可保留现场数据。

完整套件（连真实 MySQL + Redis，并把五大场景也跑在 MySQL 真相源上）：

```powershell
$env:MYSQL_TEST_DSN  = 'root:root@tcp(127.0.0.1:3306)/small_commerce_order_test?charset=utf8mb4&parseTime=true&loc=Local'
$env:REDIS_TEST_ADDR = '127.0.0.1:6379'
go test -race -count=1 ./...
```

`SERVICE_MYSQL_DSN` 可显式指定场景测试库；未设置时会自动从 `MYSQL_TEST_DSN` 派生独立库，
避免 `go test ./...` 并行时 store 包与 service 包互相 `Reset`。

覆盖内容（每个源文件都有对应测试文件）：

| 测试文件 | 覆盖范围 |
|---|---|
| `internal/api/server_test.go` | HTTP 层单元测试：`httptest` + 进程内 cache（不依赖 Redis/MySQL）。会话与身份解析、四角色权限矩阵、错误码映射（400/403/404/409）、买家数据隔离、渠道退款越权回归、静态托管 |
| `internal/service/service_test.go` | 五个验收场景 + 8 条规则回归（可分别跑在内存与 MySQL 后端） |
| `internal/service/order_test.go` | `order.go` 分支：下单参数/权限校验、关闭幂等、支付通知验签/金额/未知订单/串单/忽略、超时自动关闭、`ForceExpire` 守卫、空扫描无副作用 |
| `internal/service/refund_test.go` | `refund.go` 分支：退款申请与审核守卫、拒绝原因记录、渠道通知幂等与退款号串单、失败重试、异常支付处理、发货唯一性与默认值 |
| `internal/store/mysql_test.go` | 建表与 `BIGINT` 列、20 并发原子扣减不超卖、唯一索引、SQL CAS、事务整体回滚、重启持久化 |
| `internal/cache/cache_test.go` | 库存缓存与写失效、`SETNX` 去重 TTL、会话读写、降级实现 |

```bash
go test -race -count=1 -run 'TestAPI|TestUnit' ./internal/api ./internal/service   # 只跑新增单元/接口测试
```

## 目录

```
main.go                        启动、存储/缓存装配、超时扫描、静态托管
migrations/                    版本化建表（up/down）
internal/model/                领域模型、7 态状态机、预置账号
internal/store/                StateStore 接口、MySQL 真相源、内存回退实现
internal/cache/                Redis 缓存 / 会话 / 去重与 Noop 降级
internal/service/              8 条业务规则唯一落点 + 验收测试
internal/api/                  REST/JSON 接口、身份解析、统一响应
web/                           前端页面（index.html / style.css / app.js / images）
scripts/acceptance_check.py    一键 HTTP 验收脚本（五大场景 + 四类硬错误）
```

## 设计与约束对照

| 规则 | 实现位置 | 关键机制 |
|---|---|---|
| R1 下单占用/价格快照 | `CreateOrder` | SQL 原子条件扣减 `available>=qty`；订单冗余单价与金额 |
| R2 超时关闭幂等 | `CloseExpiredOrders` / `CancelOrder` | 服务端时间；状态 CAS 成功才释放一次 |
| R3 支付幂等/防串单 | `HandlePaymentNotify` | HMAC-SHA256 验签、金额匹配、`UNIQUE(channel_txn_id)`、拒绝结果可查 |
| R4 迟到支付人工通道 | `CLOSED` 分支 | 不复活订单、不重占库存，生成异常支付记录 |
| R5 退款×发货互斥 | `ApplyRefund` / `Ship` | 申请退款即状态迁移为「退款中」，状态机 + CAS 使发货必然失败 |
| R6 退款来源区分 | `HandleRefundChannelNotify` | 渠道确认成功才成功；失败可重试；恢复库存只认 `source=presale` |
| R7 发货唯一 | `Ship` | `UNIQUE(order_id)` + 仅「待发货」可迁移；已发货拦截售前退款 |
| R8 流水可审计 | `ledger` / `notify_results` | 只增不改；异常记录只能被处理，不能靠改状态抹掉 |

金额全程 `int64` 分，禁止浮点；正确性靠 MySQL 事务 + 行锁 + 条件更新 + 唯一索引，
不靠应用层内存锁或 Redis 计数。

## 五个场景（页面手工演示路径）

1. **活动正常成交**：买家「单独购买」→ 我的订单「去支付」→ 投递渠道通知 → 仓库「登记发货」→ 运营查看对账汇总。
2. **缺货与弃单**：连续下单至售罄（可售为 0 后下单被拒）；「模拟超时关闭」后释放的量可被其他买家买到。
3. **售前退款**：待发货订单「申请退款」→ 仓库页看不到该单 → 客服「同意退款」→「渠道返回失败」→「重试退款」→「渠道确认成功」，库存恢复。
4. **迟到支付**：待付款订单「模拟超时关闭」→「模拟迟到支付」→ 客服核实并退款 → 订单不复活、库存不增不减。
5. **操作异常**：支付弹窗「重复投递同一通知」验证幂等；页面底部「越权演示」按钮会真实调用其他角色接口并展示后端 403。

## 主要接口

商品：`GET /api/products`、`POST /api/products/{id}/onsale`
订单：`GET /api/orders`、`GET /api/orders/{id}`、`POST /api/orders`、`POST /api/orders/{id}/cancel|expire|pay-intent|refund-apply|ship`
会话：`POST /api/session`（Redis `sess:{token}`）
支付：`POST /api/payments/notify`
退款：`GET /api/refunds`、`POST /api/refunds/{id}/review|retry|channel-notify`
异常：`GET /api/abnormal`、`POST /api/abnormal/{id}/handle`
核查：`GET /api/summary|ledger|notify-results|state|shipments`、`POST /api/reset`

身份：demo 模式优先取 `X-Token`（Redis 会话），可回退 `X-Role` / `X-User-Id`；
production 模式**只认** `X-Token`，且 `POST /api/session` 必须带密码。名字始终由服务端账号表还原。
所有业务接口都在服务层做角色校验，买家接口额外校验订单归属。
`/api/reset` 与 `POST /api/orders/{id}/expire` 是演示接口，production 模式不注册（404）。
`/api/refunds/{id}/channel-notify` 是模拟渠道回调，仅客服 / 运营可触发（防止买家自助确认退款成功）；
生产环境应改为带渠道签名的回调，与支付通知对称。
