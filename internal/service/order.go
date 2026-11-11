package service

import (
	"fmt"
	"time"

	"small-commerce-order/internal/model"
)

// SetOnSale 运营维护商品上下架（R8：运营不能改订单状态，只能改商品）。
func (s *Service) SetOnSale(actor model.Actor, productID string, onSale bool) error {
	if err := requireRole(actor, model.RoleOps); err != nil {
		return err
	}
	return s.st.Update(func(st *model.State) error {
		p := st.Product(productID)
		if p == nil {
			return notFound("商品不存在")
		}
		p.OnSale = onSale
		word := "上架"
		if !onSale {
			word = "下架"
		}
		st.AppendLedger("商品"+word, 0, "", p.ID, p.Name, s.now())
		return nil
	})
}

// CreateOrder R1：只能在售且数量足够；下单即占用库存；订单保存商品价格快照。
// 写事务串行化 + 临界区内条件判断，等价于 UPDATE products SET available=available-n WHERE available>=n。
func (s *Service) CreateOrder(actor model.Actor, productID string, qty int) (*model.Order, error) {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return nil, err
	}
	if qty < 1 || qty > 2 {
		return nil, bad("下单数量必须为 1–2 件")
	}
	var out *model.Order
	err := s.st.Update(func(st *model.State) error {
		p := st.Product(productID)
		if p == nil {
			return notFound("商品不存在")
		}
		if !p.OnSale {
			return conflict("商品已下架，无法购买")
		}
		if p.Available < qty {
			return conflict("可售数量不足，无法下单")
		}
		now := s.now()
		p.Available -= qty // 原子占用：条件判断与扣减在同一写事务内
		o := &model.Order{
			ID: st.NextID("NO"), BuyerID: actor.UserID, BuyerName: actor.Name,
			ProductID: p.ID, ProductName: p.Name, ProductImage: p.Image,
			PriceFen: p.PriceFen, Qty: qty, AmountFen: p.PriceFen * int64(qty),
			Status:    model.StatusPendingPay,
			CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), StatusUpdatedAt: now,
		}
		st.Orders = append(st.Orders, o)
		st.AppendLedger("库存占用", o.AmountFen, o.ID, p.ID,
			fmt.Sprintf("下单占用 %d 件，单价快照 %d 分", qty, p.PriceFen), now)
		out = o
		return nil
	})
	return out, err
}

// closeOrder 内部 helper：R2 关闭幂等——只有「待付款 → 已关闭」迁移成功才释放库存。
func closeOrder(st *model.State, o *model.Order, now time.Time, reason string) error {
	if err := model.Transition(o, model.StatusClosed, now); err != nil {
		return conflict(err.Error())
	}
	o.ClosedAt = &now
	if p := st.Product(o.ProductID); p != nil {
		p.Available += o.Qty
	}
	st.AppendLedger("库存释放", o.AmountFen, o.ID, o.ProductID, reason+"，释放占用 "+fmt.Sprint(o.Qty)+" 件", now)
	return nil
}

// CancelOrder 买家主动取消（幂等：重复取消不再释放）。
func (s *Service) CancelOrder(actor model.Actor, orderID string) error {
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
		if o.Status == model.StatusClosed || o.Status == model.StatusAbnormalPay {
			return conflict("订单已关闭，无需重复释放库存")
		}
		return closeOrder(st, o, s.now(), "买家主动取消")
	})
}

