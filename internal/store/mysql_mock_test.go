package store

import (
	"database/sql/driver"
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

// oneRow 构造单行结果集。
func oneRow(cols []string, vals ...any) *sqlmock.Rows {
	out := make([]driver.Value, len(vals))
	for i, v := range vals {
		out[i] = v
	}
	return sqlmock.NewRows(cols).AddRow(out...)
}

// mockCache 记录库存失效调用。
type mockCache struct{ invalidated []string }

func (m *mockCache) Stock(string) (int, bool)                  { return 0, false }
func (m *mockCache) SetStock(string, int)                      {}
func (m *mockCache) InvalidateStock(ids ...string)             { m.invalidated = append(m.invalidated, ids...) }
func (m *mockCache) PaymentSeen(string) bool                   { return false }
func (m *mockCache) MarkPayment(string)                        {}
func (m *mockCache) SaveSession(string, string, string) error  { return nil }
func (m *mockCache) LoadSession(string) (string, string, bool) { return "", "", false }
func (m *mockCache) Ping() error                               { return nil }

var (
	mockNow   = time.Date(2026, 11, 11, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	mockLater = mockNow.Add(3 * time.Minute)
)

func newMockStore(t *testing.T) (*MySQLStore, sqlmock.Sqlmock, *mockCache) {
	t.Helper()
	db, mk, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fc := &mockCache{}
	return &MySQLStore{db: db, cache: fc, now: func() time.Time { return mockNow }}, mk, fc
}

// expectLoadState 为 loadState 的 8 张表 + meta 注册行数据。
func expectLoadState(mk sqlmock.Sqlmock, st *model.State) {
	pRows := sqlmock.NewRows([]string{"id", "name", "image", "price_fen", "old_price_fen", "total", "available", "on_sale", "sales"})
	for _, p := range st.Products {
		onSale := int8(0)
		if p.OnSale {
			onSale = 1
		}
		pRows.AddRow(p.ID, p.Name, p.Image, p.PriceFen, p.OldPriceFen, p.Total, p.Available, onSale, p.Sales)
	}
	mk.ExpectQuery(regexp.QuoteMeta("FROM products")).WillReturnRows(pRows)

	oRows := sqlmock.NewRows([]string{"id", "buyer_id", "buyer_name", "product_id", "product_name", "product_image",
		"price_fen", "qty", "amount_fen", "status", "created_at", "expires_at", "closed_at", "paid_at",
		"channel_txn_id", "refund_id", "shipment_id", "abnormal_id", "status_updated_at"})
	for _, o := range st.Orders {
		oRows.AddRow(o.ID, o.BuyerID, o.BuyerName, o.ProductID, o.ProductName, o.ProductImage,
			o.PriceFen, o.Qty, o.AmountFen, o.Status, o.CreatedAt, o.ExpiresAt,
			nullTime(o.ClosedAt), nullTime(o.PaidAt), nullStr(o.ChannelTxnID), nullStr(o.RefundID),
			nullStr(o.ShipmentID), nullStr(o.AbnormalID), o.StatusUpdatedAt)
	}
	mk.ExpectQuery(regexp.QuoteMeta("FROM orders")).WillReturnRows(oRows)

	payRows := sqlmock.NewRows([]string{"channel_txn_id", "order_id", "amount_fen", "created_at"})
	for _, p := range st.Payments {
		payRows.AddRow(p.ChannelTxnID, p.OrderID, p.AmountFen, p.CreatedAt)
	}
	mk.ExpectQuery(regexp.QuoteMeta("FROM payments")).WillReturnRows(payRows)

	nRows := sqlmock.NewRows([]string{"id", "channel_txn_id", "order_id", "amount_fen", "outcome", "detail", "created_at"})
	for _, n := range st.NotifyResults {
		nRows.AddRow(n.ID, n.ChannelTxnID, n.OrderID, n.AmountFen, n.Outcome, n.Detail, n.CreatedAt)
	}
	mk.ExpectQuery(regexp.QuoteMeta("FROM notify_results")).WillReturnRows(nRows)

	rRows := sqlmock.NewRows([]string{"id", "order_id", "source", "amount_fen", "status", "reason",
		"review_note", "channel_refund_id", "attempts", "created_at", "updated_at"})
	for _, r := range st.Refunds {
		// channel_refund_id 在建表中为 NOT NULL DEFAULT ''，直接回传字符串
		rRows.AddRow(r.ID, r.OrderID, r.Source, r.AmountFen, r.Status, r.Reason,
			r.ReviewNote, r.ChannelRefundID, r.Attempts, r.CreatedAt, r.UpdatedAt)
	}
	mk.ExpectQuery(regexp.QuoteMeta("FROM refunds")).WillReturnRows(rRows)

	shRows := sqlmock.NewRows([]string{"id", "order_id", "carrier", "tracking_no", "created_at", "operator"})
	for _, sh := range st.Shipments {
		shRows.AddRow(sh.ID, sh.OrderID, sh.Carrier, sh.TrackingNo, sh.CreatedAt, sh.Operator)
	}
	mk.ExpectQuery(regexp.QuoteMeta("FROM shipments")).WillReturnRows(shRows)

	aRows := sqlmock.NewRows([]string{"id", "order_id", "channel_txn_id", "amount_fen", "status", "reason",
		"handle_note", "refund_id", "created_at", "handled_at"})
	for _, a := range st.AbnormalPayments {
		// handle_note / refund_id 建表均为 NOT NULL DEFAULT ''，直接回传字符串
		aRows.AddRow(a.ID, a.OrderID, a.ChannelTxnID, a.AmountFen, a.Status, a.Reason,
			a.HandleNote, a.RefundID, a.CreatedAt, nullTime(a.HandledAt))
	}
	mk.ExpectQuery(regexp.QuoteMeta("FROM abnormal_payments")).WillReturnRows(aRows)

	lRows := sqlmock.NewRows([]string{"id", "type", "amount_fen", "order_id", "ref_id", "note", "created_at"})
	for _, l := range st.Ledger {
		lRows.AddRow(l.ID, l.Type, l.AmountFen, l.OrderID, l.RefID, l.Note, l.CreatedAt)
	}
	mk.ExpectQuery(regexp.QuoteMeta("FROM ledger")).WillReturnRows(lRows)

	mk.ExpectQuery(regexp.QuoteMeta("FROM meta")).WillReturnRows(oneRow([]string{"v"}, "5"))
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func mockBeginAndLock(mk sqlmock.Sqlmock) {
	mk.ExpectBegin()
	mk.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnRows(oneRow([]string{"id"}, 1))
}

// 综合场景：一次写事务覆盖 persistDiff 全部新增分支 + 库存条件更新 + 写失效。
func TestMySQLStore_UpdateHappyPath_AllInsertBranches(t *testing.T) {
	s, mk, fc := newMockStore(t)
	before := newSeed(mockNow) // P1 avail 4、P2 avail 8、订单 NO1、支付/流水各 1
	mockBeginAndLock(mk)
	expectLoadState(mk, before)

	// 写事务意图：扣 P1、加 P2、新增订单/支付/通知/流水/退款/发货/异常
	fn := func(st *model.State) error {
		st.Products[0].Available -= 1
		st.Products[1].Available += 1
		st.Orders = append(st.Orders, &model.Order{
			ID: "NO2", BuyerID: "u002", BuyerName: "李小购", ProductID: "P1",
			ProductName: "耳机", ProductImage: "images/product1.png",
			PriceFen: 10000, Qty: 1, AmountFen: 10000, Status: model.StatusPendingPay,
			CreatedAt: mockNow, ExpiresAt: mockNow.Add(10 * time.Minute), StatusUpdatedAt: mockNow,
		})
		st.Payments = append(st.Payments, &model.PaymentRecord{ChannelTxnID: "TXN-2", OrderID: "NO2", AmountFen: 10000, CreatedAt: mockLater})
		st.NotifyResults = append(st.NotifyResults, &model.NotifyResult{ID: "N2", ChannelTxnID: "TXN-2", OrderID: "NO2", AmountFen: 10000, Outcome: "ACCEPTED", CreatedAt: mockLater})
		st.AppendLedger("库存占用", 10000, "NO2", "", "占用 1 件", mockNow)
		st.Refunds = append(st.Refunds, &model.Refund{ID: "R2", OrderID: "NO2", Source: model.RefundSourcePresale, AmountFen: 10000, Status: model.RefundPending, CreatedAt: mockNow, UpdatedAt: mockNow})
		st.Shipments = append(st.Shipments, &model.Shipment{ID: "SH2", OrderID: "NO2", Carrier: "顺丰", TrackingNo: "SF1", CreatedAt: mockNow, Operator: "wh"})
		st.AbnormalPayments = append(st.AbnormalPayments, &model.AbnormalPayment{ID: "A2", OrderID: "NO2", ChannelTxnID: "TXN-X", AmountFen: 10000, Status: model.AbnormalPending, Reason: "迟到", CreatedAt: mockNow, HandledAt: &mockLater})
		return nil
	}

	mk.ExpectExec(regexp.QuoteMeta("UPDATE products SET available=available-?")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk.ExpectExec(regexp.QuoteMeta("UPDATE products SET name=")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk.ExpectExec(regexp.QuoteMeta("UPDATE products SET available=available+?")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk.ExpectExec(regexp.QuoteMeta("UPDATE products SET name=")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO orders")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO payments")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO notify_results")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO ledger")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO refunds")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO shipments")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO abnormal_payments")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("UPDATE meta SET v=")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk.ExpectCommit()

	if err := s.Update(fn); err != nil {
		t.Fatalf("综合写事务失败：%v", err)
	}
	if len(fc.invalidated) != 2 || fc.invalidated[0] != "P1" || fc.invalidated[1] != "P2" {
		t.Fatalf("应失效 P1/P2 库存缓存，实际 %v", fc.invalidated)
	}
	if err := mk.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 调用未全部发生：%v", err)
	}
}

// 业务拒绝：fn 返回错误 → 回滚，不失效缓存。
func TestMySQLStore_UpdateBusinessReject(t *testing.T) {
	s, mk, fc := newMockStore(t)
	mockBeginAndLock(mk)
	expectLoadState(mk, newSeed(mockNow))
	mk.ExpectRollback()

	err := s.Update(func(st *model.State) error {
		st.Products[0].Available -= 1
		return errors.New("业务拒绝")
	})
	if err == nil {
		t.Fatal("业务拒绝应返回错误")
	}
	if len(fc.invalidated) != 0 {
		t.Fatalf("回滚不应失效缓存：%v", fc.invalidated)
	}
	if err := mk.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}

// 订单 CAS 失败：影响行数 0 → 回滚报错。
func TestMySQLStore_UpdateOrderCASFail(t *testing.T) {
	s, mk, _ := newMockStore(t)
	mockBeginAndLock(mk)
	expectLoadState(mk, newSeed(mockNow))
	mk.ExpectExec(regexp.QuoteMeta("UPDATE orders SET")).WillReturnResult(sqlmock.NewResult(0, 0))
	mk.ExpectRollback()

	err := s.Update(func(st *model.State) error {
		o := st.Order("NO20260001")
		return model.Transition(o, model.StatusShipped, mockNow) // 待发货 → 已发货（合法迁移，CAS 由 SQL 判定）
	})
	if err == nil || !contains(err.Error(), "CAS 失败") {
		t.Fatalf("订单 CAS 失败应报错，实际 %v", err)
	}
}

// 库存条件更新影响行数不足 → 回滚报错（防超卖的最后防线）。
func TestMySQLStore_UpdateStockInsufficient(t *testing.T) {
	s, mk, _ := newMockStore(t)
	mockBeginAndLock(mk)
	expectLoadState(mk, newSeed(mockNow))
	mk.ExpectExec(regexp.QuoteMeta("UPDATE products SET available=available-?")).WillReturnResult(sqlmock.NewResult(0, 0))
	mk.ExpectRollback()

	err := s.Update(func(st *model.State) error {
		st.Products[0].Available -= 10 // 超过现有 4 件
		return nil
	})
	if err == nil || !contains(err.Error(), "可售数量不足") {
		t.Fatalf("库存不足应报错，实际 %v", err)
	}
}

// 已存在记录的差异更新分支：订单 CAS 更新、退款 CAS 更新、异常支付更新，重复记录跳过。
func TestMySQLStore_UpdateDiffBranches(t *testing.T) {
	s, mk, _ := newMockStore(t)
	before := newSeed(mockNow)
	r := &model.Refund{ID: "R1", OrderID: "NO20260001", Source: model.RefundSourcePresale,
		AmountFen: 10000, Status: model.RefundPending, Reason: "不想要了", Attempts: 1,
		CreatedAt: mockNow, UpdatedAt: mockNow}
	before.Refunds = append(before.Refunds, r)
	before.Shipments = append(before.Shipments, &model.Shipment{ID: "SH1", OrderID: "NO20260001", CreatedAt: mockNow})
	before.AbnormalPayments = append(before.AbnormalPayments, &model.AbnormalPayment{ID: "A1", OrderID: "NO20260001",
		ChannelTxnID: "TXN-LATE", AmountFen: 10000, Status: model.AbnormalPending, Reason: "迟到", CreatedAt: mockNow})
	mockBeginAndLock(mk)
	expectLoadState(mk, before)

	fn := func(st *model.State) error {
		// 商品 P1/P2 不变 → same 跳过
		// 订单迁移（CAS 更新）：种子订单为待发货 → 已发货
		o := st.Order("NO20260001")
		if err := model.Transition(o, model.StatusShipped, mockNow); err != nil {
			return err
		}
		// 退款单状态更新（CAS）
		rf := st.Refund("R1")
		rf.Status = model.RefundAwait
		rf.UpdatedAt = mockLater
		// 异常支付处理更新
		a := st.AbnormalPayments[0]
		a.Status = model.AbnormalRefunded
		a.HandledAt = &mockLater
		// 支付/通知/流水/发货重复 → 跳过
		return nil
	}
	mk.ExpectExec(regexp.QuoteMeta("UPDATE orders SET")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk.ExpectExec(regexp.QuoteMeta("UPDATE refunds SET")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk.ExpectExec(regexp.QuoteMeta("UPDATE abnormal_payments SET")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk.ExpectExec(regexp.QuoteMeta("UPDATE meta SET v=")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk.ExpectCommit()

	if err := s.Update(fn); err != nil {
		t.Fatalf("差异更新失败：%v", err)
	}
	if err := mk.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}

// 退款 CAS 失败报错。
func TestMySQLStore_UpdateRefundCASFail(t *testing.T) {
	s, mk, _ := newMockStore(t)
	before := newSeed(mockNow)
	before.Refunds = append(before.Refunds, &model.Refund{ID: "R1", OrderID: "NO20260001",
		Source: model.RefundSourcePresale, AmountFen: 10000, Status: model.RefundPending, CreatedAt: mockNow, UpdatedAt: mockNow})
	mockBeginAndLock(mk)
	expectLoadState(mk, before)
	mk.ExpectExec(regexp.QuoteMeta("UPDATE refunds SET")).WillReturnResult(sqlmock.NewResult(0, 0))
	mk.ExpectRollback()

	err := s.Update(func(st *model.State) error {
		rf := st.Refund("R1")
		rf.Status = model.RefundAwait
		rf.UpdatedAt = mockLater
		return nil
	})
	if err == nil || !contains(err.Error(), "CAS 失败") {
		t.Fatalf("退款 CAS 失败应报错，实际 %v", err)
	}
}

// loadState 读取失败 → Update 返回错误并回滚。
func TestMySQLStore_UpdateLoadStateError(t *testing.T) {
	s, mk, _ := newMockStore(t)
	mockBeginAndLock(mk)
	mk.ExpectQuery(regexp.QuoteMeta("FROM products")).WillReturnError(errors.New("db gone"))
	mk.ExpectRollback()

	if err := s.Update(func(*model.State) error { return nil }); err == nil {
		t.Fatal("loadState 失败应返回错误")
	}
}

// Reset：清空 8 张表 + seq 归零 + 重播种子 + 失效 P1/P2 缓存。
func TestMySQLStore_Reset(t *testing.T) {
	s, mk, fc := newMockStore(t)
	mk.ExpectBegin()
	mk.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnRows(oneRow([]string{"id"}, 1))
	for _, table := range []string{"ledger", "abnormal_payments", "shipments", "refunds", "notify_results", "payments", "orders", "products"} {
		mk.ExpectExec(regexp.QuoteMeta("DELETE FROM " + table)).WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mk.ExpectExec(regexp.QuoteMeta("UPDATE meta SET v='0'")).WillReturnResult(sqlmock.NewResult(0, 1))
	// 种子回播：2 商品 + 1 订单 + 1 支付 + 2 流水 + seq
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO products")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO products")).WillReturnResult(sqlmock.NewResult(2, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO orders")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO payments")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO ledger")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk.ExpectExec(regexp.QuoteMeta("INSERT INTO ledger")).WillReturnResult(sqlmock.NewResult(2, 1))
	mk.ExpectExec(regexp.QuoteMeta("UPDATE meta SET v=")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk.ExpectCommit()

	if err := s.Reset(); err != nil {
		t.Fatalf("Reset 失败：%v", err)
	}
	if len(fc.invalidated) != 2 || fc.invalidated[0] != "P1" || fc.invalidated[1] != "P2" {
		t.Fatalf("Reset 应失效 P1/P2 缓存，实际 %v", fc.invalidated)
	}
}

// ensureSeed：已有数据直接提交；空库播种。
func TestMySQLStore_EnsureSeed(t *testing.T) {
	// 分支 1：库中已有商品
	s, mk, _ := newMockStore(t)
	mk.ExpectBegin()
	mk.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnRows(oneRow([]string{"id"}, 1))
	mk.ExpectQuery(regexp.QuoteMeta("SELECT COUNT")).WillReturnRows(oneRow([]string{"n"}, 2))
	mk.ExpectCommit()
	if err := s.ensureSeed(); err != nil {
		t.Fatalf("非空库不应播种：%v", err)
	}

	// 分支 2：空库播种
	s2, mk2, _ := newMockStore(t)
	mk2.ExpectBegin()
	mk2.ExpectQuery(regexp.QuoteMeta("SELECT id FROM tx_guard")).WillReturnRows(oneRow([]string{"id"}, 1))
	mk2.ExpectQuery(regexp.QuoteMeta("SELECT COUNT")).WillReturnRows(oneRow([]string{"n"}, 0))
	mk2.ExpectExec(regexp.QuoteMeta("INSERT INTO products")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk2.ExpectExec(regexp.QuoteMeta("INSERT INTO products")).WillReturnResult(sqlmock.NewResult(2, 1))
	mk2.ExpectExec(regexp.QuoteMeta("INSERT INTO orders")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk2.ExpectExec(regexp.QuoteMeta("INSERT INTO payments")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk2.ExpectExec(regexp.QuoteMeta("INSERT INTO ledger")).WillReturnResult(sqlmock.NewResult(1, 1))
	mk2.ExpectExec(regexp.QuoteMeta("INSERT INTO ledger")).WillReturnResult(sqlmock.NewResult(2, 1))
	mk2.ExpectExec(regexp.QuoteMeta("UPDATE meta SET v=")).WillReturnResult(sqlmock.NewResult(0, 1))
	mk2.ExpectCommit()
	if err := s2.ensureSeed(); err != nil {
		t.Fatalf("空库播种失败：%v", err)
	}
}

// migrate：迁移文件缺失报错；正常执行所有语句（含注释、空行、超长语句截断提示）。
func TestMySQLStore_Migrate(t *testing.T) {
	s, mk, _ := newMockStore(t)
	if err := s.migrate(filepath.Join(t.TempDir(), "no-such-dir")); err == nil {
		t.Fatal("迁移文件缺失应报错")
	}

	dir := t.TempDir()
	sql := `-- 建表注释

CREATE TABLE products (id VARCHAR(32) PRIMARY KEY, name VARCHAR(255) NOT NULL DEFAULT '' COMMENT '名称占位用于超长语句截断分支验证');
CREATE TABLE orders (id VARCHAR(32) PRIMARY KEY);`
	if err := os.WriteFile(filepath.Join(dir, "001_init.up.sql"), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
	mk.ExpectExec(regexp.QuoteMeta("CREATE TABLE products")).WillReturnResult(sqlmock.NewResult(0, 0))
	mk.ExpectExec(regexp.QuoteMeta("CREATE TABLE orders")).WillReturnResult(sqlmock.NewResult(0, 0))
	if err := s.migrate(dir); err != nil {
		t.Fatalf("正常迁移不应报错：%v", err)
	}
	if err := mk.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}

func TestSplitSQL(t *testing.T) {
	stmts := splitSQL("-- 注释\n\nCREATE TABLE a(id INT);\n\nCREATE TABLE b(id INT) ;\n")
	if len(stmts) != 2 {
		t.Fatalf("应拆出 2 条语句，实际 %v", stmts)
	}
	if stmts[1] != "CREATE TABLE b(id INT)" {
		t.Fatalf("语句应去除首尾空白：%q", stmts[1])
	}
}

func TestEnsureDatabase_Errors(t *testing.T) {
	if err := ensureDatabase("not a valid dsn @@@"); err == nil {
		t.Fatal("非法 DSN 应报错")
	}
	// 数据库名含非法字符
	err := ensureDatabase("root:root@tcp(127.0.0.1:1)/bad-name?timeout=200ms&readTimeout=200ms")
	if err == nil || !contains(err.Error(), "数据库名非法") {
		t.Fatalf("非法库名应报错，实际 %v", err)
	}
	// 无库名 DSN：ensureDatabase 放行，由后续 Ping 失败兜底
	if err := ensureDatabase("root:root@tcp(127.0.0.1:1)/?timeout=200ms&readTimeout=200ms"); err != nil {
		t.Fatalf("无库名 DSN 应直接放行，实际 %v", err)
	}
}

func TestOpenMySQL_NilCacheDefaults(t *testing.T) {
	// now=nil → 默认时钟；cache=nil → Noop；DSN 不可达 → 连接失败报错
	_, err := OpenMySQL(MySQLConfig{
		DSN: "root:root@tcp(127.0.0.1:1)/?timeout=200ms&readTimeout=200ms&writeTimeout=200ms&parseTime=true",
	}, nil, nil)
	if err == nil || !contains(err.Error(), "连接 MySQL 失败") {
		t.Fatalf("不可达 MySQL 应报连接失败，实际 %v", err)
	}
}

// Snapshot 委托 loadState：正常返回与错误透传。
func TestMySQLStore_Snapshot(t *testing.T) {
	s, mk, _ := newMockStore(t)
	expectLoadState(mk, newSeed(mockNow))
	st, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Products) != 2 || len(st.Orders) != 1 {
		t.Fatalf("快照内容不符：products=%d orders=%d", len(st.Products), len(st.Orders))
	}
	// 订单 NULL 字段应正确映射
	if st.Orders[0].PaidAt == nil || st.Orders[0].ChannelTxnID == "" {
		t.Fatalf("种子订单的收款信息应恢复：%+v", st.Orders[0])
	}

	mk.ExpectQuery(regexp.QuoteMeta("FROM products")).WillReturnError(errors.New("boom"))
	if _, err := s.Snapshot(); err == nil {
		t.Fatal("查询失败应返回错误")
	}
}

// 纯函数辅助测试。
func TestPersistHelpers(t *testing.T) {
	if !same(map[string]int{"a": 1}, map[string]int{"a": 1}) {
		t.Fatal("相同 JSON 应判等")
	}
	if same(map[string]int{"a": 1}, map[string]int{"a": 2}) {
		t.Fatal("不同 JSON 不应判等")
	}
	m := idsOf([]string{"x", "y", "x"})
	if len(m) != 2 || !m["x"] || !m["y"] {
		t.Fatalf("idsOf 结果不符：%v", m)
	}
	got := collect([]*model.Refund{{ID: "R1"}, {ID: "R2"}}, func(r *model.Refund) string { return r.ID })
	if len(got) != 2 {
		t.Fatalf("collect 结果不符：%v", got)
	}
	if nz("") != nil || nz("tx") != "tx" {
		t.Fatal("nz 空串应转 nil")
	}
	if tp(nil) != nil {
		t.Fatal("tp nil 应转 nil")
	}
	tt := mockNow
	if tp(&tt) != mockNow {
		t.Fatal("tp 应返回时间值")
	}
	db, mkh, _ := sqlmock.New()
	mkh.ExpectClose()
	ms := &MySQLStore{db: db, cache: &mockCache{}}
	if ms.Cache() != ms.cache {
		t.Fatal("Cache() 应返回内部缓存")
	}
	if err := ms.Close(); err != nil {
		t.Fatalf("关闭不应报错：%v", err)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
