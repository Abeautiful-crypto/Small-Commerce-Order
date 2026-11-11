package service

import (
	"testing"
	"time"

	"small-commerce-order/internal/model"
)

// 本文件是 order.go 的细粒度分支单测；service_test.go 保留五大场景回归。
// 复用其中的 newHarness / errCode / h.available 等 helper。

func TestUnit_CreateOrderValidation(t *testing.T) {
	h := newHarness(t)
	for _, qty := range []int{-1, 0, 3} {
		if _, err := h.svc.CreateOrder(buyer1, "P1", qty); errCode(err) != CodeBadRequest {
			t.Fatalf("数量 %d 应被参数校验拒绝，实际 %v", qty, err)
		}
	}
	if _, err := h.svc.CreateOrder(buyer1, "NO-SUCH", 1); errCode(err) != CodeNotFound {
		t.Fatalf("未知商品应返回 NOT_FOUND，实际 %v", err)
	}
	if _, err := h.svc.CreateOrder(ops, "P1", 1); errCode(err) != CodeForbidden {
		t.Fatalf("非买家下单应被拒绝，实际 %v", err)
	}
	if err := h.svc.SetOnSale(ops, "P1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CreateOrder(buyer1, "P1", 1); errCode(err) != CodeConflict {
		t.Fatalf("下架商品不可购买，实际 %v", err)
	}
	if err := h.svc.SetOnSale(ops, "P1", true); err != nil {
		t.Fatal(err)
	}
	// 人为把可售压到 1，验证「可售不足」分支（原子条件更新的前置判定）
	if err := h.st.Update(func(st *model.State) error {
		st.Product("P1").Available = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CreateOrder(buyer1, "P1", 2); errCode(err) != CodeConflict {
		t.Fatalf("可售不足应返回 CONFLICT，实际 %v", err)
	}
	if got := h.available(t, "P1"); got != 1 {
		t.Fatalf("被拒绝的下单不得改动库存，期望 1，实际 %d", got)
	}
}

func TestUnit_SetOnSaleGuards(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.SetOnSale(buyer1, "P1", false); errCode(err) != CodeForbidden {
		t.Fatalf("买家不得上下架，实际 %v", err)
	}
	if err := h.svc.SetOnSale(ops, "NO-SUCH", false); errCode(err) != CodeNotFound {
		t.Fatalf("未知商品应返回 NOT_FOUND，实际 %v", err)
	}
	if err := h.svc.SetOnSale(ops, "P2", false); err != nil {
		t.Fatal(err)
	}
	ps, err := h.svc.Products(buyer1)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.ID == "P2" && p.OnSale {
			t.Fatal("下架未生效")
		}
	}
}

func TestUnit_CancelAndCloseIdempotency(t *testing.T) {
	h := newHarness(t)
	before := h.available(t, "P1")
	o, err := h.svc.CreateOrder(buyer2, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.available(t, "P1"); got != before-1 {
		t.Fatalf("下单应占用 1 件，期望 %d，实际 %d", before-1, got)
	}
	if err := h.svc.CancelOrder(buyer2, o.ID); err != nil {
		t.Fatal(err)
	}
	if got := h.available(t, "P1"); got != before {
		t.Fatalf("取消应释放 1 件，期望 %d，实际 %d", before, got)
	}
	if err := h.svc.CancelOrder(buyer2, o.ID); errCode(err) != CodeConflict {
		t.Fatalf("重复取消应被拒绝，实际 %v", err)
	}
	if got := h.available(t, "P1"); got != before {
		t.Fatalf("重复取消不得重复释放，期望 %d，实际 %d", before, got)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusClosed)
}

func TestUnit_PaymentNotifyRejections(t *testing.T) {
	h := newHarness(t)
	o, err := h.svc.CreateOrder(buyer2, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := h.svc.CreatePaymentIntent(buyer2, o.ID)
	if err != nil {
		t.Fatal(err)
	}

	bad := *intent
	bad.Signature = "invalid"
	mismatch := *intent
	mismatch.AmountFen = 1
	mismatch.Signature = h.svc.sign(mismatch.ChannelTxnID, mismatch.OrderID, mismatch.AmountFen, mismatch.Status, mismatch.Nonce)
	unknown := *intent
	unknown.ChannelTxnID = "TXN-UNIT-UNKNOWN"
	unknown.OrderID = "NO-NOT-EXIST"
	unknown.Signature = h.svc.sign(unknown.ChannelTxnID, unknown.OrderID, unknown.AmountFen, unknown.Status, unknown.Nonce)
	crossed := *intent
	crossed.ChannelTxnID = "TXN-SEED-0001" // 已归属预置订单
	crossed.Signature = h.svc.sign(crossed.ChannelTxnID, crossed.OrderID, crossed.AmountFen, crossed.Status, crossed.Nonce)
	notSuccess := *intent
	notSuccess.Status = "FAIL"
	notSuccess.Signature = h.svc.sign(notSuccess.ChannelTxnID, notSuccess.OrderID, notSuccess.AmountFen, notSuccess.Status, notSuccess.Nonce)

	want := []string{"BAD_SIGNATURE", "AMOUNT_MISMATCH", "UNKNOWN_ORDER", "TXN_CONFLICT", "IGNORED"}
	for i, in := range []model.PaymentIntent{bad, mismatch, unknown, crossed, notSuccess} {
		res, err := h.svc.HandlePaymentNotify(in)
		if err != nil {
			t.Fatalf("第 %d 笔通知不应返回系统错误：%v", i, err)
		}
		if res.Outcome != want[i] {
			t.Fatalf("第 %d 笔通知期望 %s，实际 %s（%s）", i, want[i], res.Outcome, res.Detail)
		}
	}
	results, err := h.svc.NotifyResults(ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(want) {
		t.Fatalf("被拒绝的通知必须全部落流水可查询，期望 %d 条，实际 %d 条", len(want), len(results))
	}
	assertStatus(t, h.order(t, o.ID), model.StatusPendingPay)
	st, _ := h.svc.State(ops)
	if len(st.Payments) != 1 {
		t.Fatalf("异常通知不得产生收款，期望 1 条（预置），实际 %d 条", len(st.Payments))
	}
}

func TestUnit_PaymentNotifyIgnoredOnShipped(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer2, "P1", 1)
	if _, err := h.svc.Ship(wh, o.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	intent := model.PaymentIntent{
		ChannelTxnID: "TXN-UNIT-TERMINAL", OrderID: o.ID, AmountFen: o.AmountFen,
		Status: "SUCCESS", Nonce: "N-1",
	}
	intent.Signature = h.svc.sign(intent.ChannelTxnID, intent.OrderID, intent.AmountFen, intent.Status, intent.Nonce)
	res, err := h.svc.HandlePaymentNotify(intent)
	if err != nil || res.Outcome != "IGNORED" {
		t.Fatalf("已发货订单的通知应判 IGNORED，实际 %+v %v", res, err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusShipped)
	st, _ := h.svc.State(ops)
	if len(st.Payments) != 2 {
		t.Fatalf("不得重复收款，期望 2 条，实际 %d 条", len(st.Payments))
	}
}

func TestUnit_AutoCloseBeforeLateNotify(t *testing.T) {
	h := newHarness(t)
	before := h.available(t, "P1")
	o, err := h.svc.CreateOrder(buyer3, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	h.advance(11 * time.Minute) // 超过服务端 10 分钟未付款期限
	intent, err := h.svc.CreatePaymentIntent(buyer3, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.HandlePaymentNotify(*intent)
	if err != nil || res.Outcome != "LATE_PAYMENT" {
		t.Fatalf("超时后通知应进入迟到支付通道，实际 %+v %v", res, err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusAbnormalPay)
	if got := h.available(t, "P1"); got != before {
		t.Fatalf("关闭释放一次且迟到通知不得重占，期望 %d，实际 %d", before, got)
	}
}

func TestUnit_ForceExpireGuards(t *testing.T) {
	h := newHarness(t)
	o, err := h.svc.CreateOrder(buyer2, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ForceExpire(buyer3, o.ID); errCode(err) != CodeForbidden {
		t.Fatalf("非订单归属人不得模拟超时，实际 %v", err)
	}
	if err := h.svc.ForceExpire(buyer2, "NO-NOT-EXIST"); errCode(err) != CodeNotFound {
		t.Fatalf("未知订单应返回 NOT_FOUND，实际 %v", err)
	}
	paid := h.createPaidOrder(t, buyer2, "P1", 1)
	if err := h.svc.ForceExpire(buyer2, paid.ID); errCode(err) != CodeConflict {
		t.Fatalf("已付款订单不可模拟超时，实际 %v", err)
	}
}

func TestUnit_CloseExpiredNoSideEffect(t *testing.T) {
	h := newHarness(t)
	st0, err := h.svc.State(ops)
	if err != nil {
		t.Fatal(err)
	}
	n, err := h.svc.CloseExpiredOrders()
	if err != nil || n != 0 {
		t.Fatalf("无过期订单时不应有关闭动作：n=%d err=%v", n, err)
	}
	st1, _ := h.svc.State(ops)
	if len(st1.Ledger) != len(st0.Ledger) {
		t.Fatalf("空扫描不得产生流水：%d → %d", len(st0.Ledger), len(st1.Ledger))
	}
}
