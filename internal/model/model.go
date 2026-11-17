// Package model 定义订单交易系统的领域模型。
// 金额全程使用 int64「分」，禁止浮点；所有集合均为只增流水或带唯一键的业务记录。
package model

import "time"

// ---------------- 角色 ----------------

type Role string

const (
	RoleBuyer     Role = "buyer"     // 买家：仅能操作自己的订单
	RoleOps       Role = "ops"       // 运营：商品上下架、对账核查，不能改订单状态
	RoleWarehouse Role = "warehouse" // 仓库：登记发货（一次）、给商品加库存
	RoleCS        Role = "cs"        // 客服：审核退款、处理迟到支付
	RoleAdmin     Role = "admin"     // 管理员：可查看/操作全部 B 端功能，并维护 B 端员工账号
	RoleChannel   Role = "channel"   // 模拟支付渠道（仅内部使用）
)

// Actor 表示一次请求的调用方身份。
type Actor struct {
	Role   Role   `json:"role"`
	UserID string `json:"userId"`
	Name   string `json:"name"`
}

// ---------------- 订单状态 ----------------

type OrderStatus string

const (
	StatusPendingPay  OrderStatus = "PENDING_PAY"  // 待付款：库存已占用
	StatusClosed      OrderStatus = "CLOSED"       // 已关闭：库存已释放
	StatusPendingShip OrderStatus = "PENDING_SHIP" // 待发货：已收款，库存仍占用
	StatusRefunding   OrderStatus = "REFUNDING"    // 退款中：冻结发货
	StatusShipped     OrderStatus = "SHIPPED"      // 已发货：库存实际扣减
	StatusSigned      OrderStatus = "SIGNED"       // 已签收：买家确认收货（可评价）
	StatusRefunded    OrderStatus = "REFUNDED"     // 已退款（售前恢复库存 / 迟到不恢复）
	StatusAbnormalPay OrderStatus = "ABNORMAL_PAY" // 异常支付：关闭后迟到的成功通知
)

var StatusLabels = map[OrderStatus]string{
	StatusPendingPay:  "待付款",
	StatusClosed:      "已关闭",
	StatusPendingShip: "待发货",
	StatusRefunding:   "退款中",
	StatusShipped:     "已发货",
	StatusSigned:      "已签收",
	StatusRefunded:    "已退款",
	StatusAbnormalPay: "异常支付",
}

func (s OrderStatus) Label() string {
	if v, ok := StatusLabels[s]; ok {
		return v
	}
	return string(s)
}

// ---------------- 商品 ----------------

type Product struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Image       string `json:"image"`
	Category    string `json:"category"`    // 商品分类：家用电器 / 电子产品 / 儿童玩具 / 书本 / 食品 / 家居生活 / 其他
	PriceFen    int64  `json:"priceFen"`    // 当前单价（分）
	OldPriceFen int64  `json:"oldPriceFen"` // 划线价（分）
	Total       int    `json:"total"`       // 总库存
	Available   int    `json:"available"`   // 可售数量（下单即扣，关闭/售前退款回补）
	OnSale      bool   `json:"onSale"`
	Sales       int    `json:"sales"`
}

// ---------------- 订单 ----------------

type Order struct {
	ID              string      `json:"id"`
	BuyerID         string      `json:"buyerId"`
	BuyerName       string      `json:"buyerName"`
	ProductID       string      `json:"productId"`
	ProductName     string      `json:"productName"`  // 下单时名称快照
	ProductImage    string      `json:"productImage"` // 下单时图片快照
	PriceFen        int64       `json:"priceFen"`     // 下单时单价快照（分）
	Qty             int         `json:"qty"`
	AmountFen       int64       `json:"amountFen"`              // = PriceFen * Qty（分）
	Channel         string      `json:"channel,omitempty"`      // 支付方式：wechat / alipay（下单时选定）
	ReceiverName    string      `json:"receiverName,omitempty"` // 收货人快照（下单时选定的地址）
	ReceiverPhone   string      `json:"receiverPhone,omitempty"`
	ReceiverAddress string      `json:"receiverAddress,omitempty"` // 收货地址快照（地址后续修改不影响订单）
	Status          OrderStatus `json:"status"`
	CreatedAt       time.Time   `json:"createdAt"`
	ExpiresAt       time.Time   `json:"expiresAt"` // 待付款保留 10 分钟（服务端时间）
	ClosedAt        *time.Time  `json:"closedAt,omitempty"`
	PaidAt          *time.Time  `json:"paidAt,omitempty"`
	ChannelTxnID    string      `json:"channelTxnId,omitempty"` // 成功收款的渠道交易号
	RefundID        string      `json:"refundId,omitempty"`     // 当前/最近一次退款单
	ShipmentID      string      `json:"shipmentId,omitempty"`   // 发货记录（对订单唯一）
	AbnormalID      string      `json:"abnormalId,omitempty"`   // 迟到支付异常记录
	StatusUpdatedAt time.Time   `json:"statusUpdatedAt"`
}

