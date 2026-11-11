package service

import (
	"fmt"
	"time"

	"small-commerce-order/internal/model"
)

func activeRefund(st *model.State, orderID string) *model.Refund {
	for _, r := range st.Refunds {
		if r.OrderID == orderID && (r.Status == model.RefundPending || r.Status == model.RefundAwait || r.Status == model.RefundFailed) {
			return r
		}
	}
	return nil
}

// ApplyRefund R5：买家对已付款未发货订单申请整单退款；迁移本身就是互斥锁，
// 状态变为退款中后，仓库发货会因状态机守卫被挡住。
func (s *Service) ApplyRefund(actor model.Actor, orderID, reason string) (*model.Refund, error) {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return nil, err
	}
	var out *model.Refund
	err := s.st.Update(func(st *model.State) error {
		o := st.Order(orderID)
		if o == nil {
			return notFound("订单不存在")
		}
		if err := requireOwner(actor, o); err != nil {
			return err
		}
		if o.Status != model.StatusPendingShip {
			return conflict("仅「待发货」订单可申请售前退款；已发货不受理")
		}
		if activeRefund(st, orderID) != nil {
			return conflict("该订单已有待处理退款单")
		}
		now := s.now()
		if err := model.Transition(o, model.StatusRefunding, now); err != nil {
			return conflict(err.Error())
		}
		r := &model.Refund{
			ID: st.NextID("RF-"), OrderID: orderID, Source: model.RefundSourcePresale,
			AmountFen: o.AmountFen, Status: model.RefundPending, Reason: reason,
			CreatedAt: now, UpdatedAt: now,
		}
		st.Refunds = append(st.Refunds, r)
		o.RefundID = r.ID
		st.AppendLedger("退款申请", r.AmountFen, orderID, r.ID, "售前退款申请，暂停发货", now)
		out = r
		return nil
	})
	return out, err
}

// ReviewRefund 客服审核：同意→等待渠道确认；拒绝→退回待发货（可继续发货）。
func (s *Service) ReviewRefund(actor model.Actor, refundID string, approve bool, note string) (*model.Refund, error) {
	if err := requireRole(actor, model.RoleCS); err != nil {
		return nil, err
	}
	var out *model.Refund
	err := s.st.Update(func(st *model.State) error {
		r := st.Refund(refundID)
		if r == nil {
			return notFound("退款单不存在")
		}
		if r.Status != model.RefundPending {
			return conflict("该退款单不处于待审核状态")
		}
		o := st.Order(r.OrderID)
		if o == nil || o.Status != model.StatusRefunding {
			return conflict("订单不处于退款中，无法审核")
		}
		now := s.now()
		r.ReviewNote = note
		r.UpdatedAt = now
		if approve {
			r.Status = model.RefundAwait
			r.Attempts++
			st.AppendLedger("退款审核", r.AmountFen, o.ID, r.ID, "客服同意退款，等待渠道确认", now)
		} else {
			r.Status = model.RefundRejected
			if err := model.Transition(o, model.StatusPendingShip, now); err != nil {
				return conflict(err.Error())
			}
			st.AppendLedger("退款审核", r.AmountFen, o.ID, r.ID, "客服拒绝退款（"+note+"），恢复可发货", now)
		}
		out = r
		return nil
	})
	return out, err
}

// HandleRefundChannelNotify R6：渠道确认成功才算退款成功；失败保留待处理可重试；
// 幂等靠退款单状态 + 渠道退款号唯一；恢复库存只看退款单来源（售前恢复、迟到不恢复）。
func (s *Service) HandleRefundChannelNotify(actor model.Actor, refundID string, success bool, channelRefundID string) (*model.Refund, error) {
	// 演示态：模拟渠道退款结果由客服或运营触发，必须校验真实身份。
	// 生产环境应改为带渠道签名的回调（与支付通知对称），不能沿用登录身份。
	if err := requireRole(actor, model.RoleCS, model.RoleOps); err != nil {
		return nil, err
	}
	var out *model.Refund
	err := s.st.Update(func(st *model.State) error {
		r := st.Refund(refundID)
		if r == nil {
			return notFound("退款单不存在")
		}
		now := s.now()
		if r.Status == model.RefundSucceeded {
			st.AppendLedger("通知结果", r.AmountFen, r.OrderID, channelRefundID, "重复的退款成功通知，不重复退款、不重复恢复库存", now)
			out = r
			return nil
		}
		if r.Status != model.RefundAwait {
			return conflict("退款单当前不等待渠道确认，无法处理通知")
		}
		if channelRefundID != "" {
			for _, other := range st.Refunds {
				if other.ID != r.ID && other.ChannelRefundID == channelRefundID {
					return conflict("渠道退款号已归属其它退款单，拒绝串单")
				}
			}
		}
		o := st.Order(r.OrderID)
		if o == nil {
			return notFound("订单不存在")
		}
		if !success {
			r.Status = model.RefundFailed
			r.UpdatedAt = now
			st.AppendLedger("退款失败", r.AmountFen, o.ID, r.ID, "渠道退款失败，退款单保留待处理，可重试", now)
			out = r
			return nil
		}
		r.Status = model.RefundSucceeded
		r.ChannelRefundID = channelRefundID
		r.UpdatedAt = now
		if err := model.Transition(o, model.StatusRefunded, now); err != nil {
			return conflict(err.Error())
		}
		note := "渠道确认退款成功"
		if r.Source == model.RefundSourcePresale {
			if p := st.Product(o.ProductID); p != nil {
				p.Available += o.Qty // 售前退款恢复库存
			}
			note += "，来源=售前，恢复库存 " + fmt.Sprint(o.Qty) + " 件"
		} else {
			note += "，来源=迟到支付，订单关闭时已释放过，不再恢复库存"
		}
		st.AppendLedger("退款", r.AmountFen, o.ID, r.ID, note, now)
		if a := st.AbnormalByOrder(o.ID); a != nil && a.Status == model.AbnormalPending {
			a.Status = model.AbnormalRefunded
			a.HandledAt = &now
			a.HandleNote = "客服核实迟到支付后完成退款（库存不恢复）"
		}
		out = r
		return nil
	})
	return out, err
}