// CloseExpiredOrders R2：超时以服务端时间判断；后台每秒扫描，也可由接口惰性触发。
func (s *Service) CloseExpiredOrders() (int, error) {
	n := 0
	err := s.st.Update(func(st *model.State) error {
		now := s.now()
		for _, o := range st.Orders {
			if o.Status != model.StatusPendingPay || now.Before(o.ExpiresAt) {
				continue
			}
			if err := closeOrder(st, o, now, "待付款超过 10 分钟，系统自动关闭"); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// CreatePaymentIntent 模拟支付渠道受理付款，生成带签名的待投递通知。
// 订单关闭后仍可生成，用于演示「迟到支付通知」。
func (s *Service) CreatePaymentIntent(actor model.Actor, orderID string) (*model.PaymentIntent, error) {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return nil, err
	}
	var out *model.PaymentIntent
	err := s.st.Update(func(st *model.State) error {
		o := st.Order(orderID)
		if o == nil {
			return notFound("订单不存在")
		}
		if err := requireOwner(actor, o); err != nil {
			return err
		}
		switch o.Status {
		case model.StatusPendingPay, model.StatusClosed, model.StatusAbnormalPay:
		default:
			return conflict("当前订单状态无需发起支付")
		}
		txn := st.NextID("TXN-")
		intent := &model.PaymentIntent{
			ChannelTxnID: txn, OrderID: orderID, AmountFen: o.AmountFen,
			Status: "SUCCESS", Nonce: st.NextID("NONCE-"),
		}
		intent.Signature = s.sign(intent.ChannelTxnID, intent.OrderID, intent.AmountFen, intent.Status, intent.Nonce)
		out = intent
		return nil
	})
	return out, err
}

// HandlePaymentNotify R3/R4：渠道通知处理。
// 所有结果（含验签失败/金额不符/未知订单/串单/重复/迟到）都写入只增流水并可查询；
// 因此本方法不因业务拒绝而回滚事务，处理结果通过 NotifyResult.Outcome 表达。
func (s *Service) HandlePaymentNotify(in model.PaymentIntent) (*model.NotifyResult, error) {
	seen := s.cache.PaymentSeen(in.ChannelTxnID) // Redis 辅助去重，MySQL 唯一索引才是兜底
	var res *model.NotifyResult
	err := s.st.Update(func(st *model.State) error {
		now := s.now()
		record := func(outcome, detail string) error {
			if seen {
				detail += "｜Redis 去重命中（仅辅助，正确性以 MySQL 唯一索引为准）"
			}
			res = &model.NotifyResult{
				ID: st.NextID("NR-"), ChannelTxnID: in.ChannelTxnID, OrderID: in.OrderID,
				AmountFen: in.AmountFen, Outcome: outcome, Detail: detail, CreatedAt: now,
			}
			st.NotifyResults = append(st.NotifyResults, res)
			st.AppendLedger("通知结果", in.AmountFen, in.OrderID, in.ChannelTxnID, outcome+"｜"+detail, now)
			return nil
		}

		// 1. 通知鉴权（验签）
		if !s.verify(in) {
			return record("BAD_SIGNATURE", "验签失败，通知被拒绝")
		}
		// 2. 订单匹配
		o := st.Order(in.OrderID)
		if o == nil {
			return record("UNKNOWN_ORDER", "未知订单，通知入异常核查列表")
		}
		// 3. 金额匹配
		if in.AmountFen != o.AmountFen {
			return record("AMOUNT_MISMATCH",
				fmt.Sprintf("通知金额 %d 分与订单金额 %d 分不符", in.AmountFen, o.AmountFen))
		}
		if in.Status != "SUCCESS" {
			return record("IGNORED", "非成功通知，不产生账务影响")
		}
		// 4. 渠道交易号唯一（重复通知不重复收款 / 不得归属两单）
		if pr := st.PaymentByTxn(in.ChannelTxnID); pr != nil {
			if pr.OrderID != in.OrderID {
				return record("TXN_CONFLICT", "同一渠道交易号已归属其它订单，拒绝串单")
			}
			return record("DUPLICATE", "重复通知：只记流水，不重复收款、不改订单状态")
		}
		// 5. 服务端时间判超时：先关闭（释放一次），再按迟到支付处理
		if o.Status == model.StatusPendingPay && !now.Before(o.ExpiresAt) {
			if err := closeOrder(st, o, now, "支付通知到达时发现订单已超时"); err != nil {
				return err
			}
		}
		switch o.Status {
		case model.StatusPendingPay:
			st.Payments = append(st.Payments, &model.PaymentRecord{
				ChannelTxnID: in.ChannelTxnID, OrderID: o.ID, AmountFen: in.AmountFen, CreatedAt: now,
			})
			o.ChannelTxnID = in.ChannelTxnID
			o.PaidAt = &now
			if err := model.Transition(o, model.StatusPendingShip, now); err != nil {
				return err
			}
			st.AppendLedger("收款", in.AmountFen, o.ID, in.ChannelTxnID, "渠道通知校验通过，确认收款", now)
			return record("ACCEPTED", "支付成功，订单转为待发货")
		case model.StatusClosed:
			// R4：不复活订单、不重占库存，进入人工通道
			if err := model.Transition(o, model.StatusAbnormalPay, now); err != nil {
				return err
			}
			a := &model.AbnormalPayment{
				ID: st.NextID("ABN-"), OrderID: o.ID, ChannelTxnID: in.ChannelTxnID,
				AmountFen: in.AmountFen, Status: model.AbnormalPending,
				Reason: "订单已关闭后收到成功支付通知（迟到支付）", CreatedAt: now,
			}
			st.AbnormalPayments = append(st.AbnormalPayments, a)
			o.AbnormalID = a.ID
			st.AppendLedger("异常支付", in.AmountFen, o.ID, a.ID,
				"迟到支付：不复活订单、不重占库存，待客服核实退款", now)
			return record("LATE_PAYMENT", "迟到支付，已进入异常支付待处理列表")
		case model.StatusAbnormalPay:
			return record("IGNORED", "订单已有异常支付记录，不重复入账、不重复建单")
		default:
			return record("IGNORED", "订单当前为 "+o.Status.Label()+"，不接收支付通知")
		}
	})
	if err == nil && res != nil {
		s.cache.MarkPayment(in.ChannelTxnID) // 事务提交成功后才标记，避免误判
	}
	return res, err
}