// ChannelLabel 支付方式展示名。
func ChannelLabel(ch string) string {
	if ch == "wechat" {
		return "微信支付"
	}
	if ch == "alipay" {
		return "支付宝支付"
	}
	return "-"
}

// ---------------- 收货地址 ----------------

// Address 买家收货地址。下单时把地址快照进订单，之后改地址不影响历史订单；
// 同一用户最多保存 10 个，默认地址在其本人地址内切换（有且只有一个）。
type Address struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	Name      string    `json:"name"`
	Phone     string    `json:"phone"`
	Region    string    `json:"region"` // 省/市/区
	Detail    string    `json:"detail"` // 街道门牌等详细地址
	IsDefault bool      `json:"isDefault"`
	CreatedAt time.Time `json:"createdAt"`
}

// Full 拼接展示用完整地址。
func (a *Address) Full() string { return a.Region + " " + a.Detail }

// ---------------- 支付 ----------------

// PaymentRecord 成功收款记录；ChannelTxnID 为唯一键（防重复收款、防串单）。
type PaymentRecord struct {
	ChannelTxnID string    `json:"channelTxnId"`
	OrderID      string    `json:"orderId"`
	AmountFen    int64     `json:"amountFen"`
	CreatedAt    time.Time `json:"createdAt"`
}

// PaymentIntent 模拟渠道受理付款后生成的待投递通知；签名由渠道密钥生成，服务端验签。
type PaymentIntent struct {
	ChannelTxnID string `json:"channelTxnId"`
	OrderID      string `json:"orderId"`
	AmountFen    int64  `json:"amountFen"`
	Status       string `json:"status"` // SUCCESS
	Nonce        string `json:"nonce"`
	Signature    string `json:"signature"`
}

