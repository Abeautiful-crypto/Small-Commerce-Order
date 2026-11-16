package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"small-commerce-order/internal/model"
	"small-commerce-order/internal/service"
	"small-commerce-order/internal/store"
)

// sideHarness 构造绑定到某个端的 Server（c=C 端买家页 / b=B 端工作台）。
type sideHarness struct {
	handler http.Handler
	cache   *memCache
	webDir  string
}

func newSideHarness(t *testing.T, side string) *sideHarness {
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
	files := map[string]string{
		"customer.html": "<!doctype html><title>C端买家页</title>",
		"business.html": "<!doctype html><title>B端工作台</title>",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(webDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	server := New(svc, webDir, c).WithSide(side)
	return &sideHarness{handler: server.Handler(), cache: c, webDir: webDir}
}

func (h *sideHarness) call(t *testing.T, method, path string, body any) (int, envelope) {
	t.Helper()
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.handler.ServeHTTP(rr, req)
	var env envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是合法 JSON: %v; body=%s", err, rr.Body.String())
	}
	return rr.Code, env
}

// C 端登录接口必须拒绝 B 端角色，并给出「请到 B 端页面登录」的明确提示。
func TestSideLogin_RejectsWrongSide(t *testing.T) {
	c := newSideHarness(t, model.SideCustomer)
	st, env := c.call(t, "POST", "/api/session", map[string]any{"role": "warehouse", "userId": "wh"})
	if st != http.StatusForbidden {
		t.Fatalf("C 端用仓库账号登录应 403，实际 %d", st)
	}
	if env.Error == nil || !strings.Contains(env.Error.Message, "B 端") {
		t.Fatalf("拒绝提示应指出应到 B 端登录：%+v", env.Error)
	}

	b := newSideHarness(t, model.SideBusiness)
	st, env = b.call(t, "POST", "/api/session", map[string]any{"role": "buyer", "userId": "u001"})
	if st != http.StatusForbidden {
		t.Fatalf("B 端用买家账号登录应 403，实际 %d", st)
	}
	if env.Error == nil || !strings.Contains(env.Error.Message, "C 端") {
		t.Fatalf("拒绝提示应指出应到 C 端登录：%+v", env.Error)
	}
	// 正确端的登录必须成功
	st, env = b.call(t, "POST", "/api/session", map[string]any{"role": "warehouse", "userId": "wh"})
	if st != http.StatusOK || env.Data == nil {
		t.Fatalf("B 端仓库账号登录应成功，实际 %d %+v", st, env.Error)
	}
}

// meta 按端裁剪账号：C 端只有买家，B 端只有工作台角色。
func TestSideMeta_FiltersUsers(t *testing.T) {
	c := newSideHarness(t, model.SideCustomer)
	st, env := c.call(t, "GET", "/api/meta", nil)
	if st != http.StatusOK {
		t.Fatalf("meta 应 200，实际 %d", st)
	}
	m := env.Data.(map[string]any)
	if m["side"] != model.SideCustomer {
		t.Fatalf("C 端 meta.side 应为 c：%v", m["side"])
	}
	for _, u := range m["users"].([]any) {
		role := u.(map[string]any)["role"]
		if role != string(model.RoleBuyer) {
			t.Fatalf("C 端账号列表不应出现 %v", role)
		}
	}

	b := newSideHarness(t, model.SideBusiness)
	_, env = b.call(t, "GET", "/api/meta", nil)
	for _, u := range env.Data.(map[string]any)["users"].([]any) {
		role := u.(map[string]any)["role"]
		if role == string(model.RoleBuyer) {
			t.Fatal("B 端账号列表不应出现买家")
		}
	}
}

// 首页按端返回不同入口页。
func TestSideStatic_EntryPage(t *testing.T) {
	for _, tc := range []struct{ side, want string }{
		{model.SideCustomer, "C端买家页"},
		{model.SideBusiness, "B端工作台"},
	} {
		h := newSideHarness(t, tc.side)
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		h.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), tc.want) {
			t.Fatalf("side=%s 首页应包含 %s，实际 %d %s", tc.side, tc.want, rr.Code, rr.Body.String())
		}
	}
}

// 跨端复用会话令牌：买家令牌只认 C 端，拿到 B 端一律 403。
func TestSideSession_CrossSideDenied(t *testing.T) {
	c := newSideHarness(t, model.SideCustomer)
	st, env := c.call(t, "POST", "/api/session", map[string]any{"role": "buyer", "userId": "u001"})
	if st != http.StatusOK {
		t.Fatalf("C 端买家登录应成功，实际 %d %+v", st, env.Error)
	}
	token := env.Data.(map[string]any)["token"].(string)

	req := httptest.NewRequest("GET", "/api/orders", nil)
	req.Header.Set("X-Token", token)
	rr := httptest.NewRecorder()
	c.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("令牌在所属端应可用，实际 %d", rr.Code)
	}

	// 同一令牌打 B 端：角色仍是买家，属于跨端复用，必须拒绝
	b := newSideHarness(t, model.SideBusiness)
	// 令牌存在 C 端的缓存里，这里把同一份会话搬到 B 端的缓存，模拟共享 Redis
	b.cache.SaveSession(token, "buyer", "u001")
	req = httptest.NewRequest("GET", "/api/shipments", nil)
	req.Header.Set("X-Token", token)
	rr = httptest.NewRecorder()
	b.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("买家令牌访问 B 端应 403，实际 %d", rr.Code)
	}
}
