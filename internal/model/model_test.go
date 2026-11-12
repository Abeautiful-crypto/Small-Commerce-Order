package model

import (
	"testing"
	"time"
)

func TestOrderStatus_Label(t *testing.T) {
	if got := (StatusPendingPay).Label(); got != "待付款" {
		t.Fatalf("待付款标签不符：%s", got)
	}
	if got := OrderStatus("UNKNOWN").Label(); got != "UNKNOWN" {
		t.Fatalf("未知状态应原样返回：%s", got)
	}
}

// 状态机唯一裁判：合法迁移全部放行，非法迁移全部拒绝。
func TestCanTransition(t *testing.T) {
	legal := [][2]OrderStatus{
		{StatusPendingPay, StatusPendingShip},
		{StatusPendingPay, StatusClosed},
		{StatusPendingShip, StatusRefunding},
		{StatusPendingShip, StatusShipped},
		{StatusRefunding, StatusPendingShip},
		{StatusRefunding, StatusRefunded},
		{StatusClosed, StatusAbnormalPay},
		{StatusAbnormalPay, StatusRefunded},
	}
	for _, pair := range legal {
		if !CanTransition(pair[0], pair[1]) {
			t.Fatalf("合法迁移被拒：%s → %s", pair[0], pair[1])
		}
	}
	illegal := [][2]OrderStatus{
		{StatusPendingPay, StatusShipped},
		{StatusPendingPay, StatusRefunded},
		{StatusPendingShip, StatusPendingPay},
		{StatusClosed, StatusPendingShip},
		{StatusShipped, StatusRefunded},
		{StatusRefunded, StatusShipped},
		{OrderStatus("UNKNOWN"), StatusPendingShip},
		{StatusPendingPay, OrderStatus("UNKNOWN")},
	}
	for _, pair := range illegal {
		if CanTransition(pair[0], pair[1]) {
			t.Fatalf("非法迁移被放行：%s → %s", pair[0], pair[1])
		}
	}
}

func TestTransition(t *testing.T) {
	now := time.Now()
	o := &Order{ID: "NO1", Status: StatusPendingPay}
	if err := Transition(o, StatusPendingShip, now); err != nil {
		t.Fatalf("合法迁移失败：%v", err)
	}
	if o.Status != StatusPendingShip || !o.StatusUpdatedAt.Equal(now) {
		t.Fatalf("迁移未落状态：%+v", o)
	}
	if err := Transition(o, StatusPendingPay, now); err == nil {
		t.Fatal("非法迁移必须报错")
	}
}

func TestIsTerminal(t *testing.T) {
	if !IsTerminal(StatusShipped) || !IsTerminal(StatusRefunded) {
		t.Fatal("已发货/已退款应为终态")
	}
	for _, s := range []OrderStatus{StatusPendingPay, StatusClosed, StatusPendingShip, StatusRefunding, StatusAbnormalPay} {
		if IsTerminal(s) {
			t.Fatalf("%s 不应为终态", s)
		}
	}
}

func TestNextID_FmtInt(t *testing.T) {
	st := &State{}
	if got := st.NextID("NO"); got != "NO1" || st.Seq != 1 {
		t.Fatalf("首序号应为 NO1，实际 %s seq=%d", got, st.Seq)
	}
	if got := st.NextID("R"); got != "R2" {
		t.Fatalf("次序号应为 R2，实际 %s", got)
	}
	if got := fmtInt(0); got != "0" {
		t.Fatalf("0 应格式化为 0，实际 %s", got)
	}
	if got := fmtInt(1234567890123); got != "1234567890123" {
		t.Fatalf("大数格式化不符：%s", got)
	}
}

// 各类查找器：命中与未命中。
func TestState_Finders(t *testing.T) {
	now := time.Now()
	st := newTestState(now)
	if p := st.Product("P1"); p == nil || p.ID != "P1" {
		t.Fatal("Product 命中失败")
	}
	if st.Product("NOPE") != nil {
		t.Fatal("Product 未命中应返回 nil")
	}
	if o := st.Order("NO1"); o == nil {
		t.Fatal("Order 命中失败")
	}
	if st.Order("NOX") != nil {
		t.Fatal("Order 未命中应返回 nil")
	}
	if r := st.Refund("R1"); r == nil {
		t.Fatal("Refund 命中失败")
	}
	if st.Refund("RX") != nil {
		t.Fatal("Refund 未命中应返回 nil")
	}
	if p := st.PaymentByTxn("TXN1"); p == nil {
		t.Fatal("PaymentByTxn 命中失败")
	}
	if st.PaymentByTxn("TXNX") != nil {
		t.Fatal("PaymentByTxn 未命中应返回 nil")
	}
	if s := st.ShipmentByOrder("NO1"); s == nil {
		t.Fatal("ShipmentByOrder 命中失败")
	}
	if st.ShipmentByOrder("NOX") != nil {
		t.Fatal("ShipmentByOrder 未命中应返回 nil")
	}
	if a := st.AbnormalByOrder("NO1"); a == nil {
		t.Fatal("AbnormalByOrder 命中失败")
	}
	if st.AbnormalByOrder("NOX") != nil {
		t.Fatal("AbnormalByOrder 未命中应返回 nil")
	}
}

// 流水只增不改：追加后序号单调、字段完整。
func TestAppendLedger(t *testing.T) {
	now := time.Now()
	st := newTestState(now)
	e1 := st.AppendLedger("库存占用", 10000, "NO1", "", "占用 1 件", now)
	e2 := st.AppendLedger("收款", 10000, "NO1", "TXN1", "收款", now)
	if e1.ID != 1 || e2.ID != 2 || st.Seq != 2 {
		t.Fatalf("流水序号应单调递增：%d,%d seq=%d", e1.ID, e2.ID, st.Seq)
	}
	if len(st.Ledger) != 2 {
		t.Fatalf("流水应只增，实际 %d 条", len(st.Ledger))
	}
	if e2.Type != "收款" || e2.RefID != "TXN1" {
		t.Fatalf("流水字段不符：%+v", e2)
	}
}

// FindUser：ID+角色精确匹配。
func TestFindUser(t *testing.T) {
	if u, ok := FindUser("u001", RoleBuyer); !ok || u.Name != "张小买" {
		t.Fatalf("买家命中失败：%+v %v", u, ok)
	}
	if _, ok := FindUser("u001", RoleOps); ok {
		t.Fatal("角色不匹配不应命中")
	}
	if _, ok := FindUser("nobody", RoleBuyer); ok {
		t.Fatal("未知用户不应命中")
	}
	if _, ok := FindUser("channel", RoleChannel); !ok {
		t.Fatal("渠道内部身份应可命中")
	}
}

func newTestState(now time.Time) *State {
	return &State{
		Products:         []*Product{{ID: "P1", PriceFen: 10000}},
		Orders:           []*Order{{ID: "NO1", Status: StatusPendingPay, CreatedAt: now}},
		Payments:         []*PaymentRecord{{ChannelTxnID: "TXN1", OrderID: "NO1"}},
		Refunds:          []*Refund{{ID: "R1", OrderID: "NO1"}},
		Shipments:        []*Shipment{{ID: "S1", OrderID: "NO1"}},
		AbnormalPayments: []*AbnormalPayment{{ID: "A1", OrderID: "NO1"}},
	}
}
