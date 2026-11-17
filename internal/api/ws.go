package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"small-commerce-order/internal/model"
	"small-commerce-order/internal/service"
)

// ---------- 聊天 WebSocket：实时通道 ----------
//
// 客户端连接 GET /api/chat/ws?token=...（演示模式可回退 role/uid 参数），
// 认证与 HTTP 接口同源：sess:{token} 会话（Redis）+ 账号表校验，跨端令牌同样被拒。
//
// 入站消息（客户端 → 服务端，JSON）：
//   {"type":"sub","orderId":"NO..."}      订阅某订单会话（先过 ChatAccess 权限校验）
//   {"type":"unsub","orderId":"NO..."}    取消订阅
//   {"type":"send","orderId":"NO...","kind":"text|order","text":"...","ref":1}
//                                         发消息（ref 用于把回执对回本条请求）
//   {"type":"ping"}                       应用层探活
//
// 出站消息（服务端 → 客户端）：
//   {"type":"sub_ok","orderId":...} / {"type":"error","message":...}
//   {"type":"sent","ref":1,"msg":{...}}                 发送回执（发给发送者自己）
//   {"type":"send_error","ref":1,"message":...}         发送被拒（越权 / 超长 / 订单不存在）
//   {"type":"chat","orderId":...,"msg":{...}}           新消息实时推送（发给订阅者）
//   {"type":"threads_dirty","orderId":...}              会话列表有变化，客户端拉一次 threads
//   {"type":"pong"}
//
// 断线兜底：前端 WS 断开时自动恢复原有的 4s 弹窗轮询 + 8s 全局轮询，
// HTTP 收发接口原样保留，WS 只是加速通道。

const (
	wsWriteWait  = 10 * time.Second // 单次写超时
	wsPongWait   = 60 * time.Second // 读超时：超过没收到任何数据/pong 就断开
	wsPingPeriod = 25 * time.Second // 必须小于 wsPongWait
	wsSendBuffer = 64               // 出站缓冲：慢客户端超限即踢，避免阻塞广播
	wsReadLimit  = 4096             // 入站单帧上限（聊天消息远小于此）
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// 页面与 WS 同源部署；跨 Origin 的握手（CSRF 式连接）直接拒绝。
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return true // 非浏览器客户端（测试 / 脚本）没有 Origin
		}
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	},
}

// ChatHub 维护当前在线的聊天连接；C 端与 B 端两个 Server 实例共享同一张表，
// 买家与客服才能互相实时推送（两端口同进程，零跨进程通信成本）。
type ChatHub struct {
	mu      sync.Mutex
	clients map[*wsClient]struct{}
}

// NewChatHub 创建共享在线表；由 app.Build 分发给两端 Server 实例。
func NewChatHub() *ChatHub { return &ChatHub{clients: map[*wsClient]struct{}{}} }

func (h *ChatHub) register(c *wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *ChatHub) unregister(c *wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// broadcastChat 新消息广播：订阅了该订单的连接收到完整消息（发送者除外，
// 它已通过 sent 回执拿到同一消息，避免弹窗里出现两条）；客服 / 管理员以及
// 订单归属买家再收到 threads_dirty，用于刷新会话列表与未读徽标。
func (h *ChatHub) broadcastChat(orderID string, m *model.ChatMsg, from *wsClient) {
	chatBody, _ := json.Marshal(wsEvent{Type: "chat", OrderID: orderID, Msg: m})
	dirtyBody, _ := json.Marshal(wsEvent{Type: "threads_dirty", OrderID: orderID})
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c == from {
			continue
		}
		subbed := c.subscribed(orderID)
		isStaff := c.actor.Role == model.RoleCS || c.actor.Role == model.RoleAdmin
		if subbed {
			c.deliver(chatBody)
		}
		if isStaff || (c.actor.Role == model.RoleBuyer && subbed) {
			c.deliver(dirtyBody)
		}
	}
}

// wsClient 一条 WebSocket 连接。subs 由读循环独占写入、广播读取，需要锁。
type wsClient struct {
	conn      *websocket.Conn
	actor     model.Actor
	hub       *ChatHub
	send      chan []byte
	subsMu    sync.Mutex
	subs      map[string]struct{}
	closeOnce sync.Once
}

func newWSClient(conn *websocket.Conn, actor model.Actor, hub *ChatHub) *wsClient {
	return &wsClient{
		conn: conn, actor: actor, hub: hub,
		send: make(chan []byte, wsSendBuffer),
		subs: map[string]struct{}{},
	}
}

func (c *wsClient) subscribed(orderID string) bool {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	_, ok := c.subs[orderID]
	return ok
}

func (c *wsClient) addSub(orderID string) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	c.subs[orderID] = struct{}{}
}

func (c *wsClient) removeSub(orderID string) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	delete(c.subs, orderID)
}

// deliver 非阻塞投递；慢消费者（缓冲满）直接断开连接，
// 由读循环的统一清理路径回收资源，绝不 close(send) 以免与并发投递竞争。
func (c *wsClient) deliver(body []byte) {
	select {
	case c.send <- body:
	default:
		go c.shutdown()
	}
}

