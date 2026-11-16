package service

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"small-commerce-order/internal/model"
)

// OrderCard 是买家在会话里发送的「订单卡片」快照：客服据此定位订单，
// 即使之后订单状态变化，卡片仍保留发送当时的上下文（status 字段为发送时状态）。
type OrderCard struct {
	OrderID      string            `json:"orderId"`
	ProductName  string            `json:"productName"`
	ProductImage string            `json:"productImage"`
	Status       model.OrderStatus `json:"status"`
	StatusLabel  string            `json:"statusLabel"`
	AmountFen    int64             `json:"amountFen"`
	Qty          int               `json:"qty"`
	CreatedAt    time.Time         `json:"createdAt"`
}

// ChatThread 客服端会话摘要：按订单聚合，按最后一条消息倒序。
type ChatThread struct {
	OrderID      string            `json:"orderId"`
	BuyerID      string            `json:"buyerId"`
	BuyerName    string            `json:"buyerName"`
	ProductName  string            `json:"productName"`
	AmountFen    int64             `json:"amountFen"`
	OrderStatus  model.OrderStatus `json:"orderStatus"`
	LastKind     string            `json:"lastKind"`
	LastText     string            `json:"lastText"`
	LastFromRole model.Role        `json:"lastFromRole"`
	LastAt       time.Time         `json:"lastAt"`
	MsgCount     int               `json:"msgCount"`
}

const maxChatRunes = 500

// ChatThreads 会话摘要列表：
//   - 客服：返回全部会话（用于客服消息工作台与未读徽标）
//   - 买家：只返回自己订单的会话（用于「客服已回复」提示与红点）
//   - 其它角色无权访问
func (s *Service) ChatThreads(actor model.Actor) ([]*ChatThread, error) {
	switch actor.Role {
	case model.RoleBuyer, model.RoleCS:
	default:
		return nil, forbidden("无权访问会话列表")
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	byOrder := map[string]*ChatThread{}
	for _, m := range st.ChatMsgs {
		th, ok := byOrder[m.OrderID]
		if !ok {
			th = &ChatThread{OrderID: m.OrderID}
			if o := st.Order(m.OrderID); o != nil {
				th.BuyerID, th.BuyerName = o.BuyerID, o.BuyerName
				th.ProductName, th.AmountFen, th.OrderStatus = o.ProductName, o.AmountFen, o.Status
			}
			byOrder[m.OrderID] = th
		}
		th.MsgCount++
		if !m.CreatedAt.Before(th.LastAt) {
			th.LastAt = m.CreatedAt
			th.LastKind = m.Kind
			th.LastText = m.Text
			th.LastFromRole = m.FromRole
		}
	}
	out := make([]*ChatThread, 0, len(byOrder))
	for _, th := range byOrder {
		if actor.Role == model.RoleBuyer && th.BuyerID != actor.UserID {
			continue // 买家只看自己订单的会话
		}
		out = append(out, th)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out, nil
}

// ChatMessages 返回某订单的会话消息：买家只能看自己的订单，客服可看全部。
func (s *Service) ChatMessages(actor model.Actor, orderID string) ([]*model.ChatMsg, error) {
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	if err := requireChatAccess(actor, st, orderID); err != nil {
		return nil, err
	}
	return st.ChatMsgsOf(orderID), nil
}

// SendChat 买家 / 客服在订单会话里发消息。
// kind=order 时由服务端生成订单卡片快照（前端无法伪造金额与状态）。
func (s *Service) SendChat(actor model.Actor, orderID, kind, text string) (*model.ChatMsg, error) {
	text = strings.TrimSpace(text)
	var out *model.ChatMsg
	err := s.st.Update(func(st *model.State) error {
		if err := requireChatAccess(actor, st, orderID); err != nil {
			return err
		}
		o := st.Order(orderID)
		switch kind {
		case "order":
			card := OrderCard{
				OrderID: o.ID, ProductName: o.ProductName, ProductImage: o.ProductImage,
				Status: o.Status, StatusLabel: o.Status.Label(),
				AmountFen: o.AmountFen, Qty: o.Qty, CreatedAt: o.CreatedAt,
			}
			b, err := json.Marshal(card)
			if err != nil {
				return bad("生成订单卡片失败")
			}
			text = string(b)
		case "text", "":
			kind = "text"
			if text == "" {
				return bad("消息内容不能为空")
			}
			if n := len([]rune(text)); n > maxChatRunes {
				return bad("单条消息最多 500 字")
			}
		default:
			return bad("消息类型不支持")
		}
		m := &model.ChatMsg{
			ID: st.NextID("CM"), OrderID: orderID, FromRole: actor.Role,
			FromID: actor.UserID, FromName: actor.Name, Kind: kind, Text: text, CreatedAt: s.now(),
		}
		st.ChatMsgs = append(st.ChatMsgs, m)
		out = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// requireChatAccess 会话访问校验：买家只能碰自己的订单，客服可碰全部，其它角色拒绝。
func requireChatAccess(actor model.Actor, st *model.State, orderID string) error {
	o := st.Order(orderID)
	if o == nil {
		return notFound("订单不存在")
	}
	switch actor.Role {
	case model.RoleBuyer:
		return requireOwner(actor, o)
	case model.RoleCS:
		return nil
	default:
		return forbidden("无权访问：只有买家本人与客服可以进入订单会话")
	}
}
