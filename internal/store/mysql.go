package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"small-commerce-order/internal/cache"
	"small-commerce-order/internal/model"
)

// MySQLConfig 是 MySQL 真相源的连接配置。
type MySQLConfig struct {
	DSN            string
	MigrationsPath string
	MaxOpenConns   int
	MaxIdleConns   int
}

// MySQLStore 以 MySQL 为唯一真相源。
//
// 写事务流程（对应 .agent/backend/constraints.md 铁律）：
//  1. SELECT ... FOR UPDATE 锁 tx_guard 单行 → 所有跨表写事务串行化；
//  2. 从各表加载全量状态（真相源），在副本上执行业务意图；
//  3. 差异写回时，库存变化用 UPDATE ... WHERE available>=? 原子条件更新，
//     状态变化用 UPDATE ... WHERE id=? AND status=? 做 CAS，以影响行数判定成败；
//  4. 渠道交易号 / 发货订单号由 UNIQUE 索引兜底，重复插入直接使事务回滚；
//  5. 提交成功后 DEL 对应 stock:{id} 缓存键（Cache-Aside 写失效）。
type MySQLStore struct {
	db    *sql.DB
	cache cache.Cache
	now   func() time.Time
}

// ensureDatabase 确保 DSN 中的库存在。
// 生产环境由 compose/DBA 预建库并使用最小权限账号，此时直接连接即可，
// 不要求应用账号具备 CREATE DATABASE 权限；仅本地演示时才自动建库。
func ensureDatabase(dsn string) error {
	probe, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	if err := probe.Ping(); err == nil {
		_ = probe.Close()
		return nil
	}
	_ = probe.Close()

	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("解析 DSN 失败：%w", err)
	}
	name := cfg.DBName
	if name == "" {
		return nil
	}
	for _, ch := range name {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_') {
			return fmt.Errorf("数据库名非法：%s", name)
		}
	}
	cfg.DBName = ""
	root, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Ping(); err != nil {
		return err
	}
	_, err = root.Exec("CREATE DATABASE IF NOT EXISTS `" + name + "` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	return err
}

func OpenMySQL(cfg MySQLConfig, c cache.Cache, now func() time.Time) (*MySQLStore, error) {
	if now == nil {
		now = time.Now
	}
	if c == nil {
		c = cache.Noop{}
	}
	if err := ensureDatabase(cfg.DSN); err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("打开 MySQL 失败：%w", err)
	}
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 20
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 5
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(3 * time.Minute)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接 MySQL 失败：%w", err)
	}
	s := &MySQLStore{db: db, cache: c, now: now}
	if err := s.migrate(cfg.MigrationsPath); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureSeed(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *MySQLStore) Close() error { return s.db.Close() }

// ---------------- 迁移与预置 ----------------

func splitSQL(raw string) []string {
	lines := []string{}
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		lines = append(lines, t)
	}
	out := []string{}
	for _, stmt := range strings.Split(strings.Join(lines, "\n"), ";") {
		if t := strings.TrimSpace(stmt); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (s *MySQLStore) migrate(dir string) error {
	if dir == "" {
		dir = "migrations"
	}
	path := filepath.Join(dir, "001_init.up.sql")
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取迁移文件 %s 失败：%w", path, err)
	}
	for _, stmt := range splitSQL(string(b)) {
		if _, err := s.db.Exec(stmt); err != nil {
			short := stmt
			if len(short) > 90 {
				short = short[:90] + "..."
			}
			return fmt.Errorf("执行迁移失败 [%s]：%w", short, err)
		}
	}
	return nil
}

func (s *MySQLStore) ensureSeed() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockTx(tx); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM products").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return tx.Commit()
	}
	empty := normalize(&model.State{})
	if _, err := persistDiff(tx, &empty, newSeed(s.now())); err != nil {
		return err
	}
	return tx.Commit()
}

func lockTx(tx *sql.Tx) error {
	var guard int
	return tx.QueryRow("SELECT id FROM tx_guard WHERE id=1 FOR UPDATE").Scan(&guard)
}

// ---------------- StateStore 实现 ----------------

