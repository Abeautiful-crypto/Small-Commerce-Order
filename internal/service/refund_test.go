package service

import (
	"testing"

	"small-commerce-order/internal/model"
)

// 本文件是 refund.go（含 Ship / 异常支付处理）的细粒度分支单测。
// 复用 service_test.go 的 newHarness / errCode / h.createPaidOrder 等 helper。

func (h *harness) state(t *testing.T) *model.State {
	t.Helper()
	st, err := h.svc.State(ops)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestUnit_ApplyRefundGuards(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer2, "P2", 1)
	if _, err := h.svc.ApplyRefund(buyer3, o.ID, "x"); errCode(err) != CodeForbidden {
		t.Fatalf("非订单归属人不得申请退款，实际 %v", err)
	}
	if _, err := h.svc.ApplyRefund(ops, o.ID, "x"); errCode(err) != CodeForbidden {
		t.Fatalf("非买家不得申请退款，实际 %v", err)
	}
	if _, err := h.svc.ApplyRefund(buyer2, "NO-NOT-EXIST", "x"); errCode(err) != CodeNotFound {
		t.Fatalf("未知订单应返回 NOT_FOUND，实际 %v", err)
	}
	r, err := h.svc.ApplyRefund(buyer2, o.ID, "不想要了")
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != model.RefundSourcePresale || r.Status != model.RefundPending {
		t.Fatalf("售前退款单初始值异常：%+v", r)
	}
	if _, err := h.svc.ApplyRefund(buyer2, o.ID, "again"); errCode(err) != CodeConflict {
		t.Fatalf("重复申请退款应被拒绝，实际 %v", err)
	}
	pending, _ := h.svc.CreateOrder(buyer2, "P2", 1)
	if _, err := h.svc.ApplyRefund(buyer2, pending.ID, "x"); errCode(err) != CodeConflict {
		t.Fatalf("待付款订单不得申请退款，实际 %v", err)
	}
}

func TestUnit_ReviewRefundGuards(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer2, "P2", 1)
	r, _ := h.svc.ApplyRefund(buyer2, o.ID, "reason")
	if _, err := h.svc.ReviewRefund(buyer2, r.ID, true, "x"); errCode(err) != CodeForbidden {
		t.Fatalf("非客服不得审核，实际 %v", err)
	}
	if _, err := h.svc.ReviewRefund(cs, "RF-NOT-EXIST", true, "x"); errCode(err) != CodeNotFound {
		t.Fatalf("未知退款单应返回 NOT_FOUND，实际 %v", err)
	}
	approved, err := h.svc.ReviewRefund(cs, r.ID, true, "核实无误")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != model.RefundAwait || approved.Attempts != 1 || approved.ReviewNote != "核实无误" {
		t.Fatalf("同意退款后状态异常：%+v", approved)
	}
	if _, err := h.svc.ReviewRefund(cs, r.ID, false, "x"); errCode(err) != CodeConflict {
		t.Fatalf("重复审核应被拒绝，实际 %v", err)
	}
}

func TestUnit_RejectRefundRecordsReason(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer2, "P2", 1)
	r, _ := h.svc.ApplyRefund(buyer2, o.ID, "reason")
	rejected, err := h.svc.ReviewRefund(cs, r.ID, false, "商品无质量问题")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != model.RefundRejected || rejected.ReviewNote != "商品无质量问题" {
		t.Fatalf("拒绝原因未记录：%+v", rejected)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusPendingShip)
	if _, err := h.svc.Ship(wh, o.ID, "", ""); err != nil {
		t.Fatalf("拒绝后应可继续发货：%v", err)
	}
}

