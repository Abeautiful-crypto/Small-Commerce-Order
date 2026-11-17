package api

import (
	"net/http"
	"testing"

	"small-commerce-order/internal/model"
	"small-commerce-order/internal/service"
)

// 库存补货接口 + 管理员员工账号接口：权限、入参与返回结构。
func TestAPI_StockAndStaff(t *testing.T) {
	h := newAPIHarness(t)
	adminA := model.Actor{Role: model.RoleAdmin, UserID: "su"}

	// 仓库给 P1 补货 5 件
	code, env := h.request(t, http.MethodPost, "/api/products/P1/stock", map[string]any{"qty": 5}, whA, "")
	assertCode(t, code, env, http.StatusOK, "")
	got := asMap(t, env.Data)
	if got["id"] != "P1" {
		t.Fatalf("补货应返回商品本身，实际 %+v", got)
	}
	if total, _ := got["total"].(float64); total < 5 {
		t.Fatalf("补货后总库存应增加，实际 %v", got["total"])
	}

	// 买家无权补货
	code, env = h.request(t, http.MethodPost, "/api/products/P1/stock", map[string]any{"qty": 1}, buyerA, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	// 数量非法：400
	code, env = h.request(t, http.MethodPost, "/api/products/P1/stock", map[string]any{"qty": 0}, whA, "")
	assertCode(t, code, env, http.StatusBadRequest, service.CodeBadRequest)

	// 商品不存在：404
	code, env = h.request(t, http.MethodPost, "/api/products/P404/stock", map[string]any{"qty": 1}, whA, "")
	assertCode(t, code, env, http.StatusNotFound, service.CodeNotFound)

	// 员工列表：非管理员 403
	code, env = h.request(t, http.MethodGet, "/api/staff", nil, opsA, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	// 管理员可看员工列表，且包含预置管理员自己
	code, env = h.request(t, http.MethodGet, "/api/staff", nil, adminA, "")
	assertCode(t, code, env, http.StatusOK, "")
	staff := asSlice(t, env.Data)
	if len(staff) == 0 {
		t.Fatal("员工列表不应为空")
	}

	// 非管理员不能新增员工
	code, env = h.request(t, http.MethodPost, "/api/staff", map[string]any{
		"name": "王仓管", "phone": "13900000011", "password": "123456", "role": "warehouse",
	}, opsA, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	// 管理员新增仓库账号成功
	code, env = h.request(t, http.MethodPost, "/api/staff", map[string]any{
		"name": "王仓管", "phone": "13900000011", "password": "123456", "role": "warehouse",
	}, adminA, "")
	assertCode(t, code, env, http.StatusOK, "")
	created := asMap(t, asMap(t, env.Data)["user"])
	if created["role"] != "warehouse" || created["phone"] != "13900000011" {
		t.Fatalf("新增员工返回异常：%+v", created)
	}

	// 手机号重复：409
	code, env = h.request(t, http.MethodPost, "/api/staff", map[string]any{
		"name": "重复账号", "phone": "13900000011", "password": "123456", "role": "cs",
	}, adminA, "")
	assertCode(t, code, env, http.StatusConflict, service.CodeConflict)

	// 角色不在 B 端范围（买家）：400
	code, env = h.request(t, http.MethodPost, "/api/staff", map[string]any{
		"name": "假买家", "phone": "13900000012", "password": "123456", "role": "buyer",
	}, adminA, "")
	assertCode(t, code, env, http.StatusBadRequest, service.CodeBadRequest)
}
