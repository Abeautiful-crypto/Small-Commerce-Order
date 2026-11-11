package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"small-commerce-order/internal/cache"
	"small-commerce-order/internal/model"
	"small-commerce-order/internal/service"
	"small-commerce-order/internal/store"
)

// memCache 是 cache.Cache 的进程内实现，使 API 层单测不依赖 Redis。
type memCache struct {
	mu    sync.Mutex
	stock map[string]int
	pay   map[string]bool
	sess  map[string]string
}

var _ cache.Cache = (*memCache)(nil)

func newMemCache() *memCache {
	return &memCache{stock: map[string]int{}, pay: map[string]bool{}, sess: map[string]string{}}
}

func (c *memCache) Stock(id string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.stock[id]
	return n, ok
}
func (c *memCache) SetStock(id string, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stock[id] = n
}
func (c *memCache) InvalidateStock(ids ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		delete(c.stock, id)
	}
}
func (c *memCache) PaymentSeen(txn string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pay[txn]
}
func (c *memCache) MarkPayment(txn string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pay[txn] = true
}
func (c *memCache) SaveSession(token, role, userID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sess[token] = role + "|" + userID
	return nil
}
func (c *memCache) LoadSession(token string) (string, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.sess[token]
	if !ok {
		return "", "", false
	}
	parts := strings.SplitN(v, "|", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
func (c *memCache) Ping() error { return nil }

// ----------------------------------------------------------------

type apiHarness struct {
	handler http.Handler
	svc     *service.Service
	st      store.StateStore
	cache   *memCache
}

// 传入 passwords 即启用生产模式；不传则为默认 demo 模式。
func newAPIHarness(t *testing.T, security ...map[string]string) *apiHarness {
	t.Helper()
	loc := time.FixedZone("CST", 8*3600)
	clock := func() time.Time { return time.Date(2026, 11, 11, 10, 0, 0, 0, loc) }
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"), clock)
	if err != nil {
		t.Fatal(err)
	}
	c := newMemCache()
	svc := service.New(st, "test-secret", clock).WithCache(c)
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"),
		[]byte("<!doctype html><title>acceptance</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := New(svc, webDir, c)
	if len(security) > 0 {
		server = server.WithSecurity(security[0])
	}
	return &apiHarness{handler: server.Handler(), svc: svc, st: st, cache: c}
}

func (h *apiHarness) raw(method, path string, body io.Reader, actor model.Actor, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	if actor.Role != "" {
		req.Header.Set("X-Role", string(actor.Role))
		req.Header.Set("X-User-Id", actor.UserID)
	}
	if token != "" {
		req.Header.Set("X-Token", token)
	}
	rr := httptest.NewRecorder()
	h.handler.ServeHTTP(rr, req)
	return rr
}

func (h *apiHarness) request(t *testing.T, method, path string, body any, actor model.Actor, token string) (int, envelope) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	rr := h.raw(method, path, reader, actor, token)
	var env envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是合法 JSON: %v; body=%s", err, rr.Body.String())
	}
	return rr.Code, env
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("data 期望对象，实际 %T: %v", v, v)
	}
	return m
}

func asSlice(t *testing.T, v any) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("data 期望数组，实际 %T: %v", v, v)
	}
	return s
}

func assertCode(t *testing.T, code int, env envelope, wantStatus int, wantCode string) {
	t.Helper()
	if code != wantStatus {
		t.Fatalf("HTTP 状态期望 %d，实际 %d；error=%+v", wantStatus, code, env.Error)
	}
	if wantCode == "" {
		if !env.OK {
			t.Fatalf("期望成功响应，实际 error=%+v", env.Error)
		}
		return
	}
	if env.Error == nil || env.Error.Code != wantCode {
		t.Fatalf("错误码期望 %s，实际 %+v", wantCode, env.Error)
	}
}

var (
	buyerA = model.Actor{Role: model.RoleBuyer, UserID: "u001"}
	buyerB = model.Actor{Role: model.RoleBuyer, UserID: "u002"}
	buyerC = model.Actor{Role: model.RoleBuyer, UserID: "u003"}
	opsA   = model.Actor{Role: model.RoleOps, UserID: "admin"}
	whA    = model.Actor{Role: model.RoleWarehouse, UserID: "wh"}
	csA    = model.Actor{Role: model.RoleCS, UserID: "cs"}
)

