package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"small-commerce-order/internal/model"
)

// fakeCache 记录库存失效调用，用于验证「写提交后 DEL stock:*」的 Cache-Aside 语义。
type fakeCache struct {
	invalidated []string
}

func (f *fakeCache) Stock(string) (int, bool)                  { return 0, false }
func (f *fakeCache) SetStock(string, int)                      {}
func (f *fakeCache) InvalidateStock(ids ...string)             { f.invalidated = append(f.invalidated, ids...) }
func (f *fakeCache) PaymentSeen(string) bool                   { return false }
func (f *fakeCache) MarkPayment(string)                        {}
func (f *fakeCache) SaveSession(string, string, string) error  { return nil }
func (f *fakeCache) LoadSession(string) (string, string, bool) { return "", "", false }
func (f *fakeCache) Ping() error                               { return nil }

func mustOpen(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// 下单占用库存提交后，必须失效对应商品的库存缓存键。
func TestStore_UpdateInvalidatesChangedStock(t *testing.T) {
	s := mustOpen(t)
	fc := &fakeCache{}
	s.WithCache(fc)

	err := s.Update(func(st *model.State) error {
		st.Products[0].Available -= 1
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.invalidated) != 1 || fc.invalidated[0] != "P1" {
		t.Fatalf("应失效 P1 的库存缓存，实际 %v", fc.invalidated)
	}

	st, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if st.Products[0].Available != 3 {
		t.Fatalf("可售数量应为 3，实际 %d", st.Products[0].Available)
	}
}

// 业务拒绝（fn 返回错误）时事务回滚，缓存不得被误失效。
func TestStore_UpdateRollbackDoesNotInvalidate(t *testing.T) {
	s := mustOpen(t)
	fc := &fakeCache{}
	s.WithCache(fc)

	err := s.Update(func(st *model.State) error {
		st.Products[0].Available -= 1
		return errors.New("业务拒绝")
	})
	if err == nil {
		t.Fatal("fn 返回错误时 Update 必须回滚")
	}
	if len(fc.invalidated) != 0 {
		t.Fatalf("回滚不应失效缓存，实际 %v", fc.invalidated)
	}
	st, _ := s.Snapshot()
	if st.Products[0].Available != 4 {
		t.Fatalf("回滚后可售数量应保持 4，实际 %d", st.Products[0].Available)
	}
}

// 未改动任何商品可售数量的写事务（如追加流水）不应失效缓存。
func TestStore_UpdateWithoutStockChangeSkipsInvalidate(t *testing.T) {
	s := mustOpen(t)
	fc := &fakeCache{}
	s.WithCache(fc)

	if err := s.Update(func(st *model.State) error {
		st.AppendLedger("备注", 0, "", "", "不改库存", time.Now())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(fc.invalidated) != 0 {
		t.Fatalf("库存未变化不应失效缓存，实际 %v", fc.invalidated)
	}
}

// 未挂载缓存的 Store 写事务不得 panic（Noop 语义）。
func TestStore_UpdateWithoutCache(t *testing.T) {
	s := mustOpen(t)
	if err := s.Update(func(st *model.State) error {
		st.Products[1].Available -= 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Reset 恢复种子数据后必须失效全部商品库存缓存。
func TestStore_ResetInvalidatesAllStock(t *testing.T) {
	s := mustOpen(t)
	fc := &fakeCache{}
	s.WithCache(fc)

	if err := s.Update(func(st *model.State) error {
		st.Products[0].Available = 0
		st.Products[1].Available = 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(); err != nil {
		t.Fatal(err)
	}
	if len(fc.invalidated) != 4 || fc.invalidated[2] != "P1" || fc.invalidated[3] != "P2" {
		t.Fatalf("Reset 应失效发生变化的 P1/P2 库存缓存，实际 %v", fc.invalidated)
	}
	st, _ := s.Snapshot()
	if st.Products[0].Available != 4 || st.Products[1].Available != 8 {
		t.Fatalf("Reset 后应恢复种子库存，实际 P1=%d P2=%d", st.Products[0].Available, st.Products[1].Available)
	}
}

// 已有状态文件应被加载而不是重建种子；损坏文件必须报错。
func TestStore_OpenExistingAndCorrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(st *model.State) error {
		st.Products[0].Available -= 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := s2.Snapshot()
	if st.Products[0].Available != 2 {
		t.Fatalf("重启后应从文件恢复可售数量 2，实际 %d", st.Products[0].Available)
	}

	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, nil); err == nil {
		t.Fatal("损坏的状态文件必须报错")
	}
}

// changedStockIDs 的空输入与相等数量分支。
func TestChangedStockIDs_EdgeCases(t *testing.T) {
	if got := changedStockIDs(nil, nil); got != nil {
		t.Fatalf("空输入应返回 nil，实际 %v", got)
	}
	a := newSeed(time.Now())
	b := newSeed(time.Now())
	if got := changedStockIDs(a, b); got != nil {
		t.Fatalf("库存未变化应返回 nil，实际 %v", got)
	}
	b.Products[1].Available -= 1
	got := changedStockIDs(a, b)
	if len(got) != 1 || got[0] != "P2" {
		t.Fatalf("应只报告 P2，实际 %v", got)
	}
}
