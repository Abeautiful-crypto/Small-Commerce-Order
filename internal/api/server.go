// Package api 提供 REST/JSON 接口与静态页面托管。
// 身份通过 X-Role / X-User-Id 头传递，后端按预置账号表还原身份，名字不可由前端伪造。
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"small-commerce-order/internal/account"
	"small-commerce-order/internal/cache"
	"small-commerce-order/internal/model"
	"small-commerce-order/internal/service"
)

type Server struct {
	svc          *service.Service
	web          string
	cache        cache.Cache
	accounts     account.Repo      // 登录账号真相源：users（买家）/ staff（管理人员）
	production   bool              // 生产模式：强制会话令牌 + 密码登录，禁用演示接口
	passwordless bool              // 生产模式下免密码登录（仅演示/预览，仍签发会话令牌）
	passwords    map[string]string // 手机号 → 密码（生产模式可选覆盖，由环境变量注入）
	side         string            // 本实例托管的端：c=C端买家页 / b=B端工作台 / ""=不限制（测试与脚本）
}

func New(svc *service.Service, webDir string, c cache.Cache, accounts account.Repo) *Server {
	if c == nil {
		c = cache.Noop{}
	}
	if accounts == nil {
		accounts = account.NewMemRepo()
	}
	return &Server{svc: svc, web: webDir, cache: c, accounts: accounts, passwords: map[string]string{}}
}

// WithSecurity 启用生产模式。demo 模式（默认）保持「选角色即登录」以便验收与演示。
func (s *Server) WithSecurity(passwords map[string]string) *Server {
	s.production = true
	if passwords != nil {
		s.passwords = passwords
	}
	return s
}

// WithPasswordless 在生产模式下启用免密码登录：仍签发并强制 Redis 会话令牌、
// 保持角色隔离、演示接口依旧不注册，仅跳过密码校验。仅用于演示/预览环境。
func (s *Server) WithPasswordless() *Server {
	s.passwordless = true
	return s
}

// Env 供 /api/meta 与验收脚本识别运行环境。
func (s *Server) Env() string {
	if s.production {
		return "production"
	}
	return "demo"
}

// WithSide 把实例绑定到某个端：登录接口只发放该端角色，
// 首页返回该端页面，跨端令牌一律拒绝。
func (s *Server) WithSide(side string) *Server {
	s.side = side
	return s
}

// Side 返回实例所属端。
func (s *Server) Side() string { return s.side }

// entryFile 返回该端的首页文件。
func (s *Server) entryFile() string {
	if s.side == model.SideBusiness {
		return "business.html"
	}
	return "customer.html"
}

// authenticate 校验「手机号 + 密码」。账号表（users / staff）是唯一真相源：
//   - 生产模式：走账号表密文校验；若 APP_PASSWORDS 显式配置了该手机号，以配置值为准；
//   - 生产模式 + 免密码登录：只校验账号存在（仍签发并强制会话令牌）；
//   - demo 模式：允许空密码（等价于既有「选账号即登录」，验收脚本依赖此行为），
//     前端登录页始终要求输入密码，不走这条降级路径。
func (s *Server) authenticate(phone, password string) (account.Account, bool) {
	if s.production {
		if want, configured := s.passwords[phone]; configured {
			acc, ok := s.accounts.FindByPhone(phone)
			if !ok || subtle.ConstantTimeCompare([]byte(password), []byte(want)) != 1 {
				return account.Account{}, false
			}
			return acc, true
		}
		if s.passwordless {
			return s.accounts.FindByPhone(phone)
		}
		return s.accounts.Authenticate(phone, password)
	}
	if acc, ok := s.accounts.Authenticate(phone, password); ok {
		return acc, true
	}
	if password == "" {
		return s.accounts.FindByPhone(phone)
	}
	return account.Account{}, false
}