func TestUnit_ChannelNotifyIdempotencyAndConflict(t *testing.T) {
	h := newHarness(t)
	before := h.available(t, "P2")

	o := h.createPaidOrder(t, buyer2, "P2", 1)
	r, _ := h.svc.ApplyRefund(buyer2, o.ID, "reason-1")
	h.svc.ReviewRefund(cs, r.ID, true, "ok")
	first, err := h.svc.HandleRefundChannelNotify(cs, r.ID, true, "CR-UNIT-1")
	if err != nil || first.Status != model.RefundSucceeded {
		t.Fatalf("渠道确认成功失败：%+v %v", first, err)
	}
	if got := h.available(t, "P2"); got != before {
		t.Fatalf("售前退款应恢复库存至 %d，实际 %d", before, got)
	}
	// 重复成功通知：不重复退款、不重复恢复库存
	second, err := h.svc.HandleRefundChannelNotify(cs, r.ID, true, "CR-UNIT-1")
	if err != nil || second.Status != model.RefundSucceeded {
		t.Fatalf("重复通知应幂等返回：%+v %v", second, err)
	}
	if got := h.available(t, "P2"); got != before {
		t.Fatalf("重复通知导致重复恢复库存：%d", got)
	}
	st := h.state(t)
	refundFlow := 0
	for _, l := range st.Ledger {
		if l.Type == "退款" {
			refundFlow++
		}
	}
	if refundFlow != 1 {
		t.Fatalf("退款流水应只有 1 条，实际 %d 条", refundFlow)
	}

	// 同一渠道退款号不得归属两单
	o2 := h.createPaidOrder(t, buyer2, "P2", 1)
	r2, _ := h.svc.ApplyRefund(buyer2, o2.ID, "reason-2")
	h.svc.ReviewRefund(cs, r2.ID, true, "ok")
	if _, err := h.svc.HandleRefundChannelNotify(cs, r2.ID, true, "CR-UNIT-1"); errCode(err) != CodeConflict {
		t.Fatalf("渠道退款号串单应被拒绝，实际 %v", err)
	}
	if got := h.state(t).Refund(r2.ID).Status; got != model.RefundAwait {
		t.Fatalf("被拒绝的通知不得改动退款单状态，实际 %s", got)
	}
}

func TestUnit_ChannelNotifyPermissionAndState(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer2, "P2", 1)
	r, _ := h.svc.ApplyRefund(buyer2, o.ID, "reason")
	h.svc.ReviewRefund(cs, r.ID, true, "ok")

	if _, err := h.svc.HandleRefundChannelNotify(buyer2, r.ID, true, "CR-X"); errCode(err) != CodeForbidden {
		t.Fatalf("买家不得确认渠道退款结果，实际 %v", err)
	}
	if _, err := h.svc.HandleRefundChannelNotify(cs, "RF-NOT-EXIST", true, "CR-X"); errCode(err) != CodeNotFound {
		t.Fatalf("未知退款单应返回 NOT_FOUND，实际 %v", err)
	}
	// 未进入 AWAITING 的退款单不得接收渠道结果
	r3, _ := func() (*model.Refund, error) {
		o3 := h.createPaidOrder(t, buyer2, "P2", 1)
		return h.svc.ApplyRefund(buyer2, o3.ID, "reason-3")
	}()
	if _, err := h.svc.HandleRefundChannelNotify(cs, r3.ID, true, "CR-Y"); errCode(err) != CodeConflict {
		t.Fatalf("未审核的退款单不得接收渠道结果，实际 %v", err)
	}
}

func TestUnit_RetryRefundGuards(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer2, "P2", 1)
	r, _ := h.svc.ApplyRefund(buyer2, o.ID, "reason")
	h.svc.ReviewRefund(cs, r.ID, true, "ok")

	if _, err := h.svc.RetryRefund(cs, r.ID); errCode(err) != CodeConflict {
		t.Fatalf("非 FAILED 状态不得重试，实际 %v", err)
	}
	if _, err := h.svc.RetryRefund(buyer2, r.ID); errCode(err) != CodeForbidden {
		t.Fatalf("非客服不得重试，实际 %v", err)
	}
	failed, err := h.svc.HandleRefundChannelNotify(cs, r.ID, false, "CR-FAIL")
	if err != nil || failed.Status != model.RefundFailed {
		t.Fatalf("渠道失败应保留待处理：%+v %v", failed, err)
	}
	retried, err := h.svc.RetryRefund(cs, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != model.RefundAwait || retried.Attempts != 2 {
		t.Fatalf("重试后状态异常：%+v", retried)
	}
	if _, err := h.svc.HandleRefundChannelNotify(cs, r.ID, true, "CR-OK"); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusRefunded)
}

