package service

import (
	"testing"

	"small-commerce-order/internal/model"
)

// 库存补货：总库存与可售同步增加，操作留痕，越权与非法数量被拒绝。
func TestAddStock(t *testing.T) {
	h := newHarness(t)
	admin := model.Actor{Role: model.RoleAdmin, UserID: "su", Name: "系统管理员"}

	list, err := h.svc.Products(wh)
	if err != nil {
		t.Fatalf("读取商品失败：%v", err)
	}
	var before *model.Product
	for _, p := range list {
		if p.ID == "P1" {
			before = p
		}
	}
	if before == nil {
		t.Fatal("预置商品 P1 不存在")
	}
	total0, avail0 := before.Total, before.Available

	// 仓库补货 7 件
	p, err := h.svc.AddStock(wh, "P1", 7)
	if err != nil {
		t.Fatalf("仓库补货失败：%v", err)
	}
	if p.Total != total0+7 || p.Available != avail0+7 {
		t.Fatalf("补货后库存应同步 +7，总库存 %d→%d，可售 %d→%d", total0, p.Total, avail0, p.Available)
	}

	// 运营与管理员同样可以补货
	if _, err := h.svc.AddStock(ops, "P1", 1); err != nil {
		t.Fatalf("运营补货应允许：%v", err)
	}
	if _, err := h.svc.AddStock(admin, "P1", 1); err != nil {
		t.Fatalf("管理员补货应允许：%v", err)
	}

	// 买家 / 客服无权补货
	if _, err := h.svc.AddStock(buyer1, "P1", 1); errCode(err) != CodeForbidden {
		t.Fatalf("买家补货应拒绝，实际 %v", err)
	}
	if _, err := h.svc.AddStock(cs, "P1", 1); errCode(err) != CodeForbidden {
		t.Fatalf("客服补货应拒绝，实际 %v", err)
	}

	// 数量与商品校验
	if _, err := h.svc.AddStock(wh, "P1", 0); errCode(err) != CodeBadRequest {
		t.Fatalf("数量为 0 应报参数错误，实际 %v", err)
	}
	if _, err := h.svc.AddStock(wh, "P1", -3); errCode(err) != CodeBadRequest {
		t.Fatalf("负数量应报参数错误，实际 %v", err)
	}
	if _, err := h.svc.AddStock(wh, "P1", 10000); errCode(err) != CodeBadRequest {
		t.Fatalf("超过单次上限应报参数错误，实际 %v", err)
	}
	if _, err := h.svc.AddStock(wh, "P-NOT-EXIST", 1); errCode(err) != CodeNotFound {
		t.Fatalf("商品不存在应报 NOT_FOUND，实际 %v", err)
	}

	// 操作留痕：最近一条是「添加库存」
	recs, err := h.svc.ProductOps(ops)
	if err != nil {
		t.Fatalf("读取商品记录失败：%v", err)
	}
	if len(recs) == 0 {
		t.Fatal("补货应写入商品操作记录")
	}
	last := recs[len(recs)-1]
	if last.Type != "添加库存" || last.ProductID != "P1" {
		t.Fatalf("最近一条记录应为 P1 的添加库存，实际 %+v", last)
	}
}
