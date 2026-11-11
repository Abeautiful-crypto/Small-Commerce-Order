-- 订单交易系统 · 初始建表（MySQL 8.0+，幂等）
-- 金额一律 BIGINT（单位：分）；可售数量 INT；渠道交易号与发货订单号建唯一索引。
-- 状态取值由 ENUM 约束，状态迁移另由应用层 Transition 守卫 + SQL CAS 双重保证。

CREATE TABLE IF NOT EXISTS products (
  id            VARCHAR(32)  NOT NULL PRIMARY KEY,
  name          VARCHAR(255) NOT NULL,
  image         VARCHAR(255) NOT NULL DEFAULT '',
  price_fen     BIGINT       NOT NULL,
  old_price_fen BIGINT       NOT NULL DEFAULT 0,
  total         INT          NOT NULL,
  available     INT          NOT NULL,
  on_sale       TINYINT(1)   NOT NULL DEFAULT 1,
  sales         INT          NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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
  INDEX idx_orders_buyer (buyer_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 幂等-支付：渠道交易号唯一，重复通知插不进去（防重复收款 / 防串单）
CREATE TABLE IF NOT EXISTS payments (
  channel_txn_id VARCHAR(128) NOT NULL PRIMARY KEY,
  order_id       VARCHAR(64)  NOT NULL,
  amount_fen     BIGINT       NOT NULL,
  created_at     DATETIME(3)  NOT NULL,
  UNIQUE KEY uk_payments_txn (channel_txn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 发货记录对订单唯一：只能登记一次发货
CREATE TABLE IF NOT EXISTS shipments (
  id          VARCHAR(64)  NOT NULL PRIMARY KEY,
  order_id    VARCHAR(64)  NOT NULL,
  carrier     VARCHAR(128) NOT NULL,
  tracking_no VARCHAR(128) NOT NULL,
  created_at  DATETIME(3)  NOT NULL,
  operator    VARCHAR(128) NOT NULL,
  UNIQUE KEY uk_shipments_order (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 运营对账流水：只增不改，禁止 UPDATE / DELETE 历史行
CREATE TABLE IF NOT EXISTS ledger (
  id          BIGINT       NOT NULL PRIMARY KEY,
  type        VARCHAR(64)  NOT NULL,
  amount_fen  BIGINT       NOT NULL,
  order_id    VARCHAR(64)  NOT NULL DEFAULT '',
  ref_id      VARCHAR(128) NOT NULL DEFAULT '',
  note        VARCHAR(1024) NOT NULL DEFAULT '',
  created_at  DATETIME(3)  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS meta (
  k VARCHAR(64) NOT NULL PRIMARY KEY,
  v VARCHAR(255) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 全局写事务串行化行锁：所有跨表写事务先 SELECT ... FOR UPDATE 此行
CREATE TABLE IF NOT EXISTS tx_guard (
  id   TINYINT NOT NULL PRIMARY KEY,
  note VARCHAR(64) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO tx_guard (id, note) VALUES (1, '全局写事务串行化行锁');
INSERT IGNORE INTO meta (k, v) VALUES ('seq', '0');
INSERT IGNORE INTO meta (k, v) VALUES ('schema_version', '1');
