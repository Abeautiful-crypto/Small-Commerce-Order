// Package service 是业务规则的唯一落点：所有状态迁移、库存变更、幂等判定与权限校验都在此完成。
package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"small-commerce-order/internal/cache"
	"small-commerce-order/internal/model"
	"small-commerce-order/internal/store"
)

const (
	CodeBadRequest = "BAD_REQUEST"
	CodeForbidden  = "FORBIDDEN"
	CodeNotFound   = "NOT_FOUND"
	CodeConflict   = "CONFLICT"
)

// BizError 带业务码的错误，便于 API 层映射 HTTP 状态。
type BizError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *BizError) Error() string { return e.Message }

func bad(msg string) error       { return &BizError{Code: CodeBadRequest, Message: msg} }
func forbidden(msg string) error { return &BizError{Code: CodeForbidden, Message: msg} }
func notFound(msg string) error  { return &BizError{Code: CodeNotFound, Message: msg} }
func conflict(msg string) error  { return &BizError{Code: CodeConflict, Message: msg} }

type Service struct {
	st     store.StateStore
	cache  cache.Cache
	now    func() time.Time
	secret string
}

func New(st store.StateStore, secret string, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	if secret == "" {
		secret = "demo-channel-secret"
	}
	return &Service{st: st, cache: cache.Noop{}, now: now, secret: secret}
}

// WithCache 注入 Redis 缓存（仅加速/辅助去重，不参与正确性判定）。
func (s *Service) WithCache(c cache.Cache) *Service {
	if c != nil {
		s.cache = c
	}
	return s
}

// ---------------- 权限守卫 ----------------

func requireRole(actor model.Actor, roles ...model.Role) error {
	for _, r := range roles {
		if actor.Role == r {
			if _, ok := model.FindUser(actor.UserID, actor.Role); ok {
				return nil
			}
		}
	}
	return forbidden("无权访问：当前身份「" + roleLabel(actor.Role) + "」不具备该操作权限")
}

func roleLabel(r model.Role) string {
	switch r {
	case model.RoleBuyer:
		return "买家"
	case model.RoleOps:
		return "运营"
	case model.RoleWarehouse:
		return "仓库"
	case model.RoleCS:
		return "客服"
	case model.RoleChannel:
		return "模拟支付渠道"
	}
	return "未登录"
}

func requireOwner(actor model.Actor, o *model.Order) error {
	if actor.Role == model.RoleBuyer && o.BuyerID != actor.UserID {
		return forbidden("无权访问：只能操作自己的订单")
	}
	return nil
}

// ---------------- 模拟渠道签名（R3 通知鉴权） ----------------

func (s *Service) sign(channelTxnID, orderID string, amountFen int64, status, nonce string) string {
	raw := fmt.Sprintf("%s|%s|%d|%s|%s", channelTxnID, orderID, amountFen, status, nonce)
	mac := hmac.New(sha256.New, []byte(s.secret))
	_, _ = mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) verify(in model.PaymentIntent) bool {
	if in.Signature == "" {
		return false
	}
	want := s.sign(in.ChannelTxnID, in.OrderID, in.AmountFen, in.Status, in.Nonce)
	return hmac.Equal([]byte(want), []byte(in.Signature))
}

// ---------------- 查询（只读快照） ----------------

func (s *Service) snapshot() (*model.State, error) { return s.st.Snapshot() }