// RetryRefund 渠道失败后重试：状态回到等待渠道确认。
func (s *Service) RetryRefund(actor model.Actor, refundID string) (*model.Refund, error) {
	if err := requireRole(actor, model.RoleCS); err != nil {
		return nil, err
	}
	var out *model.Refund
	err := s.st.Update(func(st *model.State) error {
		r := st.Refund(refundID)
		if r == nil {
			return notFound("退款单不存在")
		}
		if r.Status != model.RefundFailed {
			return conflict("仅渠道失败的退款单可重试")
		}
		r.Status = model.RefundAwait
		r.Attempts++
		r.ChannelRefundID = ""
		r.UpdatedAt = s.now()
		st.AppendLedger("退款重试", r.AmountFen, r.OrderID, r.ID,
			fmt.Sprintf("第 %d 次向渠道发起退款", r.Attempts), r.UpdatedAt)
		out = r
		return nil
	})
	return out, err
}

// HandleAbnormalPayment 客服核实迟到支付后发起退款：退款单来源固定为 late，绝不恢复库存。
func (s *Service) HandleAbnormalPayment(actor model.Actor, abnormalID, note string) (*model.Refund, error) {
	if err := requireRole(actor, model.RoleCS); err != nil {
		return nil, err
	}
	var out *model.Refund
	err := s.st.Update(func(st *model.State) error {
		var a *model.AbnormalPayment
		for _, x := range st.AbnormalPayments {
			if x.ID == abnormalID {
				a = x
				break
			}
		}
		if a == nil {
			return notFound("异常支付记录不存在")
		}
		if a.Status != model.AbnormalPending {
			return conflict("该异常支付已处理，不能重复发起退款")
		}
		o := st.Order(a.OrderID)
		if o == nil || o.Status != model.StatusAbnormalPay {
			return conflict("订单不处于异常支付状态")
		}
		if activeRefund(st, o.ID) != nil {
			return conflict("该订单已有待处理退款单")
		}
		now := s.now()
		r := &model.Refund{
			ID: st.NextID("RF-"), OrderID: o.ID, Source: model.RefundSourceLate,
			AmountFen: a.AmountFen, Status: model.RefundAwait, Attempts: 1,
			Reason: "迟到支付人工退款", ReviewNote: note, CreatedAt: now, UpdatedAt: now,
		}
		st.Refunds = append(st.Refunds, r)
		a.RefundID = r.ID
		o.RefundID = r.ID
		st.AppendLedger("异常支付处理", r.AmountFen, o.ID, r.ID,
			"客服核实后发起退款（来源=迟到支付，退款成功也不恢复库存）", now)
		out = r
		return nil
	})
	return out, err
}

// Ship R7：仓库只能为合法待发货订单登记一次发货；发货记录对订单唯一。
// 发货是「占用转消耗」，可售数量在下单时已预扣，此处不再改动库存。
func (s *Service) Ship(actor model.Actor, orderID, carrier, trackingNo string) (*model.Shipment, error) {
	if err := requireRole(actor, model.RoleWarehouse); err != nil {
		return nil, err
	}
	var out *model.Shipment
	err := s.st.Update(func(st *model.State) error {
		o := st.Order(orderID)
		if o == nil {
			return notFound("订单不存在")
		}
		if st.ShipmentByOrder(orderID) != nil {
			return conflict("该订单已登记过发货，不能重复登记")
		}
		if o.Status != model.StatusPendingShip {
			return conflict("仅「待发货」订单可登记发货；退款中/已关闭/异常支付均被拦截")
		}
		now := s.now()
		if err := model.Transition(o, model.StatusShipped, now); err != nil {
			return conflict(err.Error())
		}
		if carrier == "" {
			carrier = "顺丰速运"
		}
		sh := &model.Shipment{
			ID: st.NextID("SH-"), OrderID: orderID, Carrier: carrier,
			TrackingNo: trackingNo, CreatedAt: now, Operator: actor.Name,
		}
		if sh.TrackingNo == "" {
			sh.TrackingNo = "SF" + sh.ID
		}
		st.Shipments = append(st.Shipments, sh)
		o.ShipmentID = sh.ID
		st.AppendLedger("发货", o.AmountFen, orderID, sh.ID, "登记发货一次，占用转消耗", now)
		out = sh
		return nil
	})
	return out, err
}

// ForceExpire 演示辅助：把指定待付款订单的到期时间提前，便于验证 10 分钟超时关闭。
func (s *Service) ForceExpire(actor model.Actor, orderID string) error {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return err
	}
	return s.st.Update(func(st *model.State) error {
		o := st.Order(orderID)
		if o == nil {
			return notFound("订单不存在")
		}
		if err := requireOwner(actor, o); err != nil {
			return err
		}
		if o.Status != model.StatusPendingPay {
			return conflict("仅待付款订单可模拟超时")
		}
		o.ExpiresAt = s.now().Add(-time.Second)
		return nil
	})
}