// NotifyResult 每一笔渠道通知的可查询处理结果（只增不改）。
type NotifyResult struct {
	ID           string    `json:"id"`
	ChannelTxnID string    `json:"channelTxnId"`
	OrderID      string    `json:"orderId"`
	AmountFen    int64     `json:"amountFen"`
	Outcome      string    `json:"outcome"` // ACCEPTED/DUPLICATE/LATE_PAYMENT/BAD_SIGNATURE/AMOUNT_MISMATCH/UNKNOWN_ORDER/IGNORED/TXN_CONFLICT
	Detail       string    `json:"detail"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ---------------- 退款 ----------------

type RefundSource string

const (
	RefundSourcePresale RefundSource = "presale" // 售前退款：渠道成功需恢复库存
	RefundSourceLate    RefundSource = "late"    // 迟到支付退款：绝不恢复库存
)

type RefundStatus string

const (
	RefundPending   RefundStatus = "PENDING"   // 待客服审核
	RefundAwait     RefundStatus = "AWAITING"  // 已同意，等待渠道确认
	RefundFailed    RefundStatus = "FAILED"    // 渠道失败，保留待处理可重试
	RefundSucceeded RefundStatus = "SUCCEEDED" // 渠道确认成功
	RefundRejected  RefundStatus = "REJECTED"  // 客服拒绝
)

type Refund struct {
	ID              string       `json:"id"`
	OrderID         string       `json:"orderId"`
	Source          RefundSource `json:"source"` // 决定库存是否恢复的唯一依据
	AmountFen       int64        `json:"amountFen"`
	Status          RefundStatus `json:"status"`
	Reason          string       `json:"reason"`
	ReviewNote      string       `json:"reviewNote"`
	ChannelRefundID string       `json:"channelRefundId,omitempty"`
	Attempts        int          `json:"attempts"`
	CreatedAt       time.Time    `json:"createdAt"`
	UpdatedAt       time.Time    `json:"updatedAt"`
}

// ---------------- 发货 / 异常支付 ----------------

// Shipment 发货记录；OrderID 唯一，保证「只能登记一次发货」。
// 签收信息回写同一条记录：SignedAt/SignedBy 由买家「确认收货」写入，仓库可查签收时间。
type Shipment struct {
	ID         string     `json:"id"`
	OrderID    string     `json:"orderId"`
	Carrier    string     `json:"carrier"`
	TrackingNo string     `json:"trackingNo"`
	CreatedAt  time.Time  `json:"createdAt"`
	Operator   string     `json:"operator"`
	SignedAt   *time.Time `json:"signedAt,omitempty"`
	SignedBy   string     `json:"signedBy,omitempty"`
}

type AbnormalStatus string

const (
	AbnormalPending  AbnormalStatus = "PENDING"
	AbnormalRefunded AbnormalStatus = "REFUNDED"
)

// AbnormalPayment 迟到支付异常记录（只能被处理，不能被改状态抹掉）。
type AbnormalPayment struct {
	ID           string         `json:"id"`
	OrderID      string         `json:"orderId"`
	ChannelTxnID string         `json:"channelTxnId"`
	AmountFen    int64          `json:"amountFen"`
	Status       AbnormalStatus `json:"status"`
	Reason       string         `json:"reason"`
	HandleNote   string         `json:"handleNote,omitempty"`
	RefundID     string         `json:"refundId,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	HandledAt    *time.Time     `json:"handledAt,omitempty"`
}

// ---------------- 流水 ----------------

// LedgerEntry 运营对账流水：只增不改，异常记录不会被状态变更抹掉。
type LedgerEntry struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"` // 库存占用/库存释放/收款/退款/发货/异常支付/通知结果
	AmountFen int64     `json:"amountFen"`
	OrderID   string    `json:"orderId"`
	RefID     string    `json:"refId"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
}

// ---------------- 商品操作记录 ----------------

// ProductOp 商品维度的操作留痕（新增 / 上架 / 下架），与「资金流水」分开存放：
// 资金流水只记钱（收款、退款及其审核过程），商品操作不污染对账口径。
type ProductOp struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"` // 新增商品 / 商品上架 / 商品下架
	ProductID string    `json:"productId"`
	Name      string    `json:"name"` // 商品名快照（商品改名后仍可追溯）
	Operator  string    `json:"operator"`
	CreatedAt time.Time `json:"createdAt"`
}

// ---------------- 商品评论 ----------------

