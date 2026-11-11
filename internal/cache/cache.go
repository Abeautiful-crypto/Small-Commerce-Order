// Package cache 实现 Redis 缓存层（仅加速与辅助去重，绝不是数据真相源）。
//
// 用途边界（.agent/backend/tech-stack.md §2.1）：
//
//	stock:{商品id}        热点库存读加速，Cache-Aside；写事务提交后 DEL（写失效）
//	pay_dedup:{交易号}     SETNX + TTL，支付幂等短期去重；MySQL 唯一索引兜底
//	sess:{token}          登录态 / 角色，供接口越权校验
//
// 任何 Redis 故障都降级为不命中，正确性完全由 MySQL 事务 + 唯一索引 + 条件更新保证。
package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Cache interface {
	Stock(productID string) (int, bool)
	SetStock(productID string, n int)
	InvalidateStock(ids ...string)
	PaymentSeen(txnID string) bool
	MarkPayment(txnID string)
	SaveSession(token string, role, userID string) error
	LoadSession(token string) (string, string, bool)
	Ping() error
}

// Noop 是 Redis 不可用时的降级实现。
type Noop struct{}

func (Noop) Stock(string) (int, bool)                  { return 0, false }
func (Noop) SetStock(string, int)                      {}
func (Noop) InvalidateStock(...string)                 {}
func (Noop) PaymentSeen(string) bool                   { return false }
func (Noop) MarkPayment(string)                        {}
func (Noop) SaveSession(string, string, string) error  { return errors.New("缓存未启用") }
func (Noop) LoadSession(string) (string, string, bool) { return "", "", false }
func (Noop) Ping() error                               { return errors.New("缓存未启用") }

type Redis struct {
	client *redis.Client
	ctx    context.Context
}

// Open 连接 Redis，失败时返回错误由调用方降级为 Noop。
func Open(addr, password string, db int) (*Redis, error) {
	if addr == "" {
		return nil, errors.New("未配置 Redis 地址")
	}
	c := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		PoolSize:     20,
	})
	r := &Redis{client: c, ctx: context.Background()}
	if err := r.Ping(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return r, nil
}

func (r *Redis) Ping() error { return r.client.Ping(r.ctx).Err() }

func (r *Redis) Close() error { return r.client.Close() }

func stockKey(id string) string  { return "stock:" + id }
func payKey(txn string) string   { return "pay_dedup:" + txn }
func sessionKey(t string) string { return "sess:" + t }

func (r *Redis) Stock(productID string) (int, bool) {
	v, err := r.client.Get(r.ctx, stockKey(productID)).Result()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (r *Redis) SetStock(productID string, n int) {
	// 随机 TTL 防雪崩（25–40s）；写事务提交后由 InvalidateStock 主动失效
	ttl := time.Duration(25+time.Now().UnixNano()%15) * time.Second
	if err := r.client.Set(r.ctx, stockKey(productID), n, ttl).Err(); err != nil {
		log.Printf("cache set stock failed key=%s err=%v", productID, err)
	}
}

func (r *Redis) InvalidateStock(ids ...string) {
	for _, id := range ids {
		if id == "" {
			continue
		}
		if err := r.client.Del(r.ctx, stockKey(id)).Err(); err != nil {
			log.Printf("cache invalidate stock failed key=%s err=%v", id, err)
		}
	}
}

func (r *Redis) PaymentSeen(txnID string) bool {
	if txnID == "" {
		return false
	}
	n, err := r.client.Exists(r.ctx, payKey(txnID)).Result()
	return err == nil && n > 0
}

func (r *Redis) MarkPayment(txnID string) {
	if txnID == "" {
		return
	}
	if err := r.client.SetNX(r.ctx, payKey(txnID), "1", 24*time.Hour).Err(); err != nil {
		log.Printf("cache pay dedup failed txn=%s err=%v", txnID, err)
	}
}

func (r *Redis) SaveSession(token, role, userID string) error {
	return r.client.Set(r.ctx, sessionKey(token), role+"|"+userID, 24*time.Hour).Err()
}

func (r *Redis) LoadSession(token string) (string, string, bool) {
	if token == "" {
		return "", "", false
	}
	v, err := r.client.Get(r.ctx, sessionKey(token)).Result()
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(v, "|", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// NewToken 生成不可预测的会话令牌。
func NewToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}