func (s *MySQLStore) Update(fn func(*model.State) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockTx(tx); err != nil {
		return fmt.Errorf("获取写事务行锁失败：%w", err)
	}
	before, err := loadState(tx)
	if err != nil {
		return err
	}
	next, err := clone(before)
	if err != nil {
		return err
	}
	if err := fn(next); err != nil {
		return err // 业务拒绝：事务回滚，DB 无任何改动
	}
	changed, err := persistDiff(tx, before, next)
	if err != nil {
		return err // SQL 条件更新/唯一索引冲突：事务回滚
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(changed) > 0 {
		s.cache.InvalidateStock(changed...)
	}
	return nil
}

func (s *MySQLStore) Snapshot() (*model.State, error) { return loadState(s.db) }

func (s *MySQLStore) Reset() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockTx(tx); err != nil {
		return err
	}
	for _, table := range []string{"ledger", "abnormal_payments", "shipments", "refunds", "notify_results", "payments", "orders", "products"} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE meta SET v='0' WHERE k='seq'"); err != nil {
		return err
	}
	empty := normalize(&model.State{})
	if _, err := persistDiff(tx, &empty, newSeed(s.now())); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.cache.InvalidateStock("P1", "P2")
	return nil
}

// ---------------- 状态加载（真相源） ----------------

