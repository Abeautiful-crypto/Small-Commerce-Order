package service

import (
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"small-commerce-order/internal/model"
	"small-commerce-order/internal/store"
)

var (
	buyer1 = model.Actor{Role: model.RoleBuyer, UserID: "u001", Name: "张小买"}
	buyer2 = model.Actor{Role: model.RoleBuyer, UserID: "u002", Name: "李小购"}
	buyer3 = model.Actor{Role: model.RoleBuyer, UserID: "u003", Name: "王小单"}
	ops    = model.Actor{Role: model.RoleOps, UserID: "admin", Name: "运营专员"}
	wh     = model.Actor{Role: model.RoleWarehouse, UserID: "wh", Name: "仓库管理员"}
	cs     = model.Actor{Role: model.RoleCS, UserID: "cs", Name: "客服小花"}
)

type harness struct {
	svc     *Service
	st      store.StateStore
	nano    atomic.Int64
	advance func(time.Duration)
}

// mysqlBackendDSN 决定场景测试用的库。未显式指定 SERVICE_MYSQL_DSN 时，
// 从 MYSQL_TEST_DSN 自动派生独立库，避免 go test ./... 并行时与 store 包集成测试互相 Reset。
func mysqlBackendDSN() string {
	if dsn := os.Getenv("SERVICE_MYSQL_DSN"); dsn != "" {
		return dsn
	}
	base := os.Getenv("MYSQL_TEST_DSN")
	if base == "" {
		return ""
	}
	re := regexp.MustCompile(`/([A-Za-z0-9_]+)\?`)
	return re.ReplaceAllString(base, "/${1}_service?")
}

// newHarness 默认用零依赖内存存储；设置 MySQL 测试 DSN 时切换为 MySQL 真相源，
// 使同一套五大场景断言可以分别验证两种存储实现。
func newHarness(t *testing.T) *harness {
	t.Helper()
	loc := time.FixedZone("CST", 8*3600)
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, loc)
	h := &harness{}
	h.nano.Store(base.UnixNano())
	clock := func() time.Time { return time.Unix(0, h.nano.Load()).In(loc) }

	var st store.StateStore
	if dsn := mysqlBackendDSN(); dsn != "" {
		ms, err := store.OpenMySQL(store.MySQLConfig{
			DSN: dsn, MigrationsPath: "../../migrations", MaxOpenConns: 20, MaxIdleConns: 5,
		}, nil, clock)
		if err != nil {
			t.Fatalf("连接 MySQL 测试库失败：%v", err)
		}
		t.Cleanup(func() { _ = ms.Close() })
		if err := ms.Reset(); err != nil {
			t.Fatalf("重置 MySQL 测试库失败：%v", err)
		}
		st = ms
	} else {
		m, err := store.Open(filepath.Join(t.TempDir(), "state.json"), clock)
		if err != nil {
			t.Fatal(err)
		}
		st = m
	}
	h.st = st
	h.svc = New(st, "test-secret", clock)
	h.advance = func(d time.Duration) { h.nano.Add(int64(d)) }
	return h
}

func (h *harness) order(t *testing.T, id string) *model.Order {
	t.Helper()
	o, err := h.svc.GetOrder(ops, id)
	if err != nil {
		t.Fatalf("读取订单 %s 失败：%v", id, err)
	}
	return o
}

func (h *harness) available(t *testing.T, productID string) int {
	t.Helper()
	ps, err := h.svc.Products(ops)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.ID == productID {
			return p.Available
		}
	}
	t.Fatalf("商品 %s 不存在", productID)
	return -1
}

func (h *harness) pay(t *testing.T, buyer model.Actor, orderID string) string {
	t.Helper()
	intent, err := h.svc.CreatePaymentIntent(buyer, orderID)
	if err != nil {
		t.Fatalf("生成支付通知失败：%v", err)
	}
	res, err := h.svc.HandlePaymentNotify(*intent)
	if err != nil {
		t.Fatalf("投递支付通知失败：%v", err)
	}
	if res.Outcome != "ACCEPTED" {
		t.Fatalf("期望支付成功，实际 %s：%s", res.Outcome, res.Detail)
	}
	return intent.ChannelTxnID
}

