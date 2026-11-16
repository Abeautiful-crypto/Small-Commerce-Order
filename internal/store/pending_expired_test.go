package store

import (
	"path/filepath"
	"testing"
	"time"

	"small-commerce-order/internal/model"
)

// 内存存储的候选集查询：只返回「待付款且已到期」的订单，按到期时间升序，受 limit 约束。
func TestStore_PendingExpiredIDs(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	s, err := Open(filepath.Join(t.TempDir(), "state.json"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	mk := func(id string, status model.OrderStatus, expires time.Time) *model.Order {
		return &model.Order{ID: id, BuyerID: "u001", ProductID: "P1", Qty: 1,
			AmountFen: 100, Status: status, CreatedAt: expires.Add(-10 * time.Minute),
			ExpiresAt: expires, StatusUpdatedAt: now}
	}
	err = s.Update(func(st *model.State) error {
		st.Orders = append(st.Orders,
			mk("NO-EARLY", model.StatusPendingPay, now.Add(-5*time.Minute)), // 已到期（更早）
			mk("NO-LATE", model.StatusPendingPay, now.Add(-1*time.Minute)),  // 已到期（更晚）
			mk("NO-FUTURE", model.StatusPendingPay, now.Add(5*time.Minute)), // 未到期
			mk("NO-SHIPPED", model.StatusPendingShip, now.Add(-2*time.Minute)),
			mk("NO-CLOSED", model.StatusClosed, now.Add(-3*time.Minute)),
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ids, err := s.PendingExpiredIDs(now, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "NO-EARLY" || ids[1] != "NO-LATE" {
		t.Fatalf("候选集应只含两条到期待付款单且按到期时间升序，实际 %v", ids)
	}

	ids, err = s.PendingExpiredIDs(now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "NO-EARLY" {
		t.Fatalf("limit=1 应只取最早到期的候选，实际 %v", ids)
	}

	// 未来时间方向取候选：不应包含任何未到期订单
	ids, err = s.PendingExpiredIDs(now.Add(-6*time.Minute), 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("早于全部到期时间的查询应返回空，实际 %v", ids)
	}
}