func TestAPI_MetaAndStatic(t *testing.T) {
	h := newAPIHarness(t)
	code, env := h.request(t, http.MethodGet, "/api/meta", nil, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	d := asMap(t, env.Data)
	if _, ok := d["users"]; !ok {
		t.Fatal("meta 缺少 users")
	}
	if _, ok := d["statusLabels"]; !ok {
		t.Fatal("meta 缺少 statusLabels")
	}
	rr := h.raw(http.MethodGet, "/", nil, model.Actor{}, "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "acceptance") {
		t.Fatalf("静态首页托管异常：code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAPI_SessionAndActorResolution(t *testing.T) {
	h := newAPIHarness(t)
	code, env := h.request(t, http.MethodPost, "/api/session",
		map[string]string{"role": "buyer", "userId": "u002"}, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	sess := asMap(t, env.Data)
	token, _ := sess["token"].(string)
	if token == "" || sess["sessionEnabled"] != true {
		t.Fatalf("会话创建异常：%+v", sess)
	}

	// 仅凭 X-Token 即可还原身份
	code, env = h.request(t, http.MethodPost, "/api/orders",
		map[string]any{"productId": "P1", "qty": 1}, model.Actor{}, token)
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["buyerId"]; got != "u002" {
		t.Fatalf("token 身份还原错误：buyerId=%v", got)
	}

	// 无效 token 回退到合法请求头
	code, env = h.request(t, http.MethodGet, "/api/summary", nil, opsA, "invalid-token")
	assertCode(t, code, env, http.StatusOK, "")

	// 无任何身份 → 403
	code, env = h.request(t, http.MethodPost, "/api/orders",
		map[string]any{"productId": "P1", "qty": 1}, model.Actor{}, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	// 角色与账号不匹配 → 403（名字由服务端账号表校验，不可伪造）
	code, env = h.request(t, http.MethodGet, "/api/summary", nil,
		model.Actor{Role: model.RoleOps, UserID: "u001"}, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	// 非法账号登录被拒
	code, env = h.request(t, http.MethodPost, "/api/session",
		map[string]string{"role": "ops", "userId": "u001"}, model.Actor{}, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)
}

func TestAPI_NoopCacheSessionDisabled(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(st, "secret", nil)
	h := &apiHarness{handler: New(svc, t.TempDir(), cache.Noop{}).Handler()}
	code, env := h.request(t, http.MethodPost, "/api/session",
		map[string]string{"role": "buyer", "userId": "u001"}, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["sessionEnabled"]; got != false {
		t.Fatalf("Redis 不可用时 sessionEnabled 应为 false，实际 %v", got)
	}
}

func TestAPI_RoleGuardMatrix(t *testing.T) {
	h := newAPIHarness(t)
	cases := []struct {
		name, method, path string
		body               any
		actor              model.Actor
	}{
		{"buyer_must_not_manage_products", http.MethodPost, "/api/products/P1/onsale", map[string]bool{"onSale": false}, buyerA},
		{"buyer_must_not_ship", http.MethodPost, "/api/orders/NO20260001/ship", map[string]string{}, buyerA},
		{"ops_must_not_place_order", http.MethodPost, "/api/orders", map[string]any{"productId": "P1", "qty": 1}, opsA},
		{"buyer_must_not_review_refund", http.MethodPost, "/api/refunds/RF-X/review", map[string]any{"approve": true}, buyerA},
		{"buyer_must_not_handle_abnormal", http.MethodPost, "/api/abnormal/ABN-X/handle", map[string]string{}, buyerA},
		{"buyer_must_not_reset", http.MethodPost, "/api/reset", map[string]string{}, buyerA},
		{"buyer_must_not_read_summary", http.MethodGet, "/api/summary", nil, buyerA},
		{"buyer_must_not_read_state", http.MethodGet, "/api/state", nil, buyerA},
		{"cs_must_not_read_ledger", http.MethodGet, "/api/ledger", nil, csA},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, env := h.request(t, c.method, c.path, c.body, c.actor, "")
			assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)
		})
	}
	// 越权尝试后数据未改动
	code, env := h.request(t, http.MethodGet, "/api/summary", nil, opsA, "")
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["orderCount"]; got != float64(1) {
		t.Fatalf("越权请求改变了数据：orderCount=%v", got)
	}
}

func TestAPI_ErrorMappingAndBadRequest(t *testing.T) {
	h := newAPIHarness(t)

	code, env := h.request(t, http.MethodPost, "/api/orders",
		map[string]any{"productId": "P1", "qty": 0}, buyerA, "")
	assertCode(t, code, env, http.StatusBadRequest, service.CodeBadRequest)

	rr := h.raw(http.MethodPost, "/api/orders", strings.NewReader("{not-json"), buyerA, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应返回 400，实际 %d", rr.Code)
	}

	code, env = h.request(t, http.MethodGet, "/api/orders/NO-NOT-EXIST", nil, opsA, "")
	assertCode(t, code, env, http.StatusNotFound, service.CodeNotFound)

	code, env = h.request(t, http.MethodGet, "/api/not-exist", nil, opsA, "")
	assertCode(t, code, env, http.StatusNotFound, service.CodeNotFound)

	code, env = h.request(t, http.MethodPost, "/api/orders/NO20260001/ship", map[string]string{}, whA, "")
	assertCode(t, code, env, http.StatusOK, "")
	code, env = h.request(t, http.MethodPost, "/api/orders/NO20260001/ship", map[string]string{}, whA, "")
	assertCode(t, code, env, http.StatusConflict, service.CodeConflict)
}

func TestAPI_OrderFlowOverHTTP(t *testing.T) {
	h := newAPIHarness(t)
	code, env := h.request(t, http.MethodPost, "/api/orders",
		map[string]any{"productId": "P1", "qty": 1}, buyerB, "")
	assertCode(t, code, env, http.StatusOK, "")
	order := asMap(t, env.Data)
	id, _ := order["id"].(string)
	if id == "" || order["status"] != string(model.StatusPendingPay) {
		t.Fatalf("下单响应异常：%+v", order)
	}

	code, env = h.request(t, http.MethodPost, "/api/orders/"+id+"/pay-intent", map[string]string{}, buyerB, "")
	assertCode(t, code, env, http.StatusOK, "")
	intent := asMap(t, env.Data)
	if intent["signature"] == "" || intent["channelTxnId"] == "" {
		t.Fatalf("支付通知缺少签名或交易号：%+v", intent)
	}

	code, env = h.request(t, http.MethodPost, "/api/payments/notify", intent, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["outcome"]; got != "ACCEPTED" {
		t.Fatalf("支付通知结果异常：%v", got)
	}
	code, env = h.request(t, http.MethodPost, "/api/payments/notify", intent, model.Actor{}, "")
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["outcome"]; got != "DUPLICATE" {
		t.Fatalf("重复通知应判 DUPLICATE，实际 %v", got)
	}

	code, env = h.request(t, http.MethodPost, "/api/orders/"+id+"/ship", map[string]string{}, whA, "")
	assertCode(t, code, env, http.StatusOK, "")
	code, env = h.request(t, http.MethodGet, "/api/orders/"+id, nil, opsA, "")
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["status"]; got != string(model.StatusShipped) {
		t.Fatalf("发货后状态异常：%v", got)
	}

	code, env = h.request(t, http.MethodGet, "/api/summary", nil, opsA, "")
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["receivedFen"]; got != float64(20000) {
		t.Fatalf("收款总额应为一笔 10000 + 预置 10000，实际 %v", got)
	}
}

func TestAPI_BuyerDataIsolation(t *testing.T) {
	h := newAPIHarness(t)
	code, env := h.request(t, http.MethodPost, "/api/orders",
		map[string]any{"productId": "P2", "qty": 1}, buyerB, "")
	assertCode(t, code, env, http.StatusOK, "")
	id, _ := asMap(t, env.Data)["id"].(string)

	code, env = h.request(t, http.MethodGet, "/api/orders/"+id, nil, buyerA, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)
	code, env = h.request(t, http.MethodPost, "/api/orders/"+id+"/cancel", map[string]string{}, buyerA, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	code, env = h.request(t, http.MethodGet, "/api/orders", nil, buyerA, "")
	assertCode(t, code, env, http.StatusOK, "")
	for _, raw := range asSlice(t, env.Data) {
		if o, ok := raw.(map[string]any); ok && o["id"] == id {
			t.Fatal("买家 u001 的订单列表出现他人订单")
		}
	}
	code, env = h.request(t, http.MethodGet, "/api/orders/"+id, nil, buyerB, "")
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["status"]; got != string(model.StatusPendingPay) {
		t.Fatalf("越权尝试改变了订单状态：%v", got)
	}
}

func TestAPI_ChannelNotifyPermissionRegression(t *testing.T) {
	h := newAPIHarness(t)
	code, env := h.request(t, http.MethodPost, "/api/orders",
		map[string]any{"productId": "P2", "qty": 1}, buyerB, "")
	assertCode(t, code, env, http.StatusOK, "")
	id, _ := asMap(t, env.Data)["id"].(string)

	code, env = h.request(t, http.MethodPost, "/api/orders/"+id+"/pay-intent", map[string]string{}, buyerB, "")
	assertCode(t, code, env, http.StatusOK, "")
	intent := asMap(t, env.Data)
	h.request(t, http.MethodPost, "/api/payments/notify", intent, model.Actor{}, "")

	code, env = h.request(t, http.MethodPost, "/api/orders/"+id+"/refund-apply",
		map[string]string{"reason": "unit"}, buyerB, "")
	assertCode(t, code, env, http.StatusOK, "")
	rid, _ := asMap(t, env.Data)["id"].(string)
	h.request(t, http.MethodPost, "/api/refunds/"+rid+"/review",
		map[string]any{"approve": true, "note": "ok"}, csA, "")

	// 买家自助确认渠道退款成功必须被拒绝
	code, env = h.request(t, http.MethodPost, "/api/refunds/"+rid+"/channel-notify",
		map[string]bool{"success": true}, buyerB, "")
	assertCode(t, code, env, http.StatusForbidden, service.CodeForbidden)

	code, env = h.request(t, http.MethodGet, "/api/refunds", nil, csA, "")
	assertCode(t, code, env, http.StatusOK, "")
	rows := asSlice(t, env.Data)
	if len(rows) != 1 || rows[0].(map[string]any)["status"] != string(model.RefundAwait) {
		t.Fatalf("越权尝试改变了退款单状态：%+v", rows)
	}

	// 客服触发应成功
	code, env = h.request(t, http.MethodPost, "/api/refunds/"+rid+"/channel-notify",
		map[string]bool{"success": true}, csA, "")
	assertCode(t, code, env, http.StatusOK, "")
	if got := asMap(t, env.Data)["status"]; got != string(model.RefundSucceeded) {
		t.Fatalf("客服确认退款结果异常：%v", got)
	}
}

func TestAPI_PaymentNotifyRejectionEnvelope(t *testing.T) {
	h := newAPIHarness(t)
	code, env := h.request(t, http.MethodPost, "/api/orders",
		map[string]any{"productId": "P1", "qty": 1}, buyerB, "")
	assertCode(t, code, env, http.StatusOK, "")
	id, _ := asMap(t, env.Data)["id"].(string)
	code, env = h.request(t, http.MethodPost, "/api/orders/"+id+"/pay-intent", map[string]string{}, buyerB, "")
	assertCode(t, code, env, http.StatusOK, "")
	intent := asMap(t, env.Data)

	bad := map[string]any{}
	for k, v := range intent {
		bad[k] = v
	}
	bad["signature"] = "invalid-signature"
	code, env = h.request(t, http.MethodPost, "/api/payments/notify", bad, model.Actor{}, "")
	if code != http.StatusBadRequest || env.OK {
		t.Fatalf("验签失败应返回 400 且 ok=false：code=%d env=%+v", code, env)
	}
	if got := asMap(t, env.Data)["outcome"]; got != "BAD_SIGNATURE" {
		t.Fatalf("验签失败结果应为 BAD_SIGNATURE，实际 %v", got)
	}

	code, env = h.request(t, http.MethodGet, "/api/notify-results", nil, opsA, "")
	assertCode(t, code, env, http.StatusOK, "")
	if len(asSlice(t, env.Data)) != 1 {
		t.Fatal("被拒绝的支付通知必须落流水并可查询")
	}
}