type queryer interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func loadState(q queryer) (*model.State, error) {
	norm := normalize(&model.State{})
	st := &norm

	rows, err := q.Query("SELECT id,name,image,price_fen,old_price_fen,total,available,on_sale,sales FROM products ORDER BY id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		p := &model.Product{}
		var onSale int8
		if err := rows.Scan(&p.ID, &p.Name, &p.Image, &p.PriceFen, &p.OldPriceFen, &p.Total, &p.Available, &onSale, &p.Sales); err != nil {
			rows.Close()
			return nil, err
		}
		p.OnSale = onSale != 0
		st.Products = append(st.Products, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = q.Query(`SELECT id,buyer_id,buyer_name,product_id,product_name,product_image,price_fen,qty,amount_fen,
		status,created_at,expires_at,closed_at,paid_at,channel_txn_id,refund_id,shipment_id,abnormal_id,status_updated_at
		FROM orders ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		o := &model.Order{}
		var closed, paid sql.NullTime
		var txnID, refundID, shipmentID, abnormalID sql.NullString
		if err := rows.Scan(&o.ID, &o.BuyerID, &o.BuyerName, &o.ProductID, &o.ProductName, &o.ProductImage,
			&o.PriceFen, &o.Qty, &o.AmountFen, &o.Status, &o.CreatedAt, &o.ExpiresAt,
			&closed, &paid, &txnID, &refundID, &shipmentID, &abnormalID, &o.StatusUpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if closed.Valid {
			t := closed.Time
			o.ClosedAt = &t
		}
		if paid.Valid {
			t := paid.Time
			o.PaidAt = &t
		}
		o.ChannelTxnID = txnID.String
		o.RefundID = refundID.String
		o.ShipmentID = shipmentID.String
		o.AbnormalID = abnormalID.String
		st.Orders = append(st.Orders, o)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = q.Query("SELECT channel_txn_id,order_id,amount_fen,created_at FROM payments ORDER BY created_at,channel_txn_id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		p := &model.PaymentRecord{}
		if err := rows.Scan(&p.ChannelTxnID, &p.OrderID, &p.AmountFen, &p.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		st.Payments = append(st.Payments, p)
	}
	rows.Close()

	rows, err = q.Query("SELECT id,channel_txn_id,order_id,amount_fen,outcome,detail,created_at FROM notify_results ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		n := &model.NotifyResult{}
		if err := rows.Scan(&n.ID, &n.ChannelTxnID, &n.OrderID, &n.AmountFen, &n.Outcome, &n.Detail, &n.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		st.NotifyResults = append(st.NotifyResults, n)
	}
	rows.Close()

	rows, err = q.Query(`SELECT id,order_id,source,amount_fen,status,reason,review_note,channel_refund_id,attempts,created_at,updated_at
		FROM refunds ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		r := &model.Refund{}
		if err := rows.Scan(&r.ID, &r.OrderID, &r.Source, &r.AmountFen, &r.Status, &r.Reason, &r.ReviewNote,
			&r.ChannelRefundID, &r.Attempts, &r.CreatedAt, &r.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		st.Refunds = append(st.Refunds, r)
	}
	rows.Close()

	rows, err = q.Query("SELECT id,order_id,carrier,tracking_no,created_at,operator FROM shipments ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		sh := &model.Shipment{}
		if err := rows.Scan(&sh.ID, &sh.OrderID, &sh.Carrier, &sh.TrackingNo, &sh.CreatedAt, &sh.Operator); err != nil {
			rows.Close()
			return nil, err
		}
		st.Shipments = append(st.Shipments, sh)
	}
	rows.Close()

	rows, err = q.Query(`SELECT id,order_id,channel_txn_id,amount_fen,status,reason,handle_note,refund_id,created_at,handled_at
		FROM abnormal_payments ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		a := &model.AbnormalPayment{}
		var handled sql.NullTime
		if err := rows.Scan(&a.ID, &a.OrderID, &a.ChannelTxnID, &a.AmountFen, &a.Status, &a.Reason,
			&a.HandleNote, &a.RefundID, &a.CreatedAt, &handled); err != nil {
			rows.Close()
			return nil, err
		}
		if handled.Valid {
			t := handled.Time
			a.HandledAt = &t
		}
		st.AbnormalPayments = append(st.AbnormalPayments, a)
	}
	rows.Close()

	rows, err = q.Query("SELECT id,type,amount_fen,order_id,ref_id,note,created_at FROM ledger ORDER BY id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		l := &model.LedgerEntry{}
		if err := rows.Scan(&l.ID, &l.Type, &l.AmountFen, &l.OrderID, &l.RefID, &l.Note, &l.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		st.Ledger = append(st.Ledger, l)
	}
	rows.Close()

	var seq string
	if err := q.QueryRow("SELECT v FROM meta WHERE k='seq'").Scan(&seq); err == nil {
		st.Seq, _ = strconv.ParseInt(seq, 10, 64)
	}
	return st, nil
}

// ---------------- 差异写回（SQL 层原子条件更新） ----------------

func same(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

func idsOf(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func collect[T any](items []*T, key func(*T) string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[key(it)] = true
	}
	return m
}

func nz(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func tp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// persistDiff 把业务意图落到 SQL：新增用 INSERT（唯一索引兜底），
// 库存用条件更新、状态用 CAS，并以影响行数判定成败；返回被改动的商品 ID 供缓存失效。
func persistDiff(tx *sql.Tx, before, after *model.State) ([]string, error) {
	changedProducts := []string{}

	// ---- 商品：库存原子条件更新 ----
	for _, p := range after.Products {
		b := before.Product(p.ID)
		if b == nil {
			if _, err := tx.Exec(`INSERT INTO products (id,name,image,price_fen,old_price_fen,total,available,on_sale,sales)
				VALUES (?,?,?,?,?,?,?,?,?)`, p.ID, p.Name, p.Image, p.PriceFen, p.OldPriceFen,
				p.Total, p.Available, p.OnSale, p.Sales); err != nil {
				return nil, fmt.Errorf("插入商品失败：%w", err)
			}
			changedProducts = append(changedProducts, p.ID)
			continue
		}
		if same(b, p) {
			continue
		}
		if p.Available < b.Available {
			n := b.Available - p.Available
			// R1 铁律：UPDATE ... WHERE available>=n，禁止先查后扣
			res, err := tx.Exec("UPDATE products SET available=available-? WHERE id=? AND available>=?", n, p.ID, n)
			if err != nil {
				return nil, fmt.Errorf("库存条件更新失败：%w", err)
			}
			if rows, _ := res.RowsAffected(); rows != 1 {
				return nil, fmt.Errorf("可售数量不足（原子条件更新影响 %d 行）product=%s need=%d", rows, p.ID, n)
			}
		} else if p.Available > b.Available {
			n := p.Available - b.Available
			if _, err := tx.Exec("UPDATE products SET available=available+? WHERE id=?", n, p.ID); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec(`UPDATE products SET name=?,image=?,price_fen=?,old_price_fen=?,total=?,on_sale=?,sales=? WHERE id=?`,
			p.Name, p.Image, p.PriceFen, p.OldPriceFen, p.Total, p.OnSale, p.Sales, p.ID); err != nil {
			return nil, err
		}
		changedProducts = append(changedProducts, p.ID)
	}

	// ---- 订单：状态迁移 CAS（WHERE id=? AND status=?） ----
	for _, o := range after.Orders {
		b := before.Order(o.ID)
		if b == nil {
			if _, err := tx.Exec(`INSERT INTO orders
				(id,buyer_id,buyer_name,product_id,product_name,product_image,price_fen,qty,amount_fen,
				 status,created_at,expires_at,closed_at,paid_at,channel_txn_id,refund_id,shipment_id,abnormal_id,status_updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				o.ID, o.BuyerID, o.BuyerName, o.ProductID, o.ProductName, o.ProductImage,
				o.PriceFen, o.Qty, o.AmountFen, o.Status, o.CreatedAt, o.ExpiresAt,
				tp(o.ClosedAt), tp(o.PaidAt), nz(o.ChannelTxnID), nz(o.RefundID), nz(o.ShipmentID),
				nz(o.AbnormalID), o.StatusUpdatedAt); err != nil {
				return nil, fmt.Errorf("插入订单失败：%w", err)
			}
			continue
		}
		if same(b, o) {
			continue
		}
		res, err := tx.Exec(`UPDATE orders SET buyer_id=?,buyer_name=?,product_id=?,product_name=?,product_image=?,
			price_fen=?,qty=?,amount_fen=?,status=?,created_at=?,expires_at=?,closed_at=?,paid_at=?,
			channel_txn_id=?,refund_id=?,shipment_id=?,abnormal_id=?,status_updated_at=?
			WHERE id=? AND status=?`,
			o.BuyerID, o.BuyerName, o.ProductID, o.ProductName, o.ProductImage,
			o.PriceFen, o.Qty, o.AmountFen, o.Status, o.CreatedAt, o.ExpiresAt,
			tp(o.ClosedAt), tp(o.PaidAt), nz(o.ChannelTxnID), nz(o.RefundID), nz(o.ShipmentID),
			nz(o.AbnormalID), o.StatusUpdatedAt, o.ID, b.Status)
		if err != nil {
			return nil, fmt.Errorf("订单条件更新失败：%w", err)
		}
		if rows, _ := res.RowsAffected(); rows != 1 {
			return nil, fmt.Errorf("订单状态已被其它事务迁移（CAS 失败）order=%s from=%s to=%s",
				o.ID, b.Status.Label(), o.Status.Label())
		}
	}

	// ---- 支付流水：渠道交易号 UNIQUE 防重复收款 / 防串单 ----
	seenPay := collect(before.Payments, func(p *model.PaymentRecord) string { return p.ChannelTxnID })
	for _, p := range after.Payments {
		if seenPay[p.ChannelTxnID] {
			continue
		}
		if _, err := tx.Exec("INSERT INTO payments (channel_txn_id,order_id,amount_fen,created_at) VALUES (?,?,?,?)",
			p.ChannelTxnID, p.OrderID, p.AmountFen, p.CreatedAt); err != nil {
			return nil, fmt.Errorf("渠道交易号冲突或写入失败 txn=%s：%w", p.ChannelTxnID, err)
		}
	}

	// ---- 通知结果 / 流水：只增不改 ----
	seenNotify := collect(before.NotifyResults, func(n *model.NotifyResult) string { return n.ID })
	for _, n := range after.NotifyResults {
		if seenNotify[n.ID] {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO notify_results (id,channel_txn_id,order_id,amount_fen,outcome,detail,created_at)
			VALUES (?,?,?,?,?,?,?)`, n.ID, n.ChannelTxnID, n.OrderID, n.AmountFen, n.Outcome, n.Detail, n.CreatedAt); err != nil {
			return nil, err
		}
	}
	seenLedger := collect(before.Ledger, func(l *model.LedgerEntry) string { return strconv.FormatInt(l.ID, 10) })
	for _, l := range after.Ledger {
		if seenLedger[strconv.FormatInt(l.ID, 10)] {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO ledger (id,type,amount_fen,order_id,ref_id,note,created_at) VALUES (?,?,?,?,?,?,?)`,
			l.ID, l.Type, l.AmountFen, l.OrderID, l.RefID, l.Note, l.CreatedAt); err != nil {
			return nil, err
		}
	}

	// ---- 退款单 ----
	seenRefund := collect(before.Refunds, func(r *model.Refund) string { return r.ID })
	for _, r := range after.Refunds {
		if !seenRefund[r.ID] {
			if _, err := tx.Exec(`INSERT INTO refunds
				(id,order_id,source,amount_fen,status,reason,review_note,channel_refund_id,attempts,created_at,updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.OrderID, r.Source, r.AmountFen, r.Status,
				r.Reason, r.ReviewNote, r.ChannelRefundID, r.Attempts, r.CreatedAt, r.UpdatedAt); err != nil {
				return nil, err
			}
			continue
		}
		if b := before.Refund(r.ID); b != nil && !same(b, r) {
			res, err := tx.Exec(`UPDATE refunds SET status=?,reason=?,review_note=?,channel_refund_id=?,attempts=?,updated_at=?
				WHERE id=? AND status=?`, r.Status, r.Reason, r.ReviewNote, r.ChannelRefundID, r.Attempts,
				r.UpdatedAt, r.ID, b.Status)
			if err != nil {
				return nil, err
			}
			if rows, _ := res.RowsAffected(); rows != 1 {
				return nil, fmt.Errorf("退款单状态已被其它事务迁移（CAS 失败）refund=%s", r.ID)
			}
		}
	}

	// ---- 发货记录：order_id UNIQUE 保证只能登记一次 ----
	seenShip := collect(before.Shipments, func(sh *model.Shipment) string { return sh.ID })
	for _, sh := range after.Shipments {
		if seenShip[sh.ID] {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO shipments (id,order_id,carrier,tracking_no,created_at,operator)
			VALUES (?,?,?,?,?,?)`, sh.ID, sh.OrderID, sh.Carrier, sh.TrackingNo, sh.CreatedAt, sh.Operator); err != nil {
			return nil, fmt.Errorf("发货登记冲突（对订单唯一）order=%s：%w", sh.OrderID, err)
		}
	}

	// ---- 异常支付记录 ----
	seenAbn := collect(before.AbnormalPayments, func(a *model.AbnormalPayment) string { return a.ID })
	for _, a := range after.AbnormalPayments {
		if !seenAbn[a.ID] {
			if _, err := tx.Exec(`INSERT INTO abnormal_payments
				(id,order_id,channel_txn_id,amount_fen,status,reason,handle_note,refund_id,created_at,handled_at)
				VALUES (?,?,?,?,?,?,?,?,?,?)`, a.ID, a.OrderID, a.ChannelTxnID, a.AmountFen, a.Status,
				a.Reason, a.HandleNote, a.RefundID, a.CreatedAt, tp(a.HandledAt)); err != nil {
				return nil, err
			}
			continue
		}
		for _, old := range before.AbnormalPayments {
			if old.ID == a.ID && !same(old, a) {
				if _, err := tx.Exec(`UPDATE abnormal_payments SET status=?,handle_note=?,refund_id=?,handled_at=? WHERE id=?`,
					a.Status, a.HandleNote, a.RefundID, tp(a.HandledAt), a.ID); err != nil {
					return nil, err
				}
				break
			}
		}
	}

	if _, err := tx.Exec("UPDATE meta SET v=? WHERE k='seq'", strconv.FormatInt(after.Seq, 10)); err != nil {
		return nil, err
	}
	return changedProducts, nil
}

// Cache 暴露给 service 做读加速（Cache-Aside）。
func (s *MySQLStore) Cache() cache.Cache { return s.cache }

var _ StateStore = (*MySQLStore)(nil)
var _ StateStore = (*Store)(nil)
