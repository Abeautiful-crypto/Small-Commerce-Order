package service

import (
	"errors"
	"testing"

	"small-commerce-order/internal/model"
)

// stockCache 记录读取/写入/失效调用的缓存桩，用于验证 Products 的 Cache-Aside 展示路径。
type stockCache struct {
	stock map[string]int
	hits  map[string]bool
}

func newStockCache(vals map[string]int) *stockCache {
	c := &stockCache{stock: vals, hits: map[string]bool{}}
	return c
}

func (c *stockCache) Stock(id string) (int, bool) {
	v, ok := c.stock[id]
	if ok {
		c.hits[id] = true
	}
	return v, ok
}
func (c *stockCache) SetStock(id string, n int)                 { c.stock[id] = n; delete(c.hits, id) }
func (c *stockCache) InvalidateStock(ids ...string)             {}
func (c *stockCache) PaymentSeen(string) bool                   { return false }
func (c *stockCache) MarkPayment(string)                        {}
func (c *stockCache) SaveSession(string, string, string) error  { return nil }
func (c *stockCache) LoadSession(string) (string, string, bool) { return "", "", false }
func (c *stockCache) Ping() error                               { return nil }

// Orders() 的角色分支：买家只看自己、运营/客服看全部、仓库只看待发货、其他角色 403。
func TestOrders_RoleBranches(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.GetOrder(ops, "NO20260001"); err != nil { // 种子订单存在
		t.Fatal(err)
	}

	o1, err := h.svc.Orders(buyer1)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range o1 {
		if o.BuyerID != "u001" {
			t.Fatalf("买家只能看到自己的订单：%s", o.BuyerID)
		}
	}
	if len(o1) == 0 {
		t.Fatal("买家 u001 至少应有种子订单")
	}

	all, err := h.svc.Orders(ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < len(o1) {
		t.Fatal("运营应看到全部订单")
	}

	csList, err := h.svc.Orders(cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(csList) != len(all) {
		t.Fatal("客服应看到全部订单")
	}

	whList, err := h.svc.Orders(wh)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range whList {
		if o.Status != model.StatusPendingShip {
			t.Fatalf("仓库视图只应包含待发货订单：%s", o.Status)
		}
	}

	channel := model.Actor{Role: model.RoleChannel, UserID: "channel"}
	if _, err := h.svc.Orders(channel); err == nil {
		t.Fatal("渠道角色不应能查订单列表")
	}
}

// GetOrder 边界：订单不存在、越权读取他人订单。
func TestGetOrder_EdgeCases(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.GetOrder(ops, "NO-NOT-EXIST"); err == nil {
		t.Fatal("不存在订单应 404")
	}
	if _, err := h.svc.GetOrder(buyer2, "NO20260001"); err == nil {
		t.Fatal("买家读取他人订单应被拒绝")
	}
	channel := model.Actor{Role: model.RoleChannel, UserID: "channel"}
	if _, err := h.svc.GetOrder(channel, "NO20260001"); err == nil {
		t.Fatal("渠道角色不应能读订单")
	}
}

// Refunds / AbnormalPayments：买家越权被拒；客服与运营可读。
func TestRefundsAndAbnormals_AccessControl(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Refunds(buyer1); err == nil {
		t.Fatal("买家不应能读退款列表")
	}
	if _, err := h.svc.Refunds(cs); err != nil {
		t.Fatalf("客服应能读退款列表：%v", err)
	}
	if _, err := h.svc.Refunds(ops); err != nil {
		t.Fatalf("运营应能读退款列表：%v", err)
	}
	if _, err := h.svc.AbnormalPayments(buyer1); err == nil {
		t.Fatal("买家不应能读异常支付列表")
	}
	if _, err := h.svc.AbnormalPayments(cs); err != nil {
		t.Fatalf("客服应能读异常支付列表：%v", err)
	}
}

// Products 的 Cache-Aside：缓存命中直接用缓存值；未命中回源写缓存。
func TestProducts_CacheAside(t *testing.T) {
	h := newHarness(t)
	c := newStockCache(map[string]int{"P1": 99}) // P1 缓存值故意与存储不一致，验证命中优先
	h.svc.WithCache(c)

	ps, err := h.svc.Products(ops)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.ID == "P1" && p.Available != 99 {
			t.Fatalf("缓存命中应返回缓存值 99，实际 %d", p.Available)
		}
		if p.ID == "P2" && p.Available != 8 {
			t.Fatalf("未命中应回源为真实值 8，实际 %d", p.Available)
		}
	}
	if !c.hits["P1"] {
		t.Fatal("P1 应来自缓存命中")
	}

	// 快照错误路径：注入会失败的存储。
	h2 := newHarness(t)
	h2.svc.st = failingStore{}
	if _, err := h2.svc.Products(ops); err == nil {
		t.Fatal("快照失败应返回错误")
	}
	if _, err := h2.svc.Orders(ops); err == nil {
		t.Fatal("快照失败应返回错误")
	}
	if _, err := h2.svc.GetOrder(ops, "NO1"); err == nil {
		t.Fatal("快照失败应返回错误")
	}
	if _, err := h2.svc.Refunds(cs); err == nil {
		t.Fatal("快照失败应返回错误")
	}
	if _, err := h2.svc.AbnormalPayments(cs); err == nil {
		t.Fatal("快照失败应返回错误")
	}
}

// failingStore 用于触发 snapshot 错误路径。
type failingStore struct{}

func (failingStore) Update(fn func(*model.State) error) error { return errors.New("store down") }
func (failingStore) Snapshot() (*model.State, error)          { return nil, errors.New("store down") }
func (failingStore) Reset() error                             { return errors.New("store down") }
func (failingStore) Close() error                             { return nil }

// Reset 恢复种子数据（内存与 MySQL 两种后端均应幂等）。
func TestService_Reset(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.CreateOrder(buyer1, "P1", 1); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Reset(ops); err != nil {
		t.Fatalf("运营重置失败：%v", err)
	}
	if err := h.svc.Reset(buyer1); err == nil {
		t.Fatal("买家不应能重置数据")
	}
	o, err := h.svc.GetOrder(ops, "NO20260001")
	if err != nil {
		t.Fatalf("重置后种子订单应恢复：%v", err)
	}
	if o.Status != model.StatusPendingShip {
		t.Fatalf("种子订单应为待发货，实际 %s", o.Status)
	}
}
