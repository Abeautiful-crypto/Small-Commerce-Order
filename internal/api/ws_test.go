package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"small-commerce-order/internal/model"
)

// wsTestServer 把 harness 的 Handler 挂到真实 TCP 端口上，供 WebSocket 拨号。
func wsTestServer(t *testing.T, h *apiHarness) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(h.handler)
	t.Cleanup(ts.Close)
	return ts
}

func wsURL(ts *httptest.Server, query string) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/chat/ws?" + query
}

// wsDial 以演示身份拨号并确认连接；握手失败直接 Fatal。
func wsDial(t *testing.T, ts *httptest.Server, query string) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(wsURL(ts, query), nil)
	if err != nil {
		t.Fatalf("WebSocket 握手失败（%s）：%v", query, err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

// wsRead 带超时读一条服务端事件。
func wsRead(t *testing.T, ws *websocket.Conn) map[string]any {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("读 WebSocket 消息失败：%v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("服务端消息不是合法 JSON：%v", err)
	}
	return out
}

// wsWaitType 等待指定类型的事件（跳过无关事件，例如自己的 threads_dirty）。
func wsWaitType(t *testing.T, ws *websocket.Conn, typ string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("等待 %s 事件失败：%v", typ, err)
		}
		var ev map[string]any
		if json.Unmarshal(data, &ev) != nil {
			continue
		}
		if ev["type"] == typ {
			return ev
		}
	}
	t.Fatalf("3 秒内未收到 %s 事件", typ)
	return nil
}

func wsWrite(t *testing.T, ws *websocket.Conn, v any) {
	t.Helper()
	_ = ws.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := ws.WriteJSON(v); err != nil {
		t.Fatalf("发送 WebSocket 消息失败：%v", err)
	}
}

const orderIDSeed = "NO20260001" // 预置订单，买家 u001

func TestWS_ChatRealtime(t *testing.T) {
	h := newAPIHarness(t)
	ts := wsTestServer(t, h)

	buyer := wsDial(t, ts, "role=buyer&uid=u001")
	cs := wsDial(t, ts, "role=cs&uid=cs")

	// 双方订阅同一订单会话
	wsWrite(t, buyer, map[string]any{"type": "sub", "orderId": orderIDSeed})
	if ev := wsWaitType(t, buyer, "sub_ok"); ev["orderId"] != orderIDSeed {
		t.Fatalf("买家订阅回执订单号不符：%v", ev)
	}
	wsWrite(t, cs, map[string]any{"type": "sub", "orderId": orderIDSeed})
	wsWaitType(t, cs, "sub_ok")

	// 买家经 WS 发消息：自己收到 sent 回执，客服实时收到 chat + threads_dirty
	wsWrite(t, buyer, map[string]any{"type": "send", "orderId": orderIDSeed, "kind": "text", "text": "你好，想问下发货时间", "ref": 1})
	sent := wsWaitType(t, buyer, "sent")
	msg, _ := sent["msg"].(map[string]any)
	if msg == nil || msg["text"] != "你好，想问下发货时间" || msg["fromId"] != "u001" {
		t.Fatalf("sent 回执内容不符：%v", sent)
	}
	chat := wsWaitType(t, cs, "chat")
	cmsg, _ := chat["msg"].(map[string]any)
	if cmsg == nil || cmsg["text"] != "你好，想问下发货时间" {
		t.Fatalf("客服未实时收到买家消息：%v", chat)
	}
	dirty := wsWaitType(t, cs, "threads_dirty")
	if dirty["orderId"] != orderIDSeed {
		t.Fatalf("threads_dirty 订单号不符：%v", dirty)
	}

	// 客服经 WS 回复：买家实时收到
	wsWrite(t, cs, map[string]any{"type": "send", "orderId": orderIDSeed, "kind": "text", "text": "今天下午发出", "ref": 1})
	reply := wsWaitType(t, buyer, "chat")
	rmsg, _ := reply["msg"].(map[string]any)
	if rmsg == nil || rmsg["text"] != "今天下午发出" || rmsg["fromRole"] != string(model.RoleCS) {
		t.Fatalf("买家未实时收到客服回复：%v", reply)
	}

	// HTTP 兜底路径发送的消息同样广播给 WS 在线者
	code, env := h.request(t, http.MethodPost, "/api/chat/"+orderIDSeed,
		map[string]any{"kind": "text", "text": "HTTP 兜底消息"}, buyerA, "")
	if code != http.StatusOK {
		t.Fatalf("HTTP 发消息失败：%d %+v", code, env)
	}
	fallback := wsWaitType(t, cs, "chat")
	fmsg, _ := fallback["msg"].(map[string]any)
	if fmsg == nil || fmsg["text"] != "HTTP 兜底消息" {
		t.Fatalf("WS 订阅者未收到 HTTP 路径消息：%v", fallback)
	}

	// 消息确实入库：HTTP 拉取能读到 WS 与 HTTP 两路消息
	code, env = h.request(t, http.MethodGet, "/api/chat/"+orderIDSeed, nil, buyerA, "")
	if code != http.StatusOK {
		t.Fatalf("拉取消息失败：%d", code)
	}
	list := asSlice(t, env.Data)
	if len(list) != 3 {
		t.Fatalf("应有 3 条消息（WS×2 + HTTP×1），实际 %d", len(list))
	}
}

