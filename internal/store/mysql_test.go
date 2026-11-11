package store

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"small-commerce-order/internal/model"
)

// MySQL 集成测试：设置 MYSQL_TEST_DSN 后执行，未设置则跳过。
// 例：$env:MYSQL_TEST_DSN='root:root@tcp(127.0.0.1:3306)/small_commerce_order_test?charset=utf8mb4&parseTime=true&loc=Local'
func mysqlTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 MYSQL_TEST_DSN，跳过 MySQL 集成测试")
	}
	return dsn
}

func newMySQLTestStore(t *testing.T) (*MySQLStore, string) {
	t.Helper()
	dsn := mysqlTestDSN(t)
	cfg := MySQLConfig{DSN: dsn, MigrationsPath: "../../migrations", MaxOpenConns: 20, MaxIdleConns: 5}
	s, err := OpenMySQL(cfg, nil, time.Now)
	if err != nil {
		t.Fatalf("连接 MySQL 失败：%v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Reset(); err != nil {
		t.Fatalf("重置测试库失败：%v", err)
	}
	return s, dsn
}

func TestMySQL_SchemaAndSeed(t *testing.T) {
	s, _ := newMySQLTestStore(t)
	st, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Products) != 2 || len(st.Orders) != 1 || len(st.Ledger) != 2 {
		t.Fatalf("预置数据异常：products=%d orders=%d ledger=%d", len(st.Products), len(st.Orders), len(st.Ledger))
	}
	p := st.Product("P1")
	if p == nil || p.PriceFen != 10000 || p.Available != 4 {
		t.Fatalf("商品 P1 异常：%+v", p)
	}
	// 金额列必须是 BIGINT
	var colType string
	if err := s.db.QueryRow(`SELECT DATA_TYPE FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='orders' AND COLUMN_NAME='amount_fen'`).Scan(&colType); err != nil {
		t.Fatal(err)
	}
	if colType != "bigint" {
		t.Fatalf("amount_fen 列类型应为 BIGINT，实际 %s", colType)
	}
}

func TestMySQL_AtomicStockNoOversell(t *testing.T) {
	s, _ := newMySQLTestStore(t)
	if err := s.Update(func(st *model.State) error {
		p := st.Product("P1")
		p.Total, p.Available = 5, 5
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Update(func(st *model.State) error {
				p := st.Product("P1")
				if p.Available < 1 {
					return fmt.Errorf("可售不足")
				}
				p.Available-- // 写回时由 SQL 的 WHERE available>=1 原子裁决
				return nil
			})
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	st, _ := s.Snapshot()
	if success != 5 || st.Product("P1").Available != 0 {
		t.Fatalf("超卖：成功 %d 单，最终可售 %d（期望 5 / 0）", success, st.Product("P1").Available)
	}
}

func TestMySQL_UniqueIndexesAndCAS(t *testing.T) {
	s, _ := newMySQLTestStore(t)
	// 1. 渠道交易号唯一：第二次插入必须失败
	insPay := `INSERT INTO payments (channel_txn_id,order_id,amount_fen,created_at) VALUES ('TXN-DUP','NO20260001',10000,NOW())`
	if _, err := s.db.Exec(insPay); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(insPay); err == nil {
		t.Fatal("渠道交易号唯一索引未生效：重复收款风险")
	}
	// 2. 发货记录对订单唯一
	insShip := `INSERT INTO shipments (id,order_id,carrier,tracking_no,created_at,operator) VALUES ('SH-DUP','NO20260001','顺丰','SF1',NOW(),'wh')`
	if _, err := s.db.Exec(insShip); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO shipments (id,order_id,carrier,tracking_no,created_at,operator) VALUES ('SH-DUP2','NO20260001','顺丰','SF2',NOW(),'wh')`); err == nil {
		t.Fatal("发货订单唯一索引未生效：重复发货风险")
	}
	// 3. 状态迁移 CAS：seed 订单是待发货，用待付款条件更新应影响 0 行
	res, err := s.db.Exec(`UPDATE orders SET status='CLOSED' WHERE id='NO20260001' AND status='PENDING_PAY'`)
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := res.RowsAffected(); rows != 0 {
		t.Fatalf("CAS 失效：非法迁移影响了 %d 行", rows)
	}
	// 4. 库存原子条件更新：available>=5 在 available=4 时必须影响 0 行
	res, err = s.db.Exec(`UPDATE products SET available=available-5 WHERE id='P1' AND available>=5`)
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := res.RowsAffected(); rows != 0 {
		t.Fatalf("库存条件更新失效：影响了 %d 行（会超卖）", rows)
	}
}

func TestMySQL_TransactionRollback(t *testing.T) {
	s, _ := newMySQLTestStore(t)
	before, _ := s.Snapshot()
	err := s.Update(func(st *model.State) error {
		st.Product("P1").Available--
		st.Orders = append(st.Orders, &model.Order{ID: "NO-ROLLBACK", Status: model.StatusPendingPay, CreatedAt: time.Now(), ExpiresAt: time.Now()})
		st.AppendLedger("测试", 1, "NO-ROLLBACK", "", "应被回滚", time.Now())
		return fmt.Errorf("模拟业务失败")
	})
	if err == nil {
		t.Fatal("期望事务失败")
	}
	after, _ := s.Snapshot()
	if after.Product("P1").Available != before.Product("P1").Available ||
		len(after.Orders) != len(before.Orders) || len(after.Ledger) != len(before.Ledger) {
		t.Fatalf("事务未整体回滚：avail %d→%d orders %d→%d ledger %d→%d",
			before.Product("P1").Available, after.Product("P1").Available,
			len(before.Orders), len(after.Orders), len(before.Ledger), len(after.Ledger))
	}
}

func TestMySQL_PersistAcrossReopen(t *testing.T) {
	s, dsn := newMySQLTestStore(t)
	if err := s.Update(func(st *model.State) error {
		p := st.Product("P2")
		p.Available--
		st.AppendLedger("测试", 100, "NO20260001", "", "重启持久化验证", time.Now())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenMySQL(MySQLConfig{DSN: dsn, MigrationsPath: "../../migrations"}, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	st, err := s2.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if st.Product("P2").Available != 7 {
		t.Fatalf("重启后可售应为 7，实际 %d", st.Product("P2").Available)
	}
	if len(st.Ledger) != 3 {
		t.Fatalf("重启后流水应为 3 条，实际 %d", len(st.Ledger))
	}
}
