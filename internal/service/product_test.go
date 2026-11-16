package service

import (
	"testing"
)

// CreateProduct：运营可创建、默认在售、初始库存即总库存即可售库存，并记录流水。
func TestCreateProduct_Ops(t *testing.T) {
	h := newHarness(t)
	p, err := h.svc.CreateProduct(ops, "便携保温杯", 9900, 5)
	if err != nil {
		t.Fatal(err)
	}
	if p.OnSale != true || p.Total != 5 || p.Available != 5 || p.PriceFen != 9900 {
		t.Fatalf("新建商品字段不符合预期：%+v", p)
	}
	if p.OldPriceFen != p.PriceFen {
		t.Fatalf("划线价应等于售价，实际 %d/%d", p.OldPriceFen, p.PriceFen)
	}
	list, err := h.svc.Products(ops)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range list {
		if it.ID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("新建商品应出现在商品列表")
	}
	led, err := h.svc.Ledger(ops)
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, l := range led {
		if l.Type == "商品上架" && l.RefID == p.ID {
			ok = true
		}
	}
	if !ok {
		t.Fatal("新建商品应记录「商品上架」流水")
	}
}

// CreateProduct：权限与参数校验。
func TestCreateProduct_Guards(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.CreateProduct(buyer1, "x", 100, 1); err == nil {
		t.Fatal("买家不应能新建商品")
	}
	if _, err := h.svc.CreateProduct(wh, "x", 100, 1); err == nil {
		t.Fatal("仓库不应能新建商品")
	}
	if _, err := h.svc.CreateProduct(cs, "x", 100, 1); err == nil {
		t.Fatal("客服不应能新建商品")
	}
	if _, err := h.svc.CreateProduct(ops, "  ", 100, 1); err == nil {
		t.Fatal("空名称应被拒绝")
	}
	if _, err := h.svc.CreateProduct(ops, "x", 0, 1); err == nil {
		t.Fatal("价格为 0 应被拒绝")
	}
	if _, err := h.svc.CreateProduct(ops, "x", -1, 1); err == nil {
		t.Fatal("负价格应被拒绝")
	}
	if _, err := h.svc.CreateProduct(ops, "x", 100, -1); err == nil {
		t.Fatal("负库存应被拒绝")
	}
}

// CreateProduct：新建商品可立即被买家下单并占用库存（闭环校验）。
func TestCreateProduct_Orderable(t *testing.T) {
	h := newHarness(t)
	p, err := h.svc.CreateProduct(ops, "新商品", 500, 1)
	if err != nil {
		t.Fatal(err)
	}
	o, err := h.svc.CreateOrder(buyer1, p.ID, 1)
	if err != nil {
		t.Fatalf("新建商品应可下单：%v", err)
	}
	if o.AmountFen != 500 {
		t.Fatalf("订单金额应等于单价，实际 %d", o.AmountFen)
	}
	if _, err := h.svc.CreateOrder(buyer2, p.ID, 1); err == nil {
		t.Fatal("库存 1 应在第二单时不足")
	}
}