// shutdown 主动断开：只关 TCP 连接，send 通道由 cleanup 关闭。
func (c *wsClient) shutdown() {
	c.closeOnce.Do(func() { _ = c.conn.Close() })
}

// serveChatWS 处理握手、鉴权与连接生命周期；读循环阻塞到连接断开。
func (s *Server) serveChatWS(w http.ResponseWriter, r *http.Request) {
	actor := s.actorFromQuery(r)
	if actor.UserID == "" || actor.Role == "" {
		writeErr(w, &service.BizError{Code: service.CodeForbidden, Message: "聊天通道需要有效登录态"})
		return
	}
	// 与 HTTP 接口同一条跨端规则：买家只在 C 端连，客服 / 管理员只在 B 端连
	if s.side != "" && model.SideOf(actor.Role) != s.side {
		writeErr(w, &service.BizError{Code: service.CodeForbidden,
			Message: model.SideLabel(model.SideOf(actor.Role)) + "账号不能连接 " + model.SideLabel(s.side) + " 页面的聊天通道"})
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade 内部已写好 4xx 响应
	}
	client := newWSClient(conn, actor, s.hub)
	s.hub.register(client)
	go client.writePump()
	client.readPump(s) // 阻塞直至连接断开
	// 统一清理：先摘除（此后广播不再向它投递），再关 send 让写循环退出。
	// 广播的 deliver 全部发生在持有 hub.mu 期间，与 unregister 互斥，
	// 因此 close(send) 不会与并发投递竞争。
	s.hub.unregister(client)
	close(client.send)
	client.shutdown()
}

// actorFromQuery 与 actorFrom 同一条认证链路，只是凭据从 URL query 取
// （浏览器 WebSocket API 无法自定义请求头）。
func (s *Server) actorFromQuery(r *http.Request) model.Actor {
	token := r.URL.Query().Get("token")
	if token != "" {
		if role, uid, ok := s.cache.LoadSession(token); ok {
			if acc, valid := s.accounts.Find(uid); valid && string(acc.Role) == role {
				return model.Actor{Role: acc.Role, UserID: acc.ID, Name: acc.Name}
			}
		}
		if s.production {
			return model.Actor{} // 生产模式：无效令牌直接拒绝，绝不回退
		}
	}
	if s.production {
		return model.Actor{}
	}
	// 演示模式：与 HTTP 头部身份等价的 query 回退（仅限测试与验收环境）。
	// 长连接在握手时一次性定型，不做 HTTP 那样的「非法身份交给 service 守卫」——
	// 账号表里查不到就直接拒绝，避免幽灵连接占住在线表。
	role := model.Role(r.URL.Query().Get("role"))
	id := r.URL.Query().Get("uid")
	if id != "" && !strings.ContainsAny(id, "/\\") {
		if acc, ok := s.accounts.Find(id); ok && acc.Role == role {
			return model.Actor{Role: acc.Role, UserID: acc.ID, Name: acc.Name}
		}
	}
	return model.Actor{}
}

// wsEvent 服务端出站事件。
type wsEvent struct {
	Type    string         `json:"type"`
	OrderID string         `json:"orderId,omitempty"`
	Ref     int64          `json:"ref,omitempty"`
	Msg     *model.ChatMsg `json:"msg,omitempty"`
	Message string         `json:"message,omitempty"`
}

func (c *wsClient) emit(e wsEvent) {
	if b, err := json.Marshal(e); err == nil {
		c.deliver(b)
	}
}

// readPump 读循环：处理订阅 / 发送 / 探活，断开时返回。
func (c *wsClient) readPump(s *Server) {
	defer c.shutdown()
	c.conn.SetReadLimit(wsReadLimit)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var in struct {
			Type    string `json:"type"`
			OrderID string `json:"orderId"`
			Kind    string `json:"kind"`
			Text    string `json:"text"`
			Ref     int64  `json:"ref"`
		}
		if json.Unmarshal(data, &in) != nil || in.Type == "" {
			c.emit(wsEvent{Type: "error", Message: "消息格式不正确"})
			continue
		}
		switch in.Type {
		case "ping":
			c.emit(wsEvent{Type: "pong"})
		case "sub":
			if err := s.svc.CheckChatAccess(c.actor, in.OrderID); err != nil {
				c.emit(wsEvent{Type: "error", OrderID: in.OrderID, Message: err.Error()})
				continue
			}
			c.addSub(in.OrderID)
			c.emit(wsEvent{Type: "sub_ok", OrderID: in.OrderID})
		case "unsub":
			c.removeSub(in.OrderID)
		case "send":
			m, err := s.svc.SendChat(c.actor, in.OrderID, in.Kind, in.Text)
			if err != nil {
				c.emit(wsEvent{Type: "send_error", Ref: in.Ref, Message: err.Error()})
				continue
			}
			c.emit(wsEvent{Type: "sent", Ref: in.Ref, Msg: m, OrderID: in.OrderID})
			c.hub.broadcastChat(in.OrderID, m, c)
		default:
			c.emit(wsEvent{Type: "error", Message: "不支持的消息类型 " + in.Type})
		}
	}
}

// writePump 写循环：消费 send 队列并定期 ping 保活。
func (c *wsClient) writePump() {
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		c.shutdown()
	}()
	for {
		select {
		case body, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, body); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