func TestUnit_HandleAbnormalPaymentGuards(t *testing.T) {
	h := newHarness(t)
	o, err := h.svc.CreateOrder(buyer3, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ForceExpire(buyer3, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CloseExpiredOrders(); err != nil {
		t.Fatal(err)
	}
	intent, err := h.svc.CreatePaymentIntent(buyer3, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.HandlePaymentNotify(*intent); err != nil {
		t.Fatal(err)
	}
	abn, err := h.svc.AbnormalPayments(cs)
	if err != nil || len(abn) != 1 {
		t.Fatalf("异常支付列表异常：%+v %v", abn, err)
	}
	aid := abn[0].ID

	if _, err := h.svc.HandleAbnormalPayment(buyer3, aid, "x"); errCode(err) != CodeForbidden {
		t.Fatalf("非客服不得处理异常支付，实际 %v", err)
	}
	if _, err := h.svc.HandleAbnormalPayment(cs, "ABN-NOT-EXIST", "x"); errCode(err) != CodeNotFound {
		t.Fatalf("未知异常记录应返回 NOT_FOUND，实际 %v", err)
	}
	r, err := h.svc.HandleAbnormalPayment(cs, aid, "核实为重复扣款")
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != model.RefundSourceLate || r.Status != model.RefundAwait {
		t.Fatalf("迟到支付退款单来源/状态异常：%+v", r)
	}
	if _, err := h.svc.HandleAbnormalPayment(cs, aid, "again"); errCode(err) != CodeConflict {
		t.Fatalf("重复处理异常支付应被拒绝，实际 %v", err)
	}
	before := h.available(t, "P1")
	if _, err := h.svc.HandleRefundChannelNotify(cs, r.ID, true, "CR-LATE"); err != nil {
		t.Fatal(err)
	}
	if got := h.available(t, "P1"); got != before {
		t.Fatalf("迟到支付退款不得恢复库存：%d → %d", before, got)
	}
	abn2, _ := h.svc.AbnormalPayments(cs)
	if abn2[0].Status != model.AbnormalRefunded || abn2[0].HandledAt == nil {
		t.Fatalf("异常记录应标记为已处理且保留：%+v", abn2[0])
	}
}

func TestUnit_ShipGuards(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer2, "P2", 1)
	if _, err := h.svc.Ship(buyer2, o.ID, "", ""); errCode(err) != CodeForbidden {
		t.Fatalf("非仓库不得登记发货，实际 %v", err)
	}
	if _, err := h.svc.Ship(wh, "NO-NOT-EXIST", "", ""); errCode(err) != CodeNotFound {
		t.Fatalf("未知订单应返回 NOT_FOUND，实际 %v", err)
	}
	sh, err := h.svc.Ship(wh, o.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if sh.Carrier != "顺丰速运" || sh.TrackingNo == "" {
		t.Fatalf("默认承运方/运单号未生成：%+v", sh)
	}
	if _, err := h.svc.Ship(wh, o.ID, "", ""); errCode(err) != CodeConflict {
		t.Fatalf("重复发货应被拒绝，实际 %v", err)
	}
	assertStatus(t, h.order(t, o.ID), model.StatusShipped)
	if _, err := h.svc.ApplyRefund(buyer2, o.ID, "x"); errCode(err) != CodeConflict {
		t.Fatalf("已发货不受理售前退款，实际 %v", err)
	}

	o2 := h.createPaidOrder(t, buyer2, "P2", 1)
	if _, err := h.svc.ApplyRefund(buyer2, o2.ID, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Ship(wh, o2.ID, "", ""); errCode(err) != CodeConflict {
		t.Fatalf("退款中不得发货，实际 %v", err)
	}
}
