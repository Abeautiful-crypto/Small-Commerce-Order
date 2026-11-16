package service

import (
	"small-commerce-order/internal/model"
)

// Sign 买家确认收货：唯一合法迁移是 已发货 → 已签收。
// 签收时间与签收人回写到该订单的发货记录，仓库在「发货记录」里即可看到签收情况。
// 签收是终态：签收后不可再申请退款，也不可重复签收。
func (s *Service) Sign(actor model.Actor, orderID string) (*model.Order, error) {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return nil, err
	}
	var out *model.Order
	err := s.st.Update(func(st *model.State) error {
		o := st.Order(orderID)
		if o == nil {
			return notFound("订单不存在")
		}
		if err := requireOwner(actor, o); err != nil {
			return err
		}
		switch o.Status {
		case model.StatusSigned:
			return conflict("订单已签收，无需重复确认")
		case model.StatusShipped:
		default:
			return conflict("仅「已发货」订单可确认收货，当前状态：" + o.Status.Label())
		}
		now := s.now()
		if err := model.Transition(o, model.StatusSigned, now); err != nil {
			return conflict(err.Error())
		}
		if sh := st.ShipmentByOrder(orderID); sh != nil {
			t := now
			sh.SignedAt = &t
			sh.SignedBy = actor.Name
		}
		out = o
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
