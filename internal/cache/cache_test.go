package cache

import (
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func newTestRedis(t *testing.T) (*Redis, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	r, err := Open(mr.Addr(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, mr
}

// Cache-Aside 语义：未命中→回源写入→命中读取→写失效后未命中。
func TestRedis_StockCacheAside(t *testing.T) {
	r, _ := newTestRedis(t)

	if _, ok := r.Stock("P1"); ok {
		t.Fatal("初始应为未命中")
	}
	r.SetStock("P1", 4)
	n, ok := r.Stock("P1")
	if !ok || n != 4 {
		t.Fatalf("应命中 4，实际 %d %v", n, ok)
	}
	r.InvalidateStock("P1")
	if _, ok := r.Stock("P1"); ok {
		t.Fatal("失效后应未命中")
	}
	// 键损坏（非数字）按未命中处理
	r.client.Set(r.ctx, stockKey("P2"), "not-a-number", 0)
	if _, ok := r.Stock("P2"); ok {
		t.Fatal("非数字值应按未命中处理")
	}
	// 空商品 ID 防御
	r.InvalidateStock("")
}

// 支付去重：SETNX + Exists，重复交易号在 TTL 内被识别。
func TestRedis_PaymentDedup(t *testing.T) {
	r, _ := newTestRedis(t)
	if r.PaymentSeen("TXN-A") {
		t.Fatal("未登记的交易号不应视为已见")
	}
	r.MarkPayment("TXN-A")
	if !r.PaymentSeen("TXN-A") {
		t.Fatal("已登记交易号应被识别")
	}
	r.MarkPayment("") // 空交易号防御
	if r.PaymentSeen("") {
		t.Fatal("空交易号应返回 false")
	}
}

// 会话令牌：保存→加载往返，24h TTL；空令牌与损坏值按未命中处理。
func TestRedis_Session(t *testing.T) {
	r, _ := newTestRedis(t)
	if err := r.SaveSession("tok-1", "buyer", "u001"); err != nil {
		t.Fatal(err)
	}
	role, uid, ok := r.LoadSession("tok-1")
	if !ok || role != "buyer" || uid != "u001" {
		t.Fatalf("会话往返不符：%s %s %v", role, uid, ok)
	}
	if _, _, ok := r.LoadSession(""); ok {
		t.Fatal("空令牌应未命中")
	}
	r.client.Set(r.ctx, sessionKey("tok-bad"), "no-separator", 0)
	if _, _, ok := r.LoadSession("tok-bad"); ok {
		t.Fatal("损坏的会话值应未命中")
	}
}

// Open 错误路径：未配置地址、连不上的地址。
func TestOpen_Errors(t *testing.T) {
	if _, err := Open("", "", 0); err == nil {
		t.Fatal("空地址应报错")
	}
	if _, err := Open("127.0.0.1:1", "", 0); err == nil {
		t.Fatal("连不上的地址应报错")
	}
}

// Noop 降级实现语义：全部静默不命中，SaveSession/Ping 报错。
func TestNoop(t *testing.T) {
	n := Noop{}
	if _, ok := n.Stock("P1"); ok {
		t.Fatal("Noop 库存应未命中")
	}
	n.SetStock("P1", 4)
	n.InvalidateStock("P1")
	n.MarkPayment("T")
	if n.PaymentSeen("T") {
		t.Fatal("Noop 支付去重应未命中")
	}
	if err := n.SaveSession("t", "buyer", "u001"); err == nil {
		t.Fatal("Noop 会话保存应报错（登录态不可用）")
	}
	if _, _, ok := n.LoadSession("t"); ok {
		t.Fatal("Noop 会话加载应未命中")
	}
	if err := n.Ping(); err == nil {
		t.Fatal("Noop Ping 应报错")
	}
}

// NewToken：非空且两次生成不同。
func TestNewToken(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == "" || b == "" || a == b {
		t.Fatalf("令牌应非空且不重复：%s %s", a, b)
	}
}

// miniredis 关闭后所有操作按未命中降级，不 panic。
func TestRedis_AfterClose(t *testing.T) {
	r, mr := newTestRedis(t)
	r.SetStock("P1", 4)
	mr.Close()
	if _, ok := r.Stock("P1"); ok {
		t.Fatal("Redis 关闭后应未命中")
	}
	r.SetStock("P1", 3) // 记录日志但不 panic
	r.InvalidateStock("P1")
	r.MarkPayment("T")
	var _ = errors.New
}
