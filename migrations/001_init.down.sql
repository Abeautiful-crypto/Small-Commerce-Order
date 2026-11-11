-- 回滚初始建表（仅用于测试库重建，生产禁止手改 schema）
DROP TABLE IF EXISTS ledger;
DROP TABLE IF EXISTS abnormal_payments;
DROP TABLE IF EXISTS shipments;
DROP TABLE IF EXISTS refunds;
DROP TABLE IF EXISTS notify_results;
DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS meta;
DROP TABLE IF EXISTS tx_guard;