func (s *Service) Products(actor model.Actor) ([]*model.Product, error) {
	if err := requireRole(actor, model.RoleBuyer, model.RoleOps, model.RoleWarehouse, model.RoleCS); err != nil {
		return nil, err
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	// Cache-Aside：命中 stock:{id} 用缓存值，未命中回写；写事务提交后由 store 主动 DEL。
	for _, p := range st.Products {
		if n, ok := s.cache.Stock(p.ID); ok {
			p.Available = n
		} else {
			s.cache.SetStock(p.ID, p.Available)
		}
	}
	return st.Products, nil
}

// Orders 按角色返回可见订单：买家仅自己的；仓库仅待发货；运营/客服看全部。
func (s *Service) Orders(actor model.Actor) ([]*model.Order, error) {
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	switch actor.Role {
	case model.RoleBuyer:
		if err := requireRole(actor, model.RoleBuyer); err != nil {
			return nil, err
		}
		out := []*model.Order{}
		for _, o := range st.Orders {
			if o.BuyerID == actor.UserID {
				out = append(out, o)
			}
		}
		return out, nil
	case model.RoleOps, model.RoleCS:
		if err := requireRole(actor, model.RoleOps, model.RoleCS); err != nil {
			return nil, err
		}
		return st.Orders, nil
	case model.RoleWarehouse:
		if err := requireRole(actor, model.RoleWarehouse); err != nil {
			return nil, err
		}
		out := []*model.Order{}
		for _, o := range st.Orders {
			if o.Status == model.StatusPendingShip {
				out = append(out, o)
			}
		}
		return out, nil
	}
	return nil, forbidden("无权访问订单列表")
}

func (s *Service) GetOrder(actor model.Actor, id string) (*model.Order, error) {
	if err := requireRole(actor, model.RoleBuyer, model.RoleOps, model.RoleWarehouse, model.RoleCS); err != nil {
		return nil, err
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	o := st.Order(id)
	if o == nil {
		return nil, notFound("订单不存在")
	}
	if err := requireOwner(actor, o); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Service) Refunds(actor model.Actor) ([]*model.Refund, error) {
	if err := requireRole(actor, model.RoleCS, model.RoleOps); err != nil {
		return nil, err
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	return st.Refunds, nil
}

func (s *Service) AbnormalPayments(actor model.Actor) ([]*model.AbnormalPayment, error) {
	if err := requireRole(actor, model.RoleCS, model.RoleOps); err != nil {
		return nil, err
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	return st.AbnormalPayments, nil
}

func (s *Service) Shipments(actor model.Actor) ([]*model.Shipment, error) {
	if err := requireRole(actor, model.RoleWarehouse, model.RoleOps); err != nil {
		return nil, err
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	return st.Shipments, nil
}

func (s *Service) Ledger(actor model.Actor) ([]*model.LedgerEntry, error) {
	if err := requireRole(actor, model.RoleOps); err != nil {
		return nil, err
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	return st.Ledger, nil
}

func (s *Service) NotifyResults(actor model.Actor) ([]*model.NotifyResult, error) {
	if err := requireRole(actor, model.RoleOps); err != nil {
		return nil, err
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	return st.NotifyResults, nil
}

// Summary 运营对账汇总（R8）。
type Summary struct {
	ReceivedFen  int64 `json:"receivedFen"`
	RefundedFen  int64 `json:"refundedFen"`
	NetFen       int64 `json:"netFen"`
	OrderCount   int   `json:"orderCount"`
	ShippedCount int   `json:"shippedCount"`
	AbnormalCnt  int   `json:"abnormalCount"`
	LedgerCount  int   `json:"ledgerCount"`
}

func (s *Service) Summary(actor model.Actor) (*Summary, error) {
	if err := requireRole(actor, model.RoleOps); err != nil {
		return nil, err
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	out := &Summary{OrderCount: len(st.Orders), LedgerCount: len(st.Ledger)}
	for _, o := range st.Orders {
		if o.Status == model.StatusShipped {
			out.ShippedCount++
		}
	}
	out.AbnormalCnt = len(st.AbnormalPayments)
	for _, l := range st.Ledger {
		switch l.Type {
		case "收款":
			out.ReceivedFen += l.AmountFen
		case "退款":
			out.RefundedFen += l.AmountFen
		}
	}
	out.NetFen = out.ReceivedFen - out.RefundedFen
	return out, nil
}

// Reset 恢复演示预置数据（仅运营）。
func (s *Service) Reset(actor model.Actor) error {
	if err := requireRole(actor, model.RoleOps); err != nil {
		return err
	}
	return s.st.Reset()
}

// State 返回完整状态快照，供运营核查页一次性拉取（越权不可见其它角色专属数据由前端控制）。
func (s *Service) State(actor model.Actor) (*model.State, error) {
	if err := requireRole(actor, model.RoleOps); err != nil {
		return nil, err
	}
	return s.snapshot()
}
