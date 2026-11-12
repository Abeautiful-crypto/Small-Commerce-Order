package api

import (
	"net/http"
	"testing"

	"small-commerce-order/internal/cache"
	"small-commerce-order/internal/model"
	"small-commerce-order/internal/service"
	"small-commerce-order/internal/store"
)

// 生产模式单测：验证「真实登录 + 只认会话令牌 + 禁用演示接口」三道防线。
// 密码与密钥均来自配置，测试中只使用假值，不涉及任何真实凭据。

var testPasswords = map[string]string{
	"u001": "pw-buyer-1", "u002": "pw-buyer-2", "u003": "pw-buyer-3",
	"admin": "pw-ops", "wh": "pw-wh", "cs": "pw-cs",
}

func prodHarness(t *testing.T) *apiHarness {
	t.Helper()
	return newAPIHarness(t, testPasswords)
}

func TestSecurity_MetaReportsEnv(t *testing.T) {
	demo := newAPIHarness(t)
	code, env := demo.request(t, http.MethodGet, "/api/meta", nil, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["env"]; got != "demo" {
		t.Fatalf("默认应为 demo 环境，实际 %v", got)
	}
	prod := prodHarness(t)
	code, env = prod.request(t, http.MethodGet, "/api/meta", nil, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["env"]; got != "production" {
		t.Fatalf("生产模式应返回 production，实际 %v", got)
	}
}

func TestSecurity_ProductionRequiresPassword(t *testing.T) {
	h := prodHarness(t)
	login := func(pwd string) (int, envelope) {
		return h.request(t, http.MethodPost, "/api/session",
			map[string]string{"role": "buyer", "userId": "u001", "password": pwd}, model.Actor{}, "")
	}
	code, env := login("")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)
	code, env = login("wrong-password")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	code, env = login(testPasswords["u001"])
	assertCode(t, code, env, http.StatusOK, "")
	token, _ := asMap(t, env.Data)["token"].(string)
	if token == "" {
		t.Fatal("正确密码应签发会话令牌")
	}
	// 令牌可用
	code, env = h.request(t, http.MethodGet, "/api/products", nil, model.Actor{}, token)
	assertCode(t, code, env, http.StatusOK, "")
}

func TestSecurity_ProductionNoHeaderFallback(t *testing.T) {
	h := prodHarness(t)
	// 合法请求头但无令牌 → 403（生产模式绝不信任 X-Role / X-User-Id）
	code, env := h.request(t, http.MethodGet, "/api/products", nil, buyerA, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)
	code, env = h.request(t, http.MethodPost, "/api/orders",
		map[string]any{"productId": "P1", "qty": 1}, buyerA, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	// 无效令牌 + 合法请求头 → 仍然 403，不回退
	code, env = h.request(t, http.MethodGet, "/api/products", nil, buyerA, "invalid-token")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	// 数据未被上述尝试改动
	code, env = h.request(t, http.MethodGet, "/api/meta", nil, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
}

func TestSecurity_DemoEndpointsDisabledInProduction(t *testing.T) {
	h := prodHarness(t)
	code, env := h.request(t, http.MethodPost, "/api/reset", map[string]string{}, opsA, "")
	assertCode(t, code, env, http.StatusNotFound, service.CodeNotFound)

	code, env = h.request(t, http.MethodPost, "/api/orders/NO20260001/expire",
		map[string]string{}, buyerA, "")
	assertCode(t, code, env, http.StatusNotFound, service.CodeNotFound)

	// 业务接口仍然存在（只是需要令牌）
	code, env = h.request(t, http.MethodPost, "/api/session",
		map[string]string{"role": "ops", "userId": "admin", "password": testPasswords["admin"]}, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	token, _ := asMap(t, env.Data)["token"].(string)
	code, env = h.request(t, http.MethodGet, "/api/summary", nil, model.Actor{}, token)
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["orderCount"]; got != float64(1) {
		t.Fatalf("演示接口未注册不应影响数据：orderCount=%v", got)
	}
}

func TestSecurity_ProductionSessionRequiresRedis(t *testing.T) {
	st, err := store.Open(t.TempDir()+"/state.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(st, "strong-secret", nil)
	server := New(svc, t.TempDir(), cache.Noop{}).WithSecurity(testPasswords)
	h := &apiHarness{handler: server.Handler()}
	code, env := h.request(t, http.MethodPost, "/api/session",
		map[string]string{"role": "buyer", "userId": "u001", "password": testPasswords["u001"]}, model.Actor{}, "")
	if code != http.StatusInternalServerError || env.OK {
		t.Fatalf("Redis 不可用时生产模式必须拒绝登录：code=%d env=%+v", code, env)
	}
}

func TestSecurity_ProductionRoleGuardStillApplies(t *testing.T) {
	h := prodHarness(t)
	code, env := h.request(t, http.MethodPost, "/api/session",
		map[string]string{"role": "buyer", "userId": "u002", "password": testPasswords["u002"]}, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	token, _ := asMap(t, env.Data)["token"].(string)

	// 已登录买家仍不得调用其它角色接口
	code, env = h.request(t, http.MethodPost, "/api/products/P1/onsale",
		map[string]bool{"onSale": false}, model.Actor{}, token)
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)
	code, env = h.request(t, http.MethodGet, "/api/summary", nil, model.Actor{}, token)
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	// 越权尝试不得改动商品状态
	code, env = h.request(t, http.MethodGet, "/api/products", nil, model.Actor{}, token)
	assertCode(t, code, env, http.StatusOK, "")
	for _, raw := range asSlice(t, env.Data) {
		p, _ := raw.(map[string]any)
		if p["id"] == "P1" && p["onSale"] != true {
			t.Fatalf("越权尝试改动了商品状态：%+v", p)
		}
	}
}

// passwordlessHarness 构造「生产模式 + 免密码登录」的服务端。
func passwordlessHarness(t *testing.T) *apiHarness {
	t.Helper()
	st, err := store.Open(t.TempDir()+"/state.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := newMemCache()
	svc := service.New(st, "strong-secret", nil).WithCache(c)
	server := New(svc, t.TempDir(), c).WithSecurity(nil).WithPasswordless()
	return &apiHarness{handler: server.Handler(), svc: svc, st: st, cache: c}
}

// 免密码登录只跳过密码校验，其余三道防线（令牌、角色隔离、演示接口）必须保持。
func TestSecurity_PasswordlessLogin(t *testing.T) {
	h := passwordlessHarness(t)

	code, env := h.request(t, http.MethodGet, "/api/meta", nil, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	d := asMap(t, env.Data)
	if got := d["env"]; got != "production" {
		t.Fatalf("免密码模式仍应报告 production，实际 %v", got)
	}
	if got, _ := d["loginPasswordless"].(bool); !got {
		t.Fatalf("meta 应标记 loginPasswordless=true，实际 %v", d["loginPasswordless"])
	}

	// 空密码即可登录，并签发会话令牌
	code, env = h.request(t, http.MethodPost, "/api/session",
		map[string]string{"role": "buyer", "userId": "u001", "password": ""}, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	token, _ := asMap(t, env.Data)["token"].(string)
	if token == "" {
		t.Fatal("免密码登录应签发会话令牌")
	}

	// 仍然只认令牌：无令牌的请求头身份一律 403
	code, env = h.request(t, http.MethodGet, "/api/products", nil, buyerA, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	code, env = h.request(t, http.MethodGet, "/api/products", nil, model.Actor{}, token)
	assertCode(t, code, env, http.StatusOK, "")

	// 演示接口依旧不注册，避免线上数据被清空
	code, env = h.request(t, http.MethodPost, "/api/reset", map[string]string{}, opsA, "")
	assertCode(t, code, env, http.StatusNotFound, service.CodeNotFound)
}

// 未开启免密码时，production 仍然必须校验密码。
func TestSecurity_PasswordlessOffStillRequiresPassword(t *testing.T) {
	h := prodHarness(t)
	code, env := h.request(t, http.MethodPost, "/api/session",
		map[string]string{"role": "buyer", "userId": "u001", "password": ""}, model.Actor{}, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)
}
