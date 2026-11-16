package store

import (
	"testing"
	"time"
)

// 集成测试（需 MYSQL_TEST_DSN，未设置自动跳过）：
// 1) 启动迁移后 orders 必须带 (status, expires_at) 复合索引；
// 2) 候选集查询只返回已到期的待付款订单。
func TestMySQL_PendingExpiredIDsAndIndex(t *testing.T) {
	s, _ := newMySQLTestStore(t)
	now := time.Now()

	// 索引存在性
	rows, err := s.db.Query(`SHOW INDEX FROM orders WHERE Key_name='idx_orders_status_exp'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("orders 表缺少 idx_orders_status_exp 复合索引")
	}
	rows.Close()

	// 造一条已到期的待付款单 + 一条未到期的待付款单
	ins := `INSERT INTO orders (id,buyer_id,buyer_name,product_id,product_name,product_image,price_fen,qty,amount_fen,
		status,created_at,expires_at,status_updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if _, err := s.db.Exec(ins, "NO-EXP-T1", "u001", "张小买", "P1", "无线蓝牙耳机", "images/product1.png",
		10000, 1, 10000, "PENDING_PAY", now.Add(-11*time.Minute), now.Add(-1*time.Minute), now.Add(-11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ins, "NO-EXP-T2", "u001", "张小买", "P1", "无线蓝牙耳机", "images/product1.png",
		10000, 1, 10000, "PENDING_PAY", now, now.Add(10*time.Minute), now); err != nil {
		t.Fatal(err)
	}

	ids, err := s.PendingExpiredIDs(now, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "NO-EXP-T1" {
		t.Fatalf("候选集应只含已到期的 NO-EXP-T1，实际 %v", ids)
	}
}
