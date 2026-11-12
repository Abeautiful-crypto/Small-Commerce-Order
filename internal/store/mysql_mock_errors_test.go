package store

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"small-commerce-order/internal/model"
)

var errBoom = errors.New("boom")

// loadState 的 8 个表查询按顺序出现。
var loadQueries = []string{
	"FROM products", "FROM orders", "FROM payments", "FROM notify_results",
	"FROM refunds", "FROM shipments", "FROM abnormal_payments", "FROM ledger",
}

var loadQueryCols = map[string][]string{
	"FROM products":          {"id", "name", "image", "price_fen", "old_price_fen", "total", "available", "on_sale", "sales"},
	"FROM orders":            {"id", "buyer_id", "buyer_name", "product_id", "product_name", "product_image", "price_fen", "qty", "amount_fen", "status", "created_at", "expires_at", "closed_at", "paid_at", "channel_txn_id", "refund_id", "shipment_id", "abnormal_id", "status_updated_at"},
	"FROM payments":          {"channel_txn_id", "order_id", "amount_fen", "created_at"},
	"FROM notify_results":    {"id", "channel_txn_id", "order_id", "amount_fen", "outcome", "detail", "created_at"},
	"FROM refunds":           {"id", "order_id", "source", "amount_fen", "status", "reason", "review_note", "channel_refund_id", "attempts", "created_at", "updated_at"},
	"FROM shipments":         {"id", "order_id", "carrier", "tracking_no", "created_at", "operator"},
	"FROM abnormal_payments": {"id", "order_id", "channel_txn_id", "amount_fen", "status", "reason", "handle_note", "refund_id", "created_at", "handled_at"},
	"FROM ledger":            {"id", "type", "amount_fen", "order_id", "ref_id", "note", "created_at"},
}

// expectUntil 前面 i 个查询返回空结果集，第 i 个查询注入失败模式。
func expectUntil(mk sqlmock.Sqlmock, failIdx int, failMode string) {
	for i, q := range loadQueries {
		switch {
		case i < failIdx:
			mk.ExpectQuery(regexp.QuoteMeta(q)).WillReturnRows(sqlmock.NewRows(loadQueryCols[q]))
		case i == failIdx && failMode == "error":
			mk.ExpectQuery(regexp.QuoteMeta(q)).WillReturnError(errBoom)
		case i == failIdx && failMode == "scan":
			// 全部用字符串填充数值列，触发 Scan 类型错误
			vals := make([]any, len(loadQueryCols[q]))
			for j := range vals {
				vals[j] = "bad-type"
			}
			mk.ExpectQuery(regexp.QuoteMeta(q)).WillReturnRows(oneRow(loadQueryCols[q], vals...))
		}
	}
	if failIdx >= len(loadQueries) {
		mk.ExpectQuery(regexp.QuoteMeta("FROM meta")).WillReturnRows(oneRow([]string{"v"}, "0"))
	}
}

// loadState 查询失败透传（8 张表逐一注入）。
func TestLoadState_QueryErrors(t *testing.T) {
	for i, q := range loadQueries {
		s, mk, _ := newMockStore(t)
		expectUntil(mk, i, "error")
		if _, err := s.Snapshot(); err == nil {
			t.Fatalf("%s 查询失败应透传错误", q)
		}
		if err := mk.ExpectationsWereMet(); err != nil {
			t.Fatalf("%s：%v", q, err)
		}
	}
}

// loadState 行扫描类型错误透传（8 张表逐一注入）。
func TestLoadState_ScanErrors(t *testing.T) {
	for i, q := range loadQueries {
		s, mk, _ := newMockStore(t)
		expectUntil(mk, i, "scan")
		if _, err := s.Snapshot(); err == nil {
			t.Fatalf("%s 扫描类型错误应透传", q)
		}
	}
}

