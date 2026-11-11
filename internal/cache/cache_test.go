package cache

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// Redis 集成测试：设置 REDIS_TEST_ADDR 后执行，未设置则跳过。
func TestRedisIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("未设置 REDIS_TEST_ADDR，跳过 Redis 集成测试")
	}
	c, err := Open(addr, os.Getenv("REDIS_TEST_PASSWORD"), 0)
	if err != nil {
		t.Fatalf("连接 Redis 失败：%v", err)
	}
	defer c.Close()
	ctx := c.ctx

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	prod := "TEST-P-" + suffix
	txn := "TEST-TXN-" + suffix
	tok := NewToken()
	defer c.client.Del(ctx, stockKey(prod), payKey(txn), sessionKey(tok))

	// stock:{id}：Cache-Aside 读缓存 + 写失效（DEL）
	c.InvalidateStock(prod)
	if _, ok := c.Stock(prod); ok {
		t.Fatal("未写入时不应命中缓存")
	}
	c.SetStock(prod, 7)
	if n, ok := c.Stock(prod); !ok || n != 7 {
		t.Fatalf("库存缓存读取异常：n=%d ok=%v", n, ok)
	}
	c.InvalidateStock(prod)
	if _, ok := c.Stock(prod); ok {
		t.Fatal("写事务提交后必须 DEL 缓存键")
	}

	// pay_dedup:{txn}：SETNX + TTL，重复标记幂等
	if c.PaymentSeen(txn) {
		t.Fatal("首次投递前不应命中去重键")
	}
	c.MarkPayment(txn)
	c.MarkPayment(txn)
	if !c.PaymentSeen(txn) {
		t.Fatal("SETNX 去重键未生效")
	}
	ttl, err := c.client.TTL(ctx, payKey(txn)).Result()
	if err != nil || ttl <= 0 {
		t.Fatalf("去重键必须带 TTL：ttl=%v err=%v", ttl, err)
	}

	// sess:{token}：登录态与角色
	if err := c.SaveSession(tok, "buyer", "u001"); err != nil {
		t.Fatal(err)
	}
	role, uid, ok := c.LoadSession(tok)
	if !ok || role != "buyer" || uid != "u001" {
		t.Fatalf("会话解析异常：role=%s uid=%s ok=%v", role, uid, ok)
	}
	if _, _, ok := c.LoadSession("invalid-token"); ok {
		t.Fatal("无效令牌不应解析出身份")
	}
}

// Noop 是降级实现，任何操作都不能影响正确性判定。
func TestNoopNeverHits(t *testing.T) {
	var c Cache = Noop{}
	if _, ok := c.Stock("P1"); ok {
		t.Fatal("Noop 不应命中缓存")
	}
	if c.PaymentSeen("TXN") {
		t.Fatal("Noop 不应命中去重")
	}
	if _, _, ok := c.LoadSession("t"); ok {
		t.Fatal("Noop 不应解析会话")
	}
	c.SetStock("P1", 1)
	c.MarkPayment("TXN")
	c.InvalidateStock("P1")
}