func TestWS_AccessControl(t *testing.T) {
	h := newAPIHarness(t)
	ts := wsTestServer(t, h)

	// 非订单本人的买家订阅 → 服务端拒绝并回 error
	buyerB := wsDial(t, ts, "role=buyer&uid=u002")
	wsWrite(t, buyerB, map[string]any{"type": "sub", "orderId": orderIDSeed})
	ev := wsWaitType(t, buyerB, "error")
	if ev["orderId"] != orderIDSeed {
		t.Fatalf("error 事件缺少订单号：%v", ev)
	}
	// 被拒绝后发消息仍走服务端权限校验，不会被广播给他人
	wsWrite(t, buyerB, map[string]any{"type": "send", "orderId": orderIDSeed, "kind": "text", "text": "越权", "ref": 1})
	if ev := wsWaitType(t, buyerB, "send_error"); ev["ref"] != float64(1) {
		t.Fatalf("越权发送应回 send_error：%v", ev)
	}

	// 仓库角色无权进入聊天会话
	wh := wsDial(t, ts, "role=warehouse&uid=wh")
	wsWrite(t, wh, map[string]any{"type": "sub", "orderId": orderIDSeed})
	if ev := wsWaitType(t, wh, "error"); ev["message"] == "" {
		t.Fatalf("仓库订阅应被拒绝：%v", ev)
	}

	// 未知身份握手直接 403
	resp, err := http.Get(ts.URL + "/api/chat/ws?role=buyer&uid=nobody")
	if err == nil {
		defer resp.Body.Close()
	}
	if err == nil && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("未知 uid 握手应 403，实际 %d", resp.StatusCode)
	}
}

func TestWS_TokenAuth(t *testing.T) {
	h := newAPIHarness(t)
	// 有效会话令牌：与 HTTP 接口同一链路（sess → 账号表）
	if err := h.cache.SaveSession("tok-ws", "buyer", "u001"); err != nil {
		t.Fatal(err)
	}
	ts := wsTestServer(t, h)
	buyer := wsDial(t, ts, "token=tok-ws")
	wsWrite(t, buyer, map[string]any{"type": "sub", "orderId": orderIDSeed})
	wsWaitType(t, buyer, "sub_ok")

	// 无效令牌在演示模式下回退到 role/uid：都没有则 403
	resp, err := http.Get(ts.URL + "/api/chat/ws?token=bad-token")
	if err == nil {
		defer resp.Body.Close()
	}
	if err == nil && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无效令牌握手应 403，实际 %d", resp.StatusCode)
	}
}

func TestWS_UnknownType(t *testing.T) {
	h := newAPIHarness(t)
	ts := wsTestServer(t, h)
	ws := wsDial(t, ts, "role=cs&uid=cs")
	wsWrite(t, ws, map[string]any{"type": "nonsense"})
	if ev := wsWaitType(t, ws, "error"); !strings.Contains(ev["message"].(string), "nonsense") {
		t.Fatalf("未知类型应回 error 说明：%v", ev)
	}
	// 应用层探活
	wsWrite(t, ws, map[string]any{"type": "ping"})
	if ev := wsWaitType(t, ws, "pong"); ev["type"] != "pong" {
		t.Fatalf("ping 应回 pong：%v", ev)
	}
}