func (h *harness) createPaidOrder(t *testing.T, buyer model.Actor, productID string, qty int) *model.Order {
	t.Helper()
	o, err := h.svc.CreateOrder(buyer, productID, qty)
	if err != nil {
		t.Fatalf("下单失败：%v", err)
	}
	h.pay(t, buyer, o.ID)
	return h.order(t, o.ID)
}

func assertStatus(t *testing.T, o *model.Order, want model.OrderStatus) {
	t.Helper()
	if o.Status != want {
		t.Fatalf("订单 %s 状态期望 %s，实际 %s", o.ID, want.Label(), o.Status.Label())
	}
}

func errCode(err error) string {
	if e, ok := err.(*BizError); ok {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return "OTHER"
}

// 场景 1：活动正常成交——运营上架 → 买家购买付款 → 仓库发货 → 买家查询 → 运营核查。
func TestScenario1_NormalDeal(t *testing.T) {
	h := newHarness(t)
	before := h.available(t, "P1") // 预置 4（总量 5，已有 1 单占用）

	if err := h.svc.SetOnSale(ops, "P2", false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CreateOrder(buyer2, "P2", 1); errCode(err) != CodeConflict {
		t.Fatalf("下架商品应不可购买，实际 %v", err)
	}
	if err := h.svc.SetOnSale(ops, "P2", true); err != nil {
		t.Fatal(err)
	}

	o, err := h.svc.CreateOrder(buyer2, "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if o.PriceFen != 10000 || o.AmountFen != 20000 {
		t.Fatalf("金额快照错误：单价 %d 分，总额 %d 分", o.PriceFen, o.AmountFen)
	}
	if got := h.available(t, "P1"); got != before-2 {
		t.Fatalf("下单即占用：可售应为 %d，实际 %d", before-2, got)
	}

	// 调价后订单价格快照保持不变
	_ = h.st.Update(func(st *model.State) error { st.Product("P1").PriceFen = 19900; return nil })
	if got := h.order(t, o.ID).PriceFen; got != 10000 {
		t.Fatalf("价格快照被篡改：%d", got)
	}

	h.pay(t, buyer2, o.ID)
	assertStatus(t, h.order(t, o.ID), model.StatusPendingShip)

	if _, err := h.svc.Ship(wh, o.ID, "顺丰速运", "SF001"); err != nil {
		t.Fatalf("登记发货失败：%v", err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusShipped)
	if _, err := h.svc.Ship(wh, o.ID, "", ""); errCode(err) != CodeConflict {
		t.Fatalf("重复发货必须被拒绝，实际 %v", err)
	}

	// 买家只能查自己的订单
	mine, err := h.svc.Orders(buyer2)
	if err != nil || len(mine) != 1 || mine[0].ID != o.ID {
		t.Fatalf("买家订单列表异常：%v %+v", err, mine)
	}
	if _, err := h.svc.GetOrder(buyer3, o.ID); errCode(err) != CodeForbidden {
		t.Fatalf("越权读取他人订单应被拒绝，实际 %v", err)
	}

	sum, err := h.svc.Summary(ops)
	if err != nil {
		t.Fatal(err)
	}
	if sum.ReceivedFen != 30000 || sum.NetFen != 30000 || sum.ShippedCount != 1 {
		t.Fatalf("对账汇总异常：%+v", sum)
	}
	if got := h.available(t, "P1"); got != before-2 {
		t.Fatalf("发货不得改变可售数量，实际 %d", got)
	}
}

// 场景 2：缺货与弃单——20 并发抢 5 件不超卖；关闭后释放的量可被其他买家买到。
func TestScenario2_NoOversellAndRelease(t *testing.T) {
	h := newHarness(t)
	_ = h.st.Update(func(st *model.State) error {
		p := st.Product("P1")
		p.Total, p.Available = 5, 5
		return nil
	})
	buyers := []model.Actor{buyer1, buyer2, buyer3}

	var wg sync.WaitGroup
	var mu sync.Mutex
	created := []*model.Order{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o, err := h.svc.CreateOrder(buyers[i%3], "P1", 1)
			if err == nil {
				mu.Lock()
				created = append(created, o)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(created) != 5 {
		t.Fatalf("20 并发抢 5 件：应成功 5 单，实际 %d 单", len(created))
	}
	if got := h.available(t, "P1"); got != 0 {
		t.Fatalf("并发后可售应为 0，实际 %d", got)
	}
	if got := h.available(t, "P1"); got < 0 {
		t.Fatalf("出现负库存（超卖）：%d", got)
	}

	// 弃单：取消一次释放 1 件，重复取消不再释放
	victim := created[0]
	owner := model.Actor{}
	_ = h.st.Update(func(st *model.State) error { return nil })
	for _, b := range buyers {
		o, err := h.svc.GetOrder(b, victim.ID)
		if err == nil {
			owner = b
			_ = o
			break
		}
	}
	if err := h.svc.CancelOrder(owner, victim.ID); err != nil {
		t.Fatalf("取消失败：%v", err)
	}
	if got := h.available(t, "P1"); got != 1 {
		t.Fatalf("取消应释放 1 件，实际可售 %d", got)
	}
	if err := h.svc.CancelOrder(owner, victim.ID); errCode(err) != CodeConflict {
		t.Fatalf("重复取消应被拒绝（且不重复释放），实际 %v", err)
	}
	if got := h.available(t, "P1"); got != 1 {
		t.Fatalf("重复取消导致重复释放：可售 %d", got)
	}
	if _, err := h.svc.CreateOrder(buyer3, "P1", 1); err != nil {
		t.Fatalf("释放的量应可被其他买家买到：%v", err)
	}
	if got := h.available(t, "P1"); got != 0 {
		t.Fatalf("复购后可售应为 0，实际 %d", got)
	}
}

// 场景 2b：10 分钟超时以服务端时间判断，重复关闭不重复释放。
func TestScenario2_TimeoutCloseIsIdempotent(t *testing.T) {
	h := newHarness(t)
	before := h.available(t, "P1")
	o, err := h.svc.CreateOrder(buyer1, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	h.advance(9 * time.Minute)
	if n, err := h.svc.CloseExpiredOrders(); err != nil || n != 0 {
		t.Fatalf("9 分钟不应关闭：n=%d err=%v", n, err)
	}
	h.advance(2 * time.Minute)
	if n, err := h.svc.CloseExpiredOrders(); err != nil || n != 1 {
		t.Fatalf("超过 10 分钟应关闭 1 单：n=%d err=%v", n, err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusClosed)
	if got := h.available(t, "P1"); got != before {
		t.Fatalf("超时关闭应释放 1 件，实际可售 %d（初始 %d）", got, before)
	}
	if n, err := h.svc.CloseExpiredOrders(); err != nil || n != 0 {
		t.Fatalf("重复关闭应无副作用：n=%d err=%v", n, err)
	}
	if got := h.available(t, "P1"); got != before {
		t.Fatalf("重复关闭导致重复释放：可售 %d", got)
	}
}

// 场景 3：售前退款——申请期间发货被挡、客服批准、首次退款失败、重试成功、库存恢复一致。
func TestScenario3_PresaleRefund(t *testing.T) {
	h := newHarness(t)
	before := h.available(t, "P1")
	o := h.createPaidOrder(t, buyer2, "P1", 1)
	if got := h.available(t, "P1"); got != before-1 {
		t.Fatalf("下单后可售错误：%d", got)
	}

	r, err := h.svc.ApplyRefund(buyer2, o.ID, "不想要了")
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusRefunding)
	if _, err := h.svc.Ship(wh, o.ID, "", ""); errCode(err) != CodeConflict {
		t.Fatalf("申请退款期间发货必须被挡住，实际 %v", err)
	}

	if _, err := h.svc.ReviewRefund(cs, r.ID, true, "核实无误"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.HandleRefundChannelNotify(cs, r.ID, false, "CR-FAIL-1"); err != nil {
		t.Fatal(err)
	}
	if got := h.available(t, "P1"); got != before-1 {
		t.Fatalf("渠道失败不得恢复库存，实际 %d", got)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusRefunding)

	if _, err := h.svc.RetryRefund(cs, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.HandleRefundChannelNotify(cs, r.ID, true, "CR-OK-1"); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusRefunded)
	if got := h.available(t, "P1"); got != before {
		t.Fatalf("售前退款成功应恢复库存至 %d，实际 %d", before, got)
	}
	// 重复退款成功通知：不重复退款、不重复恢复库存
	if _, err := h.svc.HandleRefundChannelNotify(cs, r.ID, true, "CR-OK-1"); err != nil {
		t.Fatal(err)
	}
	if got := h.available(t, "P1"); got != before {
		t.Fatalf("重复退款通知导致重复恢复库存：%d", got)
	}
	sum, _ := h.svc.Summary(ops)
	if sum.RefundedFen != 10000 {
		t.Fatalf("退款流水应只记一笔 10000 分，实际 %d", sum.RefundedFen)
	}
}

// 场景 3b：客服拒绝退款后退回待发货，可继续发货。
func TestScenario3_RejectRefundThenShip(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer1, "P2", 1)
	r, err := h.svc.ApplyRefund(buyer1, o.ID, "拍错了")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ReviewRefund(cs, r.ID, false, "已发货前可拒绝，商品无质量问题"); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusPendingShip)
	if _, err := h.svc.Ship(wh, o.ID, "", ""); err != nil {
		t.Fatalf("拒绝退款后应可继续发货：%v", err)
	}
	if _, err := h.svc.ApplyRefund(buyer1, o.ID, "再申请"); errCode(err) != CodeConflict {
		t.Fatalf("已发货不受理售前退款，实际 %v", err)
	}
}

// 场景 4：迟到支付——关闭后通知到达不复活订单，异常列表可查，退款后库存不增不减。
func TestScenario4_LatePayment(t *testing.T) {
	h := newHarness(t)
	before := h.available(t, "P1")
	o, err := h.svc.CreateOrder(buyer3, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.available(t, "P1"); got != before-1 {
		t.Fatalf("下单应占用 1 件，实际 %d", got)
	}
	if err := h.svc.ForceExpire(buyer3, o.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := h.svc.CloseExpiredOrders(); err != nil || n != 1 {
		t.Fatalf("超时关闭失败：n=%d err=%v", n, err)
	}
	released := h.available(t, "P1")
	if released != before {
		t.Fatalf("关闭应释放库存至 %d，实际 %d", before, released)
	}

	intent, err := h.svc.CreatePaymentIntent(buyer3, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.HandlePaymentNotify(*intent)
	if err != nil || res.Outcome != "LATE_PAYMENT" {
		t.Fatalf("迟到支付应进入异常通道，实际 %+v %v", res, err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusAbnormalPay)
	if got := h.available(t, "P1"); got != released {
		t.Fatalf("迟到支付不得重占库存，实际 %d", got)
	}

	abns, err := h.svc.AbnormalPayments(cs)
	if err != nil || len(abns) != 1 || abns[0].Status != model.AbnormalPending {
		t.Fatalf("异常支付列表异常：%+v %v", abns, err)
	}
	r, err := h.svc.HandleAbnormalPayment(cs, abns[0].ID, "核实为重复扣款，原路退回")
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != model.RefundSourceLate {
		t.Fatalf("来源标记应为迟到支付，实际 %s", r.Source)
	}
	if _, err := h.svc.HandleRefundChannelNotify(cs, r.ID, true, "CR-LATE-1"); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusRefunded)
	if got := h.available(t, "P1"); got != released {
		t.Fatalf("迟到支付退款不得恢复库存（会超卖），实际 %d（应为 %d）", got, released)
	}
	sum, _ := h.svc.Summary(ops)
	if sum.ReceivedFen != 10000 || sum.RefundedFen != 10000 {
		t.Fatalf("迟到支付不应产生收款，退款只一笔：%+v", sum)
	}
	abns, _ = h.svc.AbnormalPayments(ops)
	if abns[0].Status != model.AbnormalRefunded {
		t.Fatal("异常记录应标记为已处理，且记录本身不能被抹掉")
	}
}

// 场景 5：并发退款与发货只形成一种结果；重复通知只处理一次；越权被明确拒绝。
func TestScenario5_RaceIdempotencyPermission(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer2, "P2", 1)

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := map[string]error{}
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := h.svc.ApplyRefund(buyer2, o.ID, "申请退款")
		mu.Lock()
		results["refund"] = err
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		_, err := h.svc.Ship(wh, o.ID, "", "")
		mu.Lock()
		results["ship"] = err
		mu.Unlock()
	}()
	wg.Wait()
	oneSuccess := (results["refund"] == nil) != (results["ship"] == nil)
	if !oneSuccess {
		t.Fatalf("退款与发货竞争必须只有一个成功，实际 refund=%v ship=%v", results["refund"], results["ship"])
	}
	st := h.order(t, o.ID)
	if st.Status != model.StatusRefunding && st.Status != model.StatusShipped {
		t.Fatalf("最终状态应为退款中或已发货，实际 %s", st.Status.Label())
	}
	shipments, _ := h.svc.Shipments(ops)
	if len(shipments) > 0 && st.Status == model.StatusRefunding {
		t.Fatal("退款赢时不应存在发货记录")
	}

	// 重复支付通知：同一渠道交易号只处理一次
	o2, _ := h.svc.CreateOrder(buyer1, "P2", 1)
	intent, err := h.svc.CreatePaymentIntent(buyer1, o2.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := h.svc.HandlePaymentNotify(*intent)
	second, _ := h.svc.HandlePaymentNotify(*intent)
	if first.Outcome != "ACCEPTED" || second.Outcome != "DUPLICATE" {
		t.Fatalf("重复通知应被判 DUPLICATE，实际 %s / %s", first.Outcome, second.Outcome)
	}
	st2, _ := h.svc.State(ops)
	recv := 0
	for _, l := range st2.Ledger {
		if l.Type == "收款" && l.OrderID == o2.ID {
			recv++
		}
	}
	if recv != 1 {
		t.Fatalf("重复通知导致重复收款：收款流水 %d 条", recv)
	}

	// 异常通知结果可查询（验签失败 / 金额不符 / 未知订单 / 串单）
	tampered := *intent
	tampered.AmountFen = 1
	if r, _ := h.svc.HandlePaymentNotify(tampered); r.Outcome != "BAD_SIGNATURE" {
		t.Fatalf("篡改金额应验签失败，实际 %s", r.Outcome)
	}
	mismatch := *intent
	mismatch.AmountFen = 999999
	mismatch.Signature = h.svc.sign(mismatch.ChannelTxnID, mismatch.OrderID, mismatch.AmountFen, mismatch.Status, mismatch.Nonce)
	if r, _ := h.svc.HandlePaymentNotify(mismatch); r.Outcome != "AMOUNT_MISMATCH" {
		t.Fatalf("金额不符应被拒绝，实际 %s", r.Outcome)
	}
	unknown := *intent
	unknown.ChannelTxnID = "TXN-UNKNOWN"
	unknown.OrderID = "NO-NOT-EXIST"
	unknown.Signature = h.svc.sign(unknown.ChannelTxnID, unknown.OrderID, unknown.AmountFen, unknown.Status, unknown.Nonce)
	if r, _ := h.svc.HandlePaymentNotify(unknown); r.Outcome != "UNKNOWN_ORDER" {
		t.Fatalf("未知订单应被记录，实际 %s", r.Outcome)
	}
	crossed := *intent
	crossed.ChannelTxnID = "TXN-SEED-0001" // 已归属预置订单
	crossed.Signature = h.svc.sign(crossed.ChannelTxnID, crossed.OrderID, crossed.AmountFen, crossed.Status, crossed.Nonce)
	if r, _ := h.svc.HandlePaymentNotify(crossed); r.Outcome != "TXN_CONFLICT" {
		t.Fatalf("交易号串单应被拒绝，实际 %s", r.Outcome)
	}
	results2, err := h.svc.NotifyResults(ops)
	if err != nil || len(results2) < 6 {
		t.Fatalf("异常通知处理结果必须可查询，实际 %d 条 %v", len(results2), err)
	}

	// 越权：四类角色边界
	if err := h.svc.SetOnSale(buyer1, "P1", false); errCode(err) != CodeForbidden {
		t.Fatalf("买家不得上下架，实际 %v", err)
	}
	if _, err := h.svc.Ship(ops, o.ID, "", ""); errCode(err) != CodeForbidden {
		t.Fatalf("运营不得登记发货，实际 %v", err)
	}
	if err := h.svc.CancelOrder(buyer1, o.ID); errCode(err) != CodeForbidden {
		t.Fatalf("买家不得操作他人订单，实际 %v", err)
	}
	if _, err := h.svc.Summary(buyer1); errCode(err) != CodeForbidden {
		t.Fatalf("买家不得查看运营对账，实际 %v", err)
	}
	if _, err := h.svc.Ledger(cs); errCode(err) != CodeForbidden {
		t.Fatalf("客服不得查看运营流水，实际 %v", err)
	}
}

// 渠道退款结果必须由客服/运营触发：买家不得自助确认退款成功并恢复库存。
func TestRule_ChannelNotifyPermission(t *testing.T) {
	h := newHarness(t)
	before := h.available(t, "P2")
	o := h.createPaidOrder(t, buyer2, "P2", 1)
	r, err := h.svc.ApplyRefund(buyer2, o.ID, "不想要了")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ReviewRefund(cs, r.ID, true, "核实无误"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.HandleRefundChannelNotify(buyer2, r.ID, true, "CR-FORBIDDEN"); errCode(err) != CodeForbidden {
		t.Fatalf("买家不得确认渠道退款结果，实际 %v", err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusRefunding)
	if got := h.available(t, "P2"); got != before-1 {
		t.Fatalf("越权尝试不得恢复库存，期望 %d，实际 %d", before-1, got)
	}
	if _, err := h.svc.HandleRefundChannelNotify(cs, r.ID, true, "CR-OK"); err != nil {
		t.Fatalf("客服应可触发渠道退款结果：%v", err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusRefunded)
}

// R8：流水只增不改——异常记录不会因状态变更消失。
func TestRule8_LedgerAppendOnly(t *testing.T) {
	h := newHarness(t)
	st0, _ := h.svc.State(ops)
	n := len(st0.Ledger)
	r, err := h.svc.ApplyRefund(buyer1, "NO20260001", "预置订单退款")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ReviewRefund(cs, r.ID, false, "驳回"); err != nil {
		t.Fatal(err)
	}
	st1, _ := h.svc.State(ops)
	if len(st1.Ledger) <= n {
		t.Fatal("流水必须只增不改")
	}
	for i := 0; i < n; i++ {
		if st0.Ledger[i].ID != st1.Ledger[i].ID || st0.Ledger[i].Note != st1.Ledger[i].Note {
			t.Fatalf("第 %d 条流水被修改，违反只增不改", i)
		}
	}
}
