package service

import (
	"testing"
)

// CreateProduct：运营可创建、默认在售、初始库存即总库存即可售库存，并记录流水。
func TestCreateProduct_Ops(t *testing.T) {
	h := newHarness(t)
	p, err := h.svc.CreateProduct(ops, "便携保温杯", "uploads/abc123.png", "家居生活", 9900, 5)
	if err != nil {
		t.Fatal(err)
	}
	if p.OnSale != true || p.Total != 5 || p.Available != 5 || p.PriceFen != 9900 {
		t.Fatalf("新建商品字段不符合预期：%+v", p)
	}
	if p.OldPriceFen != p.PriceFen {
		t.Fatalf("划线价应等于售价，实际 %d/%d", p.OldPriceFen, p.PriceFen)
	}
	if p.Image != "uploads/abc123.png" || p.Category != "家居生活" {
		t.Fatalf("图片/分类应保存为提交值，实际 %s / %s", p.Image, p.Category)
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
	// 商品动作写「商品操作记录」，资金流水必须保持干净
	recs, err := h.svc.ProductOps(ops)
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, r := range recs {
		if r.Type == "新增商品" && r.ProductID == p.ID {
			ok = true
		}
	}
	if !ok {
		t.Fatal("新建商品应记录「新增商品」操作记录")
	}
	led, err := h.svc.Ledger(ops)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range led {
		if l.RefID == p.ID && l.Type == "商品上架" {
			t.Fatal("资金流水不应出现商品操作")
		}
	}
	if _, err := h.svc.ProductOps(buyer1); err == nil {
		t.Fatal("商品操作记录只对运营开放")
	}
}

// CreateProduct：权限、图片、分类与参数校验。
func TestCreateProduct_Guards(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.CreateProduct(buyer1, "x", "uploads/a.png", "", 100, 1); err == nil {
		t.Fatal("买家不应能新建商品")
	}
	if _, err := h.svc.CreateProduct(wh, "x", "uploads/a.png", "", 100, 1); err == nil {
		t.Fatal("仓库不应能新建商品")
	}
	if _, err := h.svc.CreateProduct(cs, "x", "uploads/a.png", "", 100, 1); err == nil {
		t.Fatal("客服不应能新建商品")
	}
	if _, err := h.svc.CreateProduct(ops, "  ", "uploads/a.png", "", 100, 1); err == nil {
		t.Fatal("空名称应被拒绝")
	}
	// 图片：必须上传（不再有默认图）
	if _, err := h.svc.CreateProduct(ops, "无图商品", "", "", 100, 1); err == nil {
		t.Fatal("缺少商品图片应被拒绝")
	}
	for _, bad := range []string{"javascript:alert(1)", "data:image/png;base64,AAA", "file:///etc/passwd"} {
		if _, err := h.svc.CreateProduct(ops, "x", bad, "", 100, 1); err == nil {
			t.Fatalf("非法图片地址应被拒绝：%s", bad)
		}
	}
	if p, err := h.svc.CreateProduct(ops, "外链图", "https://example.com/p.png", "", 100, 1); err != nil || p.Image != "https://example.com/p.png" {
		t.Fatalf("http(s) 图片应被接受，实际 %+v %v", p, err)
	}
	// 分类：空值归入「其他」，白名单外的分类拒绝
	if p, err := h.svc.CreateProduct(ops, "无分类", "uploads/a.png", "", 100, 1); err != nil || p.Category != DefaultCategory {
		t.Fatalf("空分类应归入「%s」，实际 %+v %v", DefaultCategory, p, err)
	}
	if _, err := h.svc.CreateProduct(ops, "x", "uploads/a.png", "汽车", 100, 1); err == nil {
		t.Fatal("白名单外的分类应被拒绝")
	}
	if _, err := h.svc.CreateProduct(ops, "x", "uploads/a.png", "", 0, 1); err == nil {
		t.Fatal("价格为 0 应被拒绝")
	}
	if _, err := h.svc.CreateProduct(ops, "x", "uploads/a.png", "", -1, 1); err == nil {
		t.Fatal("负价格应被拒绝")
	}
	if _, err := h.svc.CreateProduct(ops, "x", "uploads/a.png", "", 100, -1); err == nil {
		t.Fatal("负库存应被拒绝")
	}
}

// CreateProduct：新建商品可立即被买家下单并占用库存（闭环校验）。
func TestCreateProduct_Orderable(t *testing.T) {
	h := newHarness(t)
	p, err := h.svc.CreateProduct(ops, "新商品", "uploads/b.png", "电子产品", 500, 1)
	if err != nil {
		t.Fatal(err)
	}
	o, err := h.svc.CreateOrder(buyer1, p.ID, 1, "alipay", h.addr(buyer1))
	if err != nil {
		t.Fatalf("新建商品应可下单：%v", err)
	}
	if o.AmountFen != 500 {
		t.Fatalf("订单金额应等于单价，实际 %d", o.AmountFen)
	}
	if _, err := h.svc.CreateOrder(buyer2, p.ID, 1, "alipay", h.addr(buyer2)); err == nil {
		t.Fatal("库存 1 应在第二单时不足")
	}
}
