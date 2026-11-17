-- 订单交易系统 · 初始建表（MySQL 8.0+，幂等）
-- 金额一律 BIGINT（单位：分）；可售数量 INT；渠道交易号与发货订单号建唯一索引。
-- 状态取值由 ENUM 约束，状态迁移另由应用层 Transition 守卫 + SQL CAS 双重保证。

CREATE TABLE IF NOT EXISTS products (
  id            VARCHAR(32)  NOT NULL PRIMARY KEY,
  name          VARCHAR(255) NOT NULL,
  image         VARCHAR(255) NOT NULL DEFAULT '',
  -- 商品分类：家用电器 / 电子产品 / 儿童玩具 / 书本 / 食品 / 家居生活 / 其他
  category      VARCHAR(32)  NOT NULL DEFAULT '其他',
  price_fen     BIGINT       NOT NULL,
  old_price_fen BIGINT       NOT NULL DEFAULT 0,
  total         INT          NOT NULL,
  available     INT          NOT NULL,
  on_sale       TINYINT(1)   NOT NULL DEFAULT 1,
  sales         INT          NOT NULL DEFAULT 0,
  INDEX idx_products_category (category)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='商品表：名称/分类/图片/单价(分)/总库存/可售库存/销量/上下架';

CREATE TABLE IF NOT EXISTS orders (
  id                VARCHAR(64)  NOT NULL PRIMARY KEY,
  buyer_id          VARCHAR(64)  NOT NULL,
  buyer_name        VARCHAR(128) NOT NULL,
  product_id        VARCHAR(32)  NOT NULL,
  product_name      VARCHAR(255) NOT NULL,
  product_image     VARCHAR(255) NOT NULL DEFAULT '',
  price_fen         BIGINT       NOT NULL,
  qty               INT          NOT NULL,
  amount_fen        BIGINT       NOT NULL,
  status            VARCHAR(32)  NOT NULL,
  created_at        DATETIME(3)  NOT NULL,
  expires_at        DATETIME(3)  NOT NULL,
  closed_at         DATETIME(3)  NULL,
  paid_at           DATETIME(3)  NULL,
  channel_txn_id    VARCHAR(128) NULL,
  refund_id         VARCHAR(64)  NULL,
  shipment_id       VARCHAR(64)  NULL,
  abnormal_id       VARCHAR(64)  NULL,
  status_updated_at DATETIME(3)  NOT NULL,
  INDEX idx_orders_status (status),
  -- 超时关单扫描专用：候选查询 WHERE status='PENDING_PAY' AND expires_at<=? 走本索引，
  -- 代价只与「已过期待付款订单数」成正比，与订单总量无关
  INDEX idx_orders_status_exp (status, expires_at),
  INDEX idx_orders_buyer (buyer_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='订单表：买家与商品价格快照、支付方式、收货地址快照、状态机与关单/付款时间';

-- 幂等-支付：渠道交易号唯一，重复通知插不进去（防重复收款 / 防串单）
CREATE TABLE IF NOT EXISTS payments (
  channel_txn_id VARCHAR(128) NOT NULL PRIMARY KEY,
  order_id       VARCHAR(64)  NOT NULL,
  amount_fen     BIGINT       NOT NULL,
  created_at     DATETIME(3)  NOT NULL,
  UNIQUE KEY uk_payments_txn (channel_txn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='收款记录：渠道交易号唯一，防重复收款与串单';

-- 每笔渠道通知的处理结果（只增不改，可审计）
CREATE TABLE IF NOT EXISTS notify_results (
  id             VARCHAR(64)  NOT NULL PRIMARY KEY,
  channel_txn_id VARCHAR(128) NOT NULL,
  order_id       VARCHAR(64)  NOT NULL,
  amount_fen     BIGINT       NOT NULL,
  outcome        VARCHAR(32)  NOT NULL,
  detail         VARCHAR(1024) NOT NULL,
  created_at     DATETIME(3)  NOT NULL,
  INDEX idx_notify_txn (channel_txn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='渠道支付通知处理结果：只增不改，用于对账与审计';

-- source（presale / late）决定退款成功时是否恢复库存
CREATE TABLE IF NOT EXISTS refunds (
  id                VARCHAR(64)  NOT NULL PRIMARY KEY,
  order_id          VARCHAR(64)  NOT NULL,
  source            VARCHAR(16)  NOT NULL,
  amount_fen        BIGINT       NOT NULL,
  status            VARCHAR(24)  NOT NULL,
  reason            VARCHAR(512) NOT NULL DEFAULT '',
  review_note       VARCHAR(512) NOT NULL DEFAULT '',
  channel_refund_id VARCHAR(128) NOT NULL DEFAULT '',
  attempts          INT          NOT NULL DEFAULT 0,
  created_at        DATETIME(3)  NOT NULL,
  updated_at        DATETIME(3)  NOT NULL,
  INDEX idx_refunds_order (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='退款单：售前/迟到来源、状态、客服审核与渠道重试、渠道退款号';

-- 发货记录对订单唯一：只能登记一次发货；签收时间由买家「确认收货」回写
CREATE TABLE IF NOT EXISTS shipments (
  id          VARCHAR(64)  NOT NULL PRIMARY KEY,
  order_id    VARCHAR(64)  NOT NULL,
  carrier     VARCHAR(128) NOT NULL,
  tracking_no VARCHAR(128) NOT NULL,
  created_at  DATETIME(3)  NOT NULL,
  operator    VARCHAR(128) NOT NULL,
  signed_at   DATETIME(3)  NULL,
  signed_by   VARCHAR(128) NOT NULL DEFAULT '',
  UNIQUE KEY uk_shipments_order (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='发货记录：一单只发一次，签收时间与签收人由买家确认收货回写';

CREATE TABLE IF NOT EXISTS abnormal_payments (
  id            VARCHAR(64)  NOT NULL PRIMARY KEY,
  order_id      VARCHAR(64)  NOT NULL,
  channel_txn_id VARCHAR(128) NOT NULL,
  amount_fen    BIGINT       NOT NULL,
  status        VARCHAR(24)  NOT NULL,
  reason        VARCHAR(512) NOT NULL,
  handle_note   VARCHAR(512) NOT NULL DEFAULT '',
  refund_id     VARCHAR(64)  NOT NULL DEFAULT '',
  created_at    DATETIME(3)  NOT NULL,
  handled_at    DATETIME(3)  NULL,
  INDEX idx_abnormal_order (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='异常收款登记：如迟到支付，记录处理结果与关联退款单';

-- 运营对账流水：只增不改，禁止 UPDATE / DELETE 历史行
-- 只记录与钱相关的动作（收款 / 退款 / 退款审核 / 异常支付等），商品上下架不再写入本表
CREATE TABLE IF NOT EXISTS ledger (
  id          BIGINT       NOT NULL PRIMARY KEY,
  type        VARCHAR(64)  NOT NULL,
  amount_fen  BIGINT       NOT NULL,
  order_id    VARCHAR(64)  NOT NULL DEFAULT '',
  ref_id      VARCHAR(128) NOT NULL DEFAULT '',
  note        VARCHAR(1024) NOT NULL DEFAULT '',
  created_at  DATETIME(3)  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='资金流水：收款/退款/退款审核等与钱相关动作，只增不改';

-- 商品操作记录：新增商品 / 上架 / 下架，独立于资金流水
CREATE TABLE IF NOT EXISTS product_ops (
  id          BIGINT       NOT NULL PRIMARY KEY,
  type        VARCHAR(64)  NOT NULL,
  product_id  VARCHAR(64)  NOT NULL DEFAULT '',
  name        VARCHAR(255) NOT NULL DEFAULT '',
  operator    VARCHAR(128) NOT NULL DEFAULT '',
  created_at  DATETIME(3)  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='商品操作留痕：新增/上架/下架，与资金流水分开';

-- 商品评论：parent_id 为空是一级评论，否则为回复（只支持两级）
CREATE TABLE IF NOT EXISTS reviews (
  id          VARCHAR(64)  NOT NULL PRIMARY KEY,
  product_id  VARCHAR(64)  NOT NULL,
  order_id    VARCHAR(64)  NOT NULL,
  parent_id   VARCHAR(64)  NOT NULL DEFAULT '',
  buyer_id    VARCHAR(64)  NOT NULL,
  buyer_name  VARCHAR(128) NOT NULL,
  content     VARCHAR(1024) NOT NULL,
  created_at  DATETIME(3)  NOT NULL,
  INDEX idx_reviews_product (product_id),
  INDEX idx_reviews_order (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='商品评论与回复：parent_id 为空是一级评论，否则为回复';

-- 订单客服消息：买家发送的订单卡片（kind=order）与双方文本消息
CREATE TABLE IF NOT EXISTS chat_msgs (
  id          VARCHAR(64)  NOT NULL PRIMARY KEY,
  order_id    VARCHAR(64)  NOT NULL,
  from_role   VARCHAR(24)  NOT NULL,
  from_id     VARCHAR(64)  NOT NULL,
  from_name   VARCHAR(128) NOT NULL,
  kind        VARCHAR(16)  NOT NULL,
  body        VARCHAR(2048) NOT NULL DEFAULT '',
  created_at  DATETIME(3)  NOT NULL,
  INDEX idx_chat_order (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='订单客服消息：订单卡片(kind=order)与买卖双方文本';

-- 收货地址：买家可存多个，默认地址在本人地址内切换；下单时快照进订单
CREATE TABLE IF NOT EXISTS addresses (
  id          VARCHAR(64)  NOT NULL PRIMARY KEY,
  user_id     VARCHAR(64)  NOT NULL,
  name        VARCHAR(64)  NOT NULL,
  phone       VARCHAR(24)  NOT NULL,
  region      VARCHAR(128) NOT NULL,
  detail      VARCHAR(255) NOT NULL,
  is_default  TINYINT(1)   NOT NULL DEFAULT 0,
  created_at  DATETIME(3)  NOT NULL,
  INDEX idx_addresses_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='收货地址：每人最多 10 个、默认唯一，下单时快照进订单';

CREATE TABLE IF NOT EXISTS meta (
  k VARCHAR(64) NOT NULL PRIMARY KEY,
  v VARCHAR(255) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='元数据：全局序号 seq、schema_version 等键值';

-- 用户表：C 端买家账号（登录凭据，密文存储，不可逆）
CREATE TABLE IF NOT EXISTS users (
  id            VARCHAR(64)  NOT NULL PRIMARY KEY,
  name          VARCHAR(128) NOT NULL,
  password_hash VARCHAR(128) NOT NULL,
  salt          VARCHAR(64)  NOT NULL,
  created_at    DATETIME(3)  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='买家账号表：手机号登录，PBKDF2 密码哈希 + 随机盐';

-- 管理人员表：B 端工作台账号（运营 / 仓库 / 客服），role 决定登录后可见页面
CREATE TABLE IF NOT EXISTS staff (
  id            VARCHAR(64)  NOT NULL PRIMARY KEY,
  name          VARCHAR(128) NOT NULL,
  role          VARCHAR(32)  NOT NULL,
  password_hash VARCHAR(128) NOT NULL,
  salt          VARCHAR(64)  NOT NULL,
  created_at    DATETIME(3)  NOT NULL,
  INDEX idx_staff_role (role)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='员工账号表：运营/仓库/客服/管理员，role 决定登录后落地页与可见菜单';

-- 全局写事务串行化行锁：所有跨表写事务先 SELECT ... FOR UPDATE 此行
CREATE TABLE IF NOT EXISTS tx_guard (
  id   TINYINT NOT NULL PRIMARY KEY,
  note VARCHAR(64) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='全局写事务串行化行锁：写事务先 SELECT ... FOR UPDATE 此行';

INSERT IGNORE INTO tx_guard (id, note) VALUES (1, '全局写事务串行化行锁');
INSERT IGNORE INTO meta (k, v) VALUES ('seq', '0');
INSERT IGNORE INTO meta (k, v) VALUES ('schema_version', '1');
