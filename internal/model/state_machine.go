package model

import (
	"fmt"
	"time"
)

// 订单状态机：迁移以本表为唯一裁判，任何状态变更都必须经过 Transition。
// 待付款   →(验签+金额匹配的支付通知) 待发货 / (取消·超时) 已关闭
// 待发货   →(申请退款) 退款中 / (登记发货一次) 已发货
// 退款中   →(拒绝) 待发货 / (同意+渠道成功) 已退款（售前恢复库存）
// 已关闭   →(迟到成功通知) 异常支付 →(客服核实退款) 已退款（不恢复库存）
// 已发货 / 已退款 为终态。
var allowedTransitions = map[OrderStatus]map[OrderStatus]bool{
	StatusPendingPay: {
		StatusPendingShip: true,
		StatusClosed:      true,
	},
	StatusPendingShip: {
		StatusRefunding: true,
		StatusShipped:   true,
	},
	StatusRefunding: {
		StatusPendingShip: true,
		StatusRefunded:    true,
	},
	StatusClosed: {
		StatusAbnormalPay: true,
	},
	StatusAbnormalPay: {
		StatusRefunded: true,
	},
	StatusShipped:  {},
	StatusRefunded: {},
}

// CanTransition 判断状态迁移是否合法。
func CanTransition(from, to OrderStatus) bool {
	if next, ok := allowedTransitions[from]; ok {
		return next[to]
	}
	return false
}

// Transition 集中守卫：只有合法迁移才会改写订单状态；库存等副作用由 service 按迁移结果处理。
func Transition(o *Order, to OrderStatus, at time.Time) error {
	if !CanTransition(o.Status, to) {
		return fmt.Errorf("非法状态迁移：%s → %s", o.Status.Label(), to.Label())
	}
	o.Status = to
	o.StatusUpdatedAt = at
	return nil
}

// IsTerminal 终态判断。
func IsTerminal(s OrderStatus) bool {
	return s == StatusShipped || s == StatusRefunded
}