// Review 商品评论：ParentID 为空表示一级评论，否则是对某条评论的回复（只支持两级）。
// 只有「已签收」订单的买家才能评论 / 回复该商品。
type Review struct {
	ID        string    `json:"id"`
	ProductID string    `json:"productId"`
	OrderID   string    `json:"orderId"`
	ParentID  string    `json:"parentId,omitempty"`
	BuyerID   string    `json:"buyerId"`
	BuyerName string    `json:"buyerName"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

// ---------------- 订单客服消息 ----------------

// ChatMsg 订单维度的客服会话消息。Kind=order 表示买家发送的订单卡片快照，
// 客服据此定位订单；Kind=text 为普通文本。
type ChatMsg struct {
	ID        string    `json:"id"`
	OrderID   string    `json:"orderId"`
	FromRole  Role      `json:"fromRole"`
	FromID    string    `json:"fromId"`
	FromName  string    `json:"fromName"`
	Kind      string    `json:"kind"` // text / order
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

// ---------------- 全量状态 ----------------

type State struct {
	Products         []*Product         `json:"products"`
	Orders           []*Order           `json:"orders"`
	Payments         []*PaymentRecord   `json:"payments"`
	NotifyResults    []*NotifyResult    `json:"notifyResults"`
	Refunds          []*Refund          `json:"refunds"`
	Shipments        []*Shipment        `json:"shipments"`
	AbnormalPayments []*AbnormalPayment `json:"abnormalPayments"`
	Ledger           []*LedgerEntry     `json:"ledger"`
	ProductOps       []*ProductOp       `json:"productOps"`
	Reviews          []*Review          `json:"reviews"`
	ChatMsgs         []*ChatMsg         `json:"chatMsgs"`
	Addresses        []*Address         `json:"addresses"`
	Seq              int64              `json:"seq"`
}

// NextID 在写锁内生成单调递增序号，用于构造业务主键。
func (s *State) NextID(prefix string) string {
	s.Seq++
	return prefix + fmtInt(s.Seq)
}

func fmtInt(n int64) string {
	if n == 0 {
		return "0"
	}
	buf := [24]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func (s *State) Product(id string) *Product {
	for _, p := range s.Products {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (s *State) Order(id string) *Order {
	for _, o := range s.Orders {
		if o.ID == id {
			return o
		}
	}
	return nil
}

func (s *State) Refund(id string) *Refund {
	for _, r := range s.Refunds {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (s *State) PaymentByTxn(txn string) *PaymentRecord {
	for _, p := range s.Payments {
		if p.ChannelTxnID == txn {
			return p
		}
	}
	return nil
}

func (s *State) ShipmentByOrder(orderID string) *Shipment {
	for _, sh := range s.Shipments {
		if sh.OrderID == orderID {
			return sh
		}
	}
	return nil
}

func (s *State) AbnormalByOrder(orderID string) *AbnormalPayment {
	for _, a := range s.AbnormalPayments {
		if a.OrderID == orderID {
			return a
		}
	}
	return nil
}

func (s *State) AppendLedger(typ string, amountFen int64, orderID, refID, note string, at time.Time) *LedgerEntry {
	s.Seq++
	e := &LedgerEntry{ID: s.Seq, Type: typ, AmountFen: amountFen, OrderID: orderID, RefID: refID, Note: note, CreatedAt: at}
	s.Ledger = append(s.Ledger, e)
	return e
}

// AppendProductOp 追加一条商品操作留痕（新增 / 上架 / 下架）。
// 与资金流水共用单调序号，但独立存储、独立视图，互不污染。
func (s *State) AppendProductOp(typ, productID, name, operator string, at time.Time) *ProductOp {
	s.Seq++
	op := &ProductOp{ID: s.Seq, Type: typ, ProductID: productID, Name: name, Operator: operator, CreatedAt: at}
	s.ProductOps = append(s.ProductOps, op)
	return op
}

// SoldOut 可售归零且在售即「已售罄」。
func (p *Product) SoldOut() bool {
	return p != nil && p.OnSale && p.Available <= 0
}

// StatusLabel 商品展示状态：已下架 / 已售罄 / 在售（派生值，不落库）。
func (p *Product) StatusLabel() string {
	switch {
	case p == nil:
		return "-"
	case !p.OnSale:
		return "已下架"
	case p.Available <= 0:
		return "已售罄"
	default:
		return "在售"
	}
}

// Review 按 ID 查评论。
func (s *State) Review(id string) *Review {
	for _, r := range s.Reviews {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// Address 按 ID 查地址。
func (s *State) Address(id string) *Address {
	for _, a := range s.Addresses {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// AddressesOf 某用户的地址（按创建时间升序）。
func (s *State) AddressesOf(userID string) []*Address {
	out := make([]*Address, 0, 4)
	for _, a := range s.Addresses {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out
}

// DefaultAddressOf 某用户的默认地址（无显式默认时回退第一个）。
func (s *State) DefaultAddressOf(userID string) *Address {
	var first *Address
	for _, a := range s.AddressesOf(userID) {
		if a.IsDefault {
			return a
		}
		if first == nil {
			first = a
		}
	}
	return first
}

// ChatMsgsOf 返回某订单的客服消息（按时间升序）。
func (s *State) ChatMsgsOf(orderID string) []*ChatMsg {
	out := make([]*ChatMsg, 0, 8)
	for _, m := range s.ChatMsgs {
		if m.OrderID == orderID {
			out = append(out, m)
		}
	}
	return out
}
