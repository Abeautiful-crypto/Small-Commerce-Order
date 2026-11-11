// Package store 提供事务式状态存储的两种实现：
//   - MySQLStore：生产真相源，SQL 事务 + 行锁 + 条件更新 + 唯一索引；
//   - Store（内存）：零依赖回退与单元测试用的等价语义实现。
//
// 两者都实现 StateStore：Update 在状态副本上执行业务意图，失败整体回滚；
// 写回时由具体存储把库存变更与状态迁移落成原子条件更新。
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"small-commerce-order/internal/model"
)

// StateStore 是 service 层依赖的抽象（依赖倒置，方便测试与切换存储）。
type StateStore interface {
	Update(fn func(*model.State) error) error
	Snapshot() (*model.State, error)
	Reset() error
	Close() error
}

type Store struct {
	mu    sync.RWMutex
	state *model.State
	path  string
	now   func() time.Time
}

// Open 打开状态文件；不存在时写入预置数据。
func Open(path string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	s := &Store{path: path, now: now}
	data, err := os.ReadFile(path)
	if err == nil {
		var st model.State
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, fmt.Errorf("状态文件损坏：%w", err)
		}
		st = normalize(&st)
		s.state = &st
		return s, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	s.state = newSeed(now())
	if err := s.persist(); err != nil {
		return nil, err
	}
	return s, nil
}

func normalize(st *model.State) model.State {
	if st.Products == nil {
		st.Products = []*model.Product{}
	}
	if st.Orders == nil {
		st.Orders = []*model.Order{}
	}
	if st.Payments == nil {
		st.Payments = []*model.PaymentRecord{}
	}
	if st.NotifyResults == nil {
		st.NotifyResults = []*model.NotifyResult{}
	}
	if st.Refunds == nil {
		st.Refunds = []*model.Refund{}
	}
	if st.Shipments == nil {
		st.Shipments = []*model.Shipment{}
	}
	if st.AbnormalPayments == nil {
		st.AbnormalPayments = []*model.AbnormalPayment{}
	}
	if st.Ledger == nil {
		st.Ledger = []*model.LedgerEntry{}
	}
	return *st
}

// newSeed 预置 2 个商品、1 条待发货订单与 1 条收款流水（与原型一致）。
func newSeed(now time.Time) *model.State {
	st := &model.State{
		Products: []*model.Product{
			{ID: "P1", Name: "无线蓝牙耳机 入耳式降噪 白", Image: "images/product1.png", PriceFen: 10000, OldPriceFen: 19900, Total: 5, Available: 4, OnSale: true, Sales: 12000},
			{ID: "P2", Name: "坚果零食大礼包 每日坚果 红金礼盒", Image: "images/product2.png", PriceFen: 10000, OldPriceFen: 15900, Total: 8, Available: 8, OnSale: true, Sales: 8600},
		},
	}
	created := now.Add(-3 * time.Minute)
	st.Orders = append(st.Orders, &model.Order{
		ID: "NO20260001", BuyerID: "u001", BuyerName: "张小买",
		ProductID: "P1", ProductName: "无线蓝牙耳机 入耳式降噪 白", ProductImage: "images/product1.png",
		PriceFen: 10000, Qty: 1, AmountFen: 10000, Status: model.StatusPendingShip,
		CreatedAt: created, ExpiresAt: created.Add(10 * time.Minute),
		StatusUpdatedAt: created,
	})
	paidAt := created.Add(3 * time.Minute)
	st.Orders[0].PaidAt = &paidAt
	st.Orders[0].ChannelTxnID = "TXN-SEED-0001"
	st.Payments = append(st.Payments, &model.PaymentRecord{
		ChannelTxnID: "TXN-SEED-0001", OrderID: "NO20260001", AmountFen: 10000, CreatedAt: paidAt,
	})
	st.AppendLedger("库存占用", 10000, "NO20260001", "P1", "预置订单占用 1 件", created)
	st.AppendLedger("收款", 10000, "NO20260001", "TXN-SEED-0001", "预置订单收款", paidAt)
	return st
}

// Update 执行一个写事务。fn 内所有校验通过后再改动；返回错误则整体回滚。
func (s *Store) Update(fn func(*model.State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := clone(s.state)
	if err != nil {
		return err
	}
	if err := fn(next); err != nil {
		return err
	}
	s.state = next
	return s.persist()
}

// Snapshot 返回状态深拷贝，供查询层自由遍历（不持锁、无数据竞争）。
func (s *Store) Snapshot() (*model.State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.state)
}

// Reset 恢复预置数据，供验收测试与演示重置使用。
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = newSeed(s.now())
	return s.persist()
}

// Close 内存实现无需释放资源。
func (s *Store) Close() error { return nil }

func clone(st *model.State) (*model.State, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	var out model.State
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	norm := normalize(&out)
	return &norm, nil
}

func (s *Store) persist() error {
	if dir := filepath.Dir(s.path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