// closed_at / handled_at 非空值应正确恢复。
func TestLoadState_NullableTimeBothBranches(t *testing.T) {
	s, mk, _ := newMockStore(t)
	st := newSeed(mockNow)
	st.Orders[0].ClosedAt = &mockLater // 已关闭时间非空
	st.AbnormalPayments = append(st.AbnormalPayments, &model.AbnormalPayment{
		ID: "A1", OrderID: "NO20260001", ChannelTxnID: "TXN-L", AmountFen: 10000,
		Status: model.AbnormalRefunded, Reason: "迟到", RefundID: "R9",
		CreatedAt: mockNow, HandledAt: &mockLater,
	})
	expectLoadState(mk, st)
	got, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got.Orders[0].ClosedAt == nil || !got.Orders[0].ClosedAt.Equal(mockLater) {
		t.Fatalf("closed_at 非空值应恢复：%+v", got.Orders[0])
	}
	if got.AbnormalPayments[0].HandledAt == nil || got.AbnormalPayments[0].RefundID != "R9" {
		t.Fatalf("handled_at/refund_id 应恢复：%+v", got.AbnormalPayments[0])
	}
}

// persistDiff 各 SQL 执行失败：事务回滚并透传错误。
func TestPersistDiff_ExecErrors(t *testing.T) {
	seed := newSeed(mockNow)
	empty := &model.State{}
	newOrder := &model.Order{ID: "NO9", BuyerID: "u001", ProductID: "P1", ProductName: "耳机",
		PriceFen: 10000, Qty: 1, AmountFen: 10000, Status: model.StatusPendingPay,
		CreatedAt: mockNow, ExpiresAt: mockNow.Add(10 * time.Minute), StatusUpdatedAt: mockNow}

	cases := []struct {
		name    string
		before  *model.State
		fn      func(*model.State) error
		pattern string
	}{
		{"商品插入失败", empty, func(st *model.State) error {
			st.Products = append(st.Products, &model.Product{ID: "P9", Name: "n", Total: 1, Available: 1, OnSale: true})
			return nil
		}, "INSERT INTO products"},
		{"库存扣减失败", seed, func(st *model.State) error {
			st.Products[0].Available -= 1
			return nil
		}, "UPDATE products SET available=available-?"},
		{"库存回补失败", seed, func(st *model.State) error {
			st.Products[0].Available += 1
			return nil
		}, "UPDATE products SET available=available+?"},
		{"商品资料更新失败", seed, func(st *model.State) error {
			st.Products[0].Name = "改名"
			return nil
		}, "UPDATE products SET name="},
		{"订单插入失败", empty, func(st *model.State) error {
			st.Orders = append(st.Orders, newOrder)
			return nil
		}, "INSERT INTO orders"},
		{"订单 CAS 更新失败", seed, func(st *model.State) error {
			return model.Transition(st.Order("NO20260001"), model.StatusShipped, mockNow)
		}, "UPDATE orders SET"},
		{"支付插入失败", empty, func(st *model.State) error {
			st.Payments = append(st.Payments, &model.PaymentRecord{ChannelTxnID: "TX9", OrderID: "NO9", AmountFen: 1, CreatedAt: mockNow})
			return nil
		}, "INSERT INTO payments"},
		{"通知插入失败", empty, func(st *model.State) error {
			st.NotifyResults = append(st.NotifyResults, &model.NotifyResult{ID: "N9", ChannelTxnID: "TX9", OrderID: "NO9", AmountFen: 1, Outcome: "ACCEPTED", CreatedAt: mockNow})
			return nil
		}, "INSERT INTO notify_results"},
		{"流水插入失败", empty, func(st *model.State) error {
			st.AppendLedger("库存占用", 1, "NO9", "", "备注", mockNow)
			return nil
		}, "INSERT INTO ledger"},
		{"退款插入失败", empty, func(st *model.State) error {
			st.Refunds = append(st.Refunds, &model.Refund{ID: "R9", OrderID: "NO9", Source: model.RefundSourcePresale, AmountFen: 1, Status: model.RefundPending, CreatedAt: mockNow, UpdatedAt: mockNow})
			return nil
		}, "INSERT INTO refunds"},
		{"退款 CAS 更新失败", func() *model.State {
			s := newSeed(mockNow)
			s.Refunds = append(s.Refunds, &model.Refund{ID: "R1", OrderID: "NO20260001", Source: model.RefundSourcePresale, AmountFen: 10000, Status: model.RefundPending, CreatedAt: mockNow, UpdatedAt: mockNow})
			return s
		}(),
			func(st *model.State) error {
				r := st.Refund("R1")
				r.Status = model.RefundAwait
				r.UpdatedAt = mockLater
				return nil
			}, "UPDATE refunds SET"},
		{"发货插入失败", empty, func(st *model.State) error {
			st.Shipments = append(st.Shipments, &model.Shipment{ID: "SH9", OrderID: "NO9", Carrier: "顺丰", TrackingNo: "SF", CreatedAt: mockNow, Operator: "wh"})
			return nil
		}, "INSERT INTO shipments"},
		{"异常支付插入失败", empty, func(st *model.State) error {
			st.AbnormalPayments = append(st.AbnormalPayments, &model.AbnormalPayment{ID: "A9", OrderID: "NO9", ChannelTxnID: "TXL", AmountFen: 1, Status: model.AbnormalPending, Reason: "迟到", CreatedAt: mockNow})
			return nil
		}, "INSERT INTO abnormal_payments"},
		{"异常支付更新失败", func() *model.State {
			s := newSeed(mockNow)
			s.AbnormalPayments = append(s.AbnormalPayments, &model.AbnormalPayment{ID: "A1", OrderID: "NO20260001", ChannelTxnID: "TXL", AmountFen: 10000, Status: model.AbnormalPending, Reason: "迟到", CreatedAt: mockNow})
			return s
		}(),
			func(st *model.State) error {
				a := st.AbnormalPayments[0]
				a.Status = model.AbnormalRefunded
				a.HandledAt = &mockLater
				return nil
			}, "UPDATE abnormal_payments SET"},
		{"序号更新失败", seed, func(st *model.State) error { return nil }, "UPDATE meta SET v="},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, mk, _ := newMockStore(t)
			mockBeginAndLock(mk)
			expectLoadState(mk, tc.before)
			mk.ExpectExec(regexp.QuoteMeta(tc.pattern)).WillReturnError(errBoom)
			mk.ExpectRollback()
			if err := s.Update(tc.fn); err == nil {
				t.Fatal("SQL 失败应透传错误并回滚")
			}
			if err := mk.ExpectationsWereMet(); err != nil {
				t.Fatalf("%v", err)
			}
		})
	}
}