// checkSide 拦截「拿着 B 端令牌访问 C 端接口」这类跨端复用：
// 令牌角色由服务端会话给出，前端无法伪造。
func (s *Server) checkSide(r *http.Request) error {
	if s.side == "" {
		return nil
	}
	token := r.Header.Get("X-Token")
	if token == "" {
		return nil // 无令牌（演示头部身份）由角色权限守卫负责
	}
	role, _, ok := s.cache.LoadSession(token)
	if !ok {
		return nil
	}
	if model.SideOf(model.Role(role)) != s.side {
		return &service.BizError{Code: service.CodeForbidden,
			Message: model.SideLabel(model.SideOf(model.Role(role))) + "账号的会话不能用于 " + model.SideLabel(s.side) + " 页面"}
	}
	return nil
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
		if err := s.checkSide(r); err != nil {
			writeErr(w, err)
			return
		}
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
			// 令牌里的角色必须与账号表一致，账号被改角色后旧令牌立即失效
			if acc, valid := s.accounts.Find(uid); valid && string(acc.Role) == role {
				return model.Actor{Role: acc.Role, UserID: acc.ID, Name: acc.Name}
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
	if acc, ok := s.accounts.Find(id); ok && acc.Role == role {
		return model.Actor{Role: acc.Role, UserID: acc.ID, Name: acc.Name}
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
		labels := map[string]string{}
		for k, v := range model.StatusLabels {
			labels[string(k)] = v
		}
		// 不下发任何账号清单：登录必须由用户自己输入账号与密码
		return map[string]any{
			"statusLabels": labels,
			"env":          s.Env(), "loginPasswordless": s.passwordless,
			"side": s.side, "sideLabel": model.SideLabel(s.side),
		}, nil
	}))

	// 登录：只接受「11 位手机号 + 密码」。角色由服务端账号表（users / staff）给出，
	// 请求体里的 role 字段一律忽略，前端无法自报成别的角色。
	// 登录态存 Redis sess:{token}；Redis 不可用且非生产模式时降级为请求头身份。
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Phone    string `json:"phone"`
			UserID   string `json:"userId"` // 兼容旧字段名，同样按手机号解释
			Password string `json:"password"`
		}
		if err := decodeBody(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		phone := strings.TrimSpace(req.Phone)
		if phone == "" {
			phone = strings.TrimSpace(req.UserID)
		}
		if phone == "" {
			writeErr(w, &service.BizError{Code: service.CodeBadRequest, Message: "请输入手机号"})
			return
		}
		if !account.IsPhone(phone) {
			writeErr(w, &service.BizError{Code: service.CodeBadRequest, Message: "登录账号必须是 11 位手机号"})
			return
		}

		acc, ok := s.authenticate(phone, req.Password)
		if !ok {
			writeErr(w, &service.BizError{Code: service.CodeForbidden, Message: "账号或密码错误"})
			return
		}
		// 登录端硬校验：账号角色必须属于当前页面所在的端，
		// 因此在 C 端用仓库管理员账号登录会被直接拒绝，反之亦然。
		if s.side != "" && model.SideOf(acc.Role) != s.side {
			need := model.SideLabel(model.SideOf(acc.Role))
			writeErr(w, &service.BizError{Code: service.CodeForbidden,
				Message: model.SideLabel(s.side) + "页面不接受 " + need + " 账号（" + acc.Name + "），请到 " + need + " 页面登录"})
			return
		}
		token := cache.NewToken()
		enabled := true
		if err := s.cache.SaveSession(token, string(acc.Role), acc.ID); err != nil {
			if s.production {
				// 生产模式身份完全依赖 Redis 会话，不可用时必须拒绝登录
				writeErr(w, &service.BizError{Code: "INTERNAL", Message: "生产模式要求 Redis 会话可用"})
				return
			}
			enabled = false
		}
		writeJSON(w, http.StatusOK, envelope{OK: true, Data: map[string]any{
			"token": token, "sessionEnabled": enabled, "user": acc,
			"side": model.SideOf(acc.Role), "sideLabel": model.SideLabel(model.SideOf(acc.Role)),
		}})
	})

	// 注册：C 端买家自助注册（手机号即登录账号，密码 PBKDF2 密文入库）。
	// 只创建 buyer 角色账号，B 端账号必须由数据库预置，注册接口无法伪造运营/仓库/客服身份。
	mux.HandleFunc("POST /api/register", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Phone    string `json:"phone"`
			Password string `json:"password"`
			Name     string `json:"name"`
		}
		if err := decodeBody(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		phone := strings.TrimSpace(req.Phone)
		if err := account.ValidateRegistration(phone, req.Password); err != nil {
			writeErr(w, &service.BizError{Code: service.CodeBadRequest, Message: err.Error()})
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = "用户" + phone[len(phone)-4:]
		}
		if rs := []rune(name); len(rs) > 20 {
			name = string(rs[:20])
		}
		acc, err := s.accounts.Register(name, phone, req.Password)
		if err != nil {
			code := service.CodeBadRequest
			if err == account.ErrPhoneTaken {
				code = service.CodeConflict
			}
			writeErr(w, &service.BizError{Code: code, Message: err.Error()})
			return
		}
		// 新账号立即进入身份目录，否则权限守卫会把它当成非法身份
		model.AddUser(model.User{ID: acc.ID, Name: acc.Name, Role: acc.Role})
		writeJSON(w, http.StatusOK, envelope{OK: true, Data: map[string]any{"user": acc}})
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
	mux.HandleFunc("POST /api/products", s.h(func(r *http.Request) (any, error) {
		var req struct {
			Name     string `json:"name"`
			Image    string `json:"image"`
			Category string `json:"category"`
			PriceFen int64  `json:"priceFen"`
			Stock    int64  `json:"stock"`
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		return s.svc.CreateProduct(s.actorFrom(r), req.Name, req.Image, req.Category, req.PriceFen, req.Stock)
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

	// 商品图片上传：运营从本机选图后直传服务端，落盘到 web/uploads 并返回站内 URL。
	// 仅接受 jpg/png/webp/gif，单文件上限 5MB；文件名由服务端随机生成，不使用客户端文件名。
	mux.HandleFunc("POST /api/uploads", func(w http.ResponseWriter, r *http.Request) {
		actor := s.actorFrom(r)
		if actor.Role != model.RoleOps {
			writeErr(w, &service.BizError{Code: service.CodeForbidden, Message: "无权上传：仅运营可维护商品图片"})
			return
		}
		const maxSize = 5 << 20
		r.Body = http.MaxBytesReader(w, r.Body, maxSize+(1<<20))
		if err := r.ParseMultipartForm(maxSize); err != nil {
			writeErr(w, &service.BizError{Code: service.CodeBadRequest, Message: "图片过大或表单格式不正确（上限 5MB）"})
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeErr(w, &service.BizError{Code: service.CodeBadRequest, Message: "请选择要上传的图片文件"})
			return
		}
		defer file.Close()
		head := make([]byte, 512)
		n, _ := io.ReadFull(file, head)
		head = head[:n]
		ext, ok := imageExt(http.DetectContentType(head))
		if !ok {
			writeErr(w, &service.BizError{Code: service.CodeBadRequest, Message: "只支持 JPG / PNG / WEBP / GIF 图片"})
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			writeErr(w, &service.BizError{Code: service.CodeBadRequest, Message: "读取图片失败"})
			return
		}
		dir := filepath.Join(s.web, "uploads")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			writeErr(w, &service.BizError{Code: "INTERNAL", Message: "创建上传目录失败"})
			return
		}
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			writeErr(w, &service.BizError{Code: "INTERNAL", Message: "生成文件名失败"})
			return
		}
		name := hex.EncodeToString(buf) + ext
		dst, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			writeErr(w, &service.BizError{Code: "INTERNAL", Message: "保存图片失败"})
			return
		}
		written, copyErr := io.Copy(dst, io.LimitReader(file, maxSize))
		closeErr := dst.Close()
		if copyErr != nil || closeErr != nil || written == 0 {
			_ = os.Remove(filepath.Join(dir, name))
			writeErr(w, &service.BizError{Code: "INTERNAL", Message: "保存图片失败"})
			return
		}
		writeJSON(w, http.StatusOK, envelope{OK: true, Data: map[string]any{
			"url": "uploads/" + name, "size": written,
		}})
	})

	// 商品操作记录：新增商品 / 上架 / 下架（与资金流水分开，只有运营可见）
	mux.HandleFunc("GET /api/product-ops", s.h(func(r *http.Request) (any, error) {
		return s.svc.ProductOps(s.actorFrom(r))
	}))

	// 商品评论：登录用户可看；发评论 / 回复由服务层校验「本人订单 + 已签收」
	mux.HandleFunc("GET /api/products/{id}/reviews", s.h(func(r *http.Request) (any, error) {
		return s.svc.ReviewsOf(s.actorFrom(r), r.PathValue("id"))
	}))
	mux.HandleFunc("POST /api/products/{id}/reviews", s.h(func(r *http.Request) (any, error) {
		var req struct {
			OrderID  string `json:"orderId"`
			ParentID string `json:"parentId"`
			Content  string `json:"content"`
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		return s.svc.PostReview(s.actorFrom(r), r.PathValue("id"), req.OrderID, req.ParentID, req.Content)
	}))

	// ---------- 订单客服会话 ----------
	mux.HandleFunc("GET /api/chat/threads", s.h(func(r *http.Request) (any, error) {
		return s.svc.ChatThreads(s.actorFrom(r))
	}))
	mux.HandleFunc("GET /api/chat/{orderId}", s.h(func(r *http.Request) (any, error) {
		return s.svc.ChatMessages(s.actorFrom(r), r.PathValue("orderId"))
	}))
	mux.HandleFunc("POST /api/chat/{orderId}", s.h(func(r *http.Request) (any, error) {
		var req struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		return s.svc.SendChat(s.actorFrom(r), r.PathValue("orderId"), req.Kind, req.Text)
	}))

	// ---------- 订单 ----------
	mux.HandleFunc("GET /api/orders", s.h(func(r *http.Request) (any, error) {
		return s.svc.Orders(s.actorFrom(r))
	}))
	mux.HandleFunc("POST /api/orders", s.h(func(r *http.Request) (any, error) {
		var req struct {
			ProductID string `json:"productId"`
			Qty       int    `json:"qty"`
			Channel   string `json:"channel"`   // wechat / alipay
			AddressID string `json:"addressId"` // 本人收货地址
		}
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		return s.svc.CreateOrder(s.actorFrom(r), strings.TrimSpace(req.ProductID), req.Qty,
			strings.TrimSpace(req.Channel), strings.TrimSpace(req.AddressID))
	}))

	// ---------- 收货地址（买家本人） ----------
	mux.HandleFunc("GET /api/addresses", s.h(func(r *http.Request) (any, error) {
		return s.svc.Addresses(s.actorFrom(r))
	}))
	mux.HandleFunc("POST /api/addresses", s.h(func(r *http.Request) (any, error) {
		var req service.AddressInput
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		return s.svc.AddAddress(s.actorFrom(r), req)
	}))
	mux.HandleFunc("POST /api/addresses/{id}/default", s.h(func(r *http.Request) (any, error) {
		return nil, s.svc.SetDefaultAddress(s.actorFrom(r), r.PathValue("id"))
	}))
	mux.HandleFunc("PUT /api/addresses/{id}", s.h(func(r *http.Request) (any, error) {
		var req service.AddressInput
		if err := decodeBody(r, &req); err != nil {
			return nil, err
		}
		return s.svc.UpdateAddress(s.actorFrom(r), r.PathValue("id"), req)
	}))
	mux.HandleFunc("DELETE /api/addresses/{id}", s.h(func(r *http.Request) (any, error) {
		return nil, s.svc.DeleteAddress(s.actorFrom(r), r.PathValue("id"))
	}))
	mux.HandleFunc("GET /api/orders/{id}", s.h(func(r *http.Request) (any, error) {
		return s.svc.GetOrder(s.actorFrom(r), r.PathValue("id"))
	}))
	mux.HandleFunc("POST /api/orders/{id}/cancel", s.h(func(r *http.Request) (any, error) {
		return map[string]any{"cancelled": true}, s.svc.CancelOrder(s.actorFrom(r), r.PathValue("id"))
	}))
	// 买家确认收货：已发货 → 已签收（签收时间回写发货记录）
	mux.HandleFunc("POST /api/orders/{id}/sign", s.h(func(r *http.Request) (any, error) {
		return s.svc.Sign(s.actorFrom(r), r.PathValue("id"))
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
		// 强制浏览器回源校验：避免前端改动后被旧 app.js / style.css 缓存掩盖
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		// 两个端各自有独立入口页：C 端 buyer 页、B 端工作台页
		if s.side != "" && (r.URL.Path == "/" || r.URL.Path == "/index.html") {
			clone := r.Clone(r.Context())
			cp := *r.URL
			cp.Path = "/" + s.entryFile()
			clone.URL = &cp
			fs.ServeHTTP(w, clone)
			return
		}
		fs.ServeHTTP(w, r)
	}))
	return mux
}
