// Package api 提供 REST/JSON 接口与静态页面托管。
// 身份通过 X-Role / X-User-Id 头传递，后端按预置账号表还原身份，名字不可由前端伪造。
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"small-commerce-order/internal/cache"
	"small-commerce-order/internal/model"
	"small-commerce-order/internal/service"
)

type Server struct {
	svc        *service.Service
	web        string
	cache      cache.Cache
	production bool              // 生产模式：强制会话令牌 + 密码登录，禁用演示接口
	passwords  map[string]string // 用户 ID → 密码（仅生产模式使用，由环境变量注入）
}

func New(svc *service.Service, webDir string, c cache.Cache) *Server {
	if c == nil {
		c = cache.Noop{}
	}
	return &Server{svc: svc, web: webDir, cache: c, passwords: map[string]string{}}
}

// WithSecurity 启用生产模式。demo 模式（默认）保持「选角色即登录」以便验收与演示。
func (s *Server) WithSecurity(passwords map[string]string) *Server {
	s.production = true
	if passwords != nil {
		s.passwords = passwords
	}
	return s
}

// Env 供 /api/meta 与验收脚本识别运行环境。
func (s *Server) Env() string {
	if s.production {
		return "production"
	}
	return "demo"
}

type envelope struct {
	OK    bool              `json:"ok"`
	Data  any               `json:"data,omitempty"`
	Error *service.BizError `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var be *service.BizError
	if errors.As(err, &be) {
		status := http.StatusBadRequest
		switch be.Code {
		case service.CodeForbidden:
			status = http.StatusForbidden
		case service.CodeNotFound:
			status = http.StatusNotFound
		case service.CodeConflict:
			status = http.StatusConflict
		case "INTERNAL":
			status = http.StatusInternalServerError
		}
		writeJSON(w, status, envelope{OK: false, Error: be})
		return
	}
	writeJSON(w, http.StatusInternalServerError, envelope{OK: false,
		Error: &service.BizError{Code: "INTERNAL", Message: err.Error()}})
}

// h 将业务方法包装成统一响应。
func (s *Server) h(fn func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fn(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, envelope{OK: true, Data: data})
	}
}

// actorFrom 优先用 sess:{token} 登录态还原身份（Redis），失败回退到请求头；
// 名字始终由服务端账号表给出，前端无法伪造。
func (s *Server) actorFrom(r *http.Request) model.Actor {
	if token := r.Header.Get("X-Token"); token != "" {
		if role, uid, ok := s.cache.LoadSession(token); ok {
			if u, valid := model.FindUser(uid, model.Role(role)); valid {
				return model.Actor{Role: u.Role, UserID: u.ID, Name: u.Name}
			}
		}
		if s.production {
			return model.Actor{} // 生产模式：无效令牌直接拒绝，绝不回退
		}
	}
	if s.production {
		return model.Actor{} // 生产模式：必须携带有效 X-Token
	}
	role := model.Role(r.Header.Get("X-Role"))
	id := r.Header.Get("X-User-Id")
	if u, ok := model.FindUser(id, role); ok {
		return model.Actor{Role: role, UserID: u.ID, Name: u.Name}
	}
	// 非法身份保留字段，由 service 的权限守卫统一返回 FORBIDDEN
	return model.Actor{Role: role, UserID: id}
}

func decodeBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return &service.BizError{Code: service.CodeBadRequest, Message: "请求体不是合法 JSON"}
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/meta", s.h(func(r *http.Request) (any, error) {
		users := []model.User{}
		for _, u := range model.Users {
			if u.Role != model.RoleChannel {
				users = append(users, u)
			}
		}
		labels := map[string]string{}
		for k, v := range model.StatusLabels {
			labels[string(k)] = v
		}
		return map[string]any{"users": users, "statusLabels": labels, "env": s.Env()}, nil
	}))

	// 登录态：Redis sess:{token} 存角色与用户标识；Redis 不可用时降级为请求头身份。
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Role     string `json:"role"`
			UserID   string `json:"userId"`
			Password string `json:"password"`
		}
		if err := decodeBody(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		role := model.Role(req.Role)
		u, ok := model.FindUser(req.UserID, role)
		if !ok || role == model.RoleChannel {
			writeErr(w, &service.BizError{Code: service.CodeForbidden, Message: "账号不存在或角色不匹配"})
			return
		}
		if s.production {
			want, configured := s.passwords[u.ID]
			if !configured || subtle.ConstantTimeCompare([]byte(req.Password), []byte(want)) != 1 {
				writeErr(w, &service.BizError{Code: service.CodeForbidden, Message: "账号或密码错误"})
				return
			}
		}
		token := cache.NewToken()
		enabled := true
		if err := s.cache.SaveSession(token, string(role), u.ID); err != nil {
			if s.production {
				// 生产模式身份完全依赖 Redis 会话，不可用时必须拒绝登录
				writeErr(w, &service.BizError{Code: "INTERNAL", Message: "生产模式要求 Redis 会话可用"})
				return
			}
			enabled = false
		}
		writeJSON(w, http.StatusOK, envelope{OK: true, Data: map[string]any{
			"token": token, "sessionEnabled": enabled, "user": u,
		}})
	})

	// 演示接口只在 demo 模式注册；生产模式下这两个路径不存在。
	if !s.production {
		mux.HandleFunc("POST /api/reset", s.h(func(r *http.Request) (any, error) {
			return map[string]any{"reset": true}, s.svc.Reset(s.actorFrom(r))
		}))
	}

	// ---------- 商品 ----------
	mux.HandleFunc("GET /api/products", s.h(func(r *http.Request) (any, error) {
		return s.svc.Products(s.actorFrom(r))
	}))
	mux.HandleFunc("POST /api/products/{id}/onsale", s.h(func(r *http.Request) (any, error) {
		var req struct {
			OnSale bool `json:"onSale"`
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		return map[string]any{"onSale": req.OnSale}, s.svc.SetOnSale(s.actorFrom(r), r.PathValue("id"), req.OnSale)
	}))

	// ---------- 订单 ----------
	mux.HandleFunc("GET /api/orders", s.h(func(r *http.Request) (any, error) {
		return s.svc.Orders(s.actorFrom(r))
	}))
	mux.HandleFunc("POST /api/orders", s.h(func(r *http.Request) (any, error) {
		var req struct {
			ProductID string `json:"productId"`
			Qty       int    `json:"qty"`
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		return s.svc.CreateOrder(s.actorFrom(r), strings.TrimSpace(req.ProductID), req.Qty)
	}))
	mux.HandleFunc("GET /api/orders/{id}", s.h(func(r *http.Request) (any, error) {
		return s.svc.GetOrder(s.actorFrom(r), r.PathValue("id"))
	}))
	mux.HandleFunc("POST /api/orders/{id}/cancel", s.h(func(r *http.Request) (any, error) {
		return map[string]any{"cancelled": true}, s.svc.CancelOrder(s.actorFrom(r), r.PathValue("id"))
	}))
	// 演示辅助：把订单到期时间提前并触发服务端超时扫描（生产模式不注册）
	if !s.production {
		mux.HandleFunc("POST /api/orders/{id}/expire", s.h(func(r *http.Request) (any, error) {
			a := s.actorFrom(r)
			if err := s.svc.ForceExpire(a, r.PathValue("id")); err != nil {
				return nil, err
			}
			n, err := s.svc.CloseExpiredOrders()
			if err != nil {
				return nil, err
			}
			return map[string]any{"closed": n}, nil
		}))
	}
	mux.HandleFunc("POST /api/orders/{id}/pay-intent", s.h(func(r *http.Request) (any, error) {
		return s.svc.CreatePaymentIntent(s.actorFrom(r), r.PathValue("id"))
	}))

	// ---------- 支付渠道通知（鉴权靠验签，身份由 API 构造为模拟渠道） ----------
	mux.HandleFunc("POST /api/payments/notify", func(w http.ResponseWriter, r *http.Request) {
		var in model.PaymentIntent
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		res, err := s.svc.HandlePaymentNotify(in)
		if err != nil {
			writeErr(w, err)
			return
		}
		status := http.StatusOK
		ok := true
		switch res.Outcome {
		case "BAD_SIGNATURE", "AMOUNT_MISMATCH", "UNKNOWN_ORDER", "TXN_CONFLICT":
			status, ok = http.StatusBadRequest, false
		}
		body := envelope{OK: ok, Data: res}
		if !ok {
			body.Error = &service.BizError{Code: service.CodeBadRequest, Message: res.Outcome + "｜" + res.Detail}
		}
		writeJSON(w, status, body)
	})

	// ---------- 退款 / 异常支付 ----------
	mux.HandleFunc("GET /api/refunds", s.h(func(r *http.Request) (any, error) {
		return s.svc.Refunds(s.actorFrom(r))
	}))
	mux.HandleFunc("POST /api/orders/{id}/refund-apply", s.h(func(r *http.Request) (any, error) {
		var req struct {
			Reason string `json:"reason"`
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		if req.Reason == "" {
			req.Reason = "买家申请整单退款"
		}
		return s.svc.ApplyRefund(s.actorFrom(r), r.PathValue("id"), req.Reason)
	}))
	mux.HandleFunc("POST /api/refunds/{id}/review", s.h(func(r *http.Request) (any, error) {
		var req struct {
			Approve bool   `json:"approve"`
			Note    string `json:"note"`
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		return s.svc.ReviewRefund(s.actorFrom(r), r.PathValue("id"), req.Approve, req.Note)
	}))
	mux.HandleFunc("POST /api/refunds/{id}/retry", s.h(func(r *http.Request) (any, error) {
		return s.svc.RetryRefund(s.actorFrom(r), r.PathValue("id"))
	}))
	mux.HandleFunc("POST /api/refunds/{id}/channel-notify", s.h(func(r *http.Request) (any, error) {
		var req struct {
			Success         bool   `json:"success"`
			ChannelRefundID string `json:"channelRefundId"`
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		if req.ChannelRefundID == "" {
			req.ChannelRefundID = "CR-" + r.PathValue("id")
		}
		// 模拟渠道结果由客服/运营在演示中触发；必须用真实请求身份，
		// 否则买家可自助确认退款成功并恢复库存（越权）。
		return s.svc.HandleRefundChannelNotify(s.actorFrom(r), r.PathValue("id"), req.Success, req.ChannelRefundID)
	}))
	mux.HandleFunc("GET /api/abnormal", s.h(func(r *http.Request) (any, error) {
		return s.svc.AbnormalPayments(s.actorFrom(r))
	}))
	mux.HandleFunc("POST /api/abnormal/{id}/handle", s.h(func(r *http.Request) (any, error) {
		var req struct {
			Note string `json:"note"`
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		if req.Note == "" {
			req.Note = "客服核实为迟到扣款，原路退回"
		}
		return s.svc.HandleAbnormalPayment(s.actorFrom(r), r.PathValue("id"), req.Note)
	}))

	// ---------- 发货 ----------
	mux.HandleFunc("POST /api/orders/{id}/ship", s.h(func(r *http.Request) (any, error) {
		var req struct {
			Carrier    string `json:"carrier"`
			TrackingNo string `json:"trackingNo"`
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		return s.svc.Ship(s.actorFrom(r), r.PathValue("id"), req.Carrier, req.TrackingNo)
	}))
	mux.HandleFunc("GET /api/shipments", s.h(func(r *http.Request) (any, error) {
		return s.svc.Shipments(s.actorFrom(r))
	}))

	// ---------- 运营核查 ----------
	mux.HandleFunc("GET /api/summary", s.h(func(r *http.Request) (any, error) {
		return s.svc.Summary(s.actorFrom(r))
	}))
	mux.HandleFunc("GET /api/ledger", s.h(func(r *http.Request) (any, error) {
		return s.svc.Ledger(s.actorFrom(r))
	}))
	mux.HandleFunc("GET /api/notify-results", s.h(func(r *http.Request) (any, error) {
		return s.svc.NotifyResults(s.actorFrom(r))
	}))
	mux.HandleFunc("GET /api/state", s.h(func(r *http.Request) (any, error) {
		return s.svc.State(s.actorFrom(r))
	}))

	// ---------- 静态前端 ----------
	fs := http.FileServer(http.Dir(s.web))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusNotFound, envelope{OK: false,
				Error: &service.BizError{Code: service.CodeNotFound, Message: "接口不存在"}})
			return
		}
		fs.ServeHTTP(w, r)
	}))
	return mux
}