// Update/Reset/ensureSeed 的 Begin / 行锁 / Commit 失败路径。
func TestTxLifecycleErrors(t *testing.T) {
	// Update：Begin 失败
	s, mk, _ := newMockStore(t)
	mk.ExpectBegin().WillReturnError(errBoom)
	if err := s.Update(func(*model.State) error { return nil }); err == nil {
		t.Fatal("Begin 失败应报错")
	}

	// Update：行锁失败
	s2, mk2, _ := newMockStore(t)
	mk2.ExpectBegin()
	mk2.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnError(errBoom)
	mk2.ExpectRollback()
	if err := s2.Update(func(*model.State) error { return nil }); err == nil {
		t.Fatal("行锁失败应报错")
	}

	// Update：Commit 失败
	s3, mk3, _ := newMockStore(t)
	mockBeginAndLock(mk3)
	expectLoadState(mk3, newSeed(mockNow))
	mk3.ExpectExec(regexp.QuoteMeta("UPDATE meta SET v=")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk3.ExpectCommit().WillReturnError(errBoom)
	if err := s3.Update(func(st *model.State) error { st.Seq++; return nil }); err == nil {
		t.Fatal("Commit 失败应报错")
	}

	// ensureSeed：Begin / 行锁 / 计数扫描 / 播种失败
	s4, mk4, _ := newMockStore(t)
	mk4.ExpectBegin().WillReturnError(errBoom)
	if err := s4.ensureSeed(); err == nil {
		t.Fatal("ensureSeed Begin 失败应报错")
	}
	s5, mk5, _ := newMockStore(t)
	mk5.ExpectBegin()
	mk5.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnError(errBoom)
	mk5.ExpectRollback()
	if err := s5.ensureSeed(); err == nil {
		t.Fatal("ensureSeed 行锁失败应报错")
	}
	s6, mk6, _ := newMockStore(t)
	mk6.ExpectBegin()
	mk6.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnRows(oneRow([]string{"id"}, 1))
	mk6.ExpectQuery(regexp.QuoteMeta("SELECT COUNT")).WillReturnRows(oneRow([]string{"n"}, "bad"))
	mk6.ExpectRollback()
	if err := s6.ensureSeed(); err == nil {
		t.Fatal("计数扫描失败应报错")
	}
	s7, mk7, _ := newMockStore(t)
	mk7.ExpectBegin()
	mk7.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnRows(oneRow([]string{"id"}, 1))
	mk7.ExpectQuery(regexp.QuoteMeta("SELECT COUNT")).WillReturnRows(oneRow([]string{"n"}, 0))
	mk7.ExpectExec(regexp.QuoteMeta("INSERT INTO products")).WillReturnError(errBoom)
	mk7.ExpectRollback()
	if err := s7.ensureSeed(); err == nil {
		t.Fatal("播种失败应报错")
	}

	// Reset：Begin / 行锁 / DELETE / seq 归零 / 播种 / Commit 失败
	s8, mk8, _ := newMockStore(t)
	mk8.ExpectBegin().WillReturnError(errBoom)
	if err := s8.Reset(); err == nil {
		t.Fatal("Reset Begin 失败应报错")
	}
	s9, mk9, _ := newMockStore(t)
	mk9.ExpectBegin()
	mk9.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnError(errBoom)
	mk9.ExpectRollback()
	if err := s9.Reset(); err == nil {
		t.Fatal("Reset 行锁失败应报错")
	}
	s10, mk10, _ := newMockStore(t)
	mk10.ExpectBegin()
	mk10.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnRows(oneRow([]string{"id"}, 1))
	mk10.ExpectExec(regexp.QuoteMeta("DELETE FROM ledger")).WillReturnError(errBoom)
	mk10.ExpectRollback()
	if err := s10.Reset(); err == nil {
		t.Fatal("Reset 清表失败应报错")
	}
	s11, mk11, _ := newMockStore(t)
	mk11.ExpectBegin()
	mk11.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnRows(oneRow([]string{"id"}, 1))
	for _, table := range []string{"ledger", "abnormal_payments", "shipments", "refunds", "notify_results", "payments", "orders", "products"} {
		mk11.ExpectExec(regexp.QuoteMeta("DELETE FROM " + table)).WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mk11.ExpectExec(regexp.QuoteMeta("UPDATE meta SET v='0'")).WillReturnError(errBoom)
	mk11.ExpectRollback()
	if err := s11.Reset(); err == nil {
		t.Fatal("Reset seq 归零失败应报错")
	}
	s12, mk12, _ := newMockStore(t)
	mk12.ExpectBegin()
	mk12.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnRows(oneRow([]string{"id"}, 1))
	for _, table := range []string{"ledger", "abnormal_payments", "shipments", "refunds", "notify_results", "payments", "orders", "products"} {
		mk12.ExpectExec(regexp.QuoteMeta("DELETE FROM " + table)).WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mk12.ExpectExec(regexp.QuoteMeta("UPDATE meta SET v='0'")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk12.ExpectExec(regexp.QuoteMeta("INSERT INTO products")).WillReturnError(errBoom)
	mk12.ExpectRollback()
	if err := s12.Reset(); err == nil {
		t.Fatal("Reset 播种失败应报错")
	}
	s13, mk13, _ := newMockStore(t)
	mk13.ExpectBegin()
	mk13.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnRows(oneRow([]string{"id"}, 1))
	for _, table := range []string{"ledger", "abnormal_payments", "shipments", "refunds", "notify_results", "payments", "orders", "products"} {
		mk13.ExpectExec(regexp.QuoteMeta("DELETE FROM " + table)).WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mk13.ExpectExec(regexp.QuoteMeta("UPDATE meta SET v='0'")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk13.ExpectExec(regexp.QuoteMeta("INSERT INTO products")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk13.ExpectExec(regexp.QuoteMeta("INSERT INTO products")).WillReturnResult(sqlmock.NewResult(2, 1))
	mk13.ExpectExec(regexp.QuoteMeta("INSERT INTO orders")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk13.ExpectExec(regexp.QuoteMeta("INSERT INTO payments")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk13.ExpectExec(regexp.QuoteMeta("INSERT INTO ledger")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk13.ExpectExec(regexp.QuoteMeta("INSERT INTO ledger")).WillReturnResult(sqlmock.NewResult(2, 1))
	mk13.ExpectExec(regexp.QuoteMeta("UPDATE meta SET v=")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk13.ExpectCommit().WillReturnError(errBoom)
	if err := s13.Reset(); err == nil {
		t.Fatal("Reset Commit 失败应报错")
	}
}

// migrate 执行失败：普通语句与超长语句（截断提示分支）都要覆盖。
func TestMySQLStore_MigrateExecErrors(t *testing.T) {
	s, mk, _ := newMockStore(t)
	dir := t.TempDir()
	short := "CREATE TABLE a (id INT)"
	long := "CREATE TABLE b (id INT, " + strings.Repeat("col VARCHAR(64) NOT NULL DEFAULT '', ", 5) + "z INT)"
	sqlFile := "-- c\n" + short + ";\n\n" + long + ";"
	if err := os.WriteFile(filepath.Join(dir, "001_init.up.sql"), []byte(sqlFile), 0o644); err != nil {
		t.Fatal(err)
	}
	mk.ExpectExec(regexp.QuoteMeta(short)).WillReturnError(errBoom)
	if err := s.migrate(dir); err == nil {
		t.Fatal("迁移执行失败应报错")
	}

	s2, mk2, _ := newMockStore(t)
	mk2.ExpectExec(regexp.QuoteMeta(long)).WillReturnError(errBoom)
	if err := s2.migrate(dir); err == nil {
		t.Fatal("超长语句迁移失败也应报错")
	}
}

// migrate("") 默认从工作目录的 migrations 读取（chdir 到仓库根）。
func TestMySQLStore_MigrateDefaultDir(t *testing.T) {
	wd, _ := os.Getwd()
	repo := filepath.Join(wd, "..", "..")
	if err := os.Chdir(repo); err != nil {
		t.Skipf("无法进入仓库根：%v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	data, err := os.ReadFile(filepath.Join("migrations", "001_init.up.sql"))
	if err != nil {
		t.Skipf("迁移文件缺失：%v", err)
	}
	stmts := splitSQL(string(data))
	s, mk, _ := newMockStore(t)
	for _, stmt := range stmts {
		mk.ExpectExec(regexp.QuoteMeta(stmt)).WillReturnResult(sqlmock.NewResult(0, 0))
	}
	if err := s.migrate(""); err != nil {
		t.Fatalf("默认目录迁移失败：%v", err)
	}
}

// 内存 Store 的持久化失败与 Close。
func TestStore_PersistAndCloseErrors(t *testing.T) {
	dir := t.TempDir()
	// data 指向目录 → 读错误（非 NotExist）分支
	if _, err := Open(dir, time.Now); err == nil {
		t.Fatal("data 为目录应报错")
	}
	// tmp 被目录占用 → 写快照失败
	path := filepath.Join(dir, "st.json")
	st1, err := Open(path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st1.Close(); err != nil {
		t.Fatalf("Close 不应报错：%v", err)
	}
	if err := os.MkdirAll(path+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st1.Update(func(*model.State) error { return nil }); err == nil {
		t.Fatal("写快照失败应报错")
	}
	if err := st1.Reset(); err == nil {
		t.Fatal("Reset 写快照失败应报错")
	}
	// Open 时种子首次持久化失败：tmp 目录已存在但目标文件不存在
	path2 := filepath.Join(dir, "seed.json")
	if err := os.MkdirAll(path2+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path2, time.Now); err == nil {
		t.Fatal("种子持久化失败应报错")
	}
}
