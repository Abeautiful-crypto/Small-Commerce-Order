package service

import (
	"encoding/json"
	"testing"

	"small-commerce-order/internal/model"
)

// 签收链路：已发货 → 已签收，签收时间回写发货记录；重复签收 / 跨买家签收 / 未发货签收都要拒绝。
func TestSign_Flow(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer1, "P1", 1)
	if _, err := h.svc.Ship(wh, o.ID, "顺丰速运", "SF100"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Sign(buyer2, o.ID); err == nil {
		t.Fatal("非本人订单不能签收")
	}
	signed, err := h.svc.Sign(buyer1, o.ID)
	if err != nil {
		t.Fatalf("签收应成功：%v", err)
	}
	if signed.Status != model.StatusSigned {
		t.Fatalf("状态应为已签收，实际 %s", signed.Status)
	}
	shs, err := h.svc.Shipments(wh)
	if err != nil {
		t.Fatal(err)
	}
	var hit *model.Shipment
	for _, sh := range shs {
		if sh.OrderID == o.ID {
			hit = sh
		}
	}
	if hit == nil || hit.SignedAt == nil || hit.SignedBy == "" {
		t.Fatalf("发货记录应回写签收时间与签收人，实际 %+v", hit)
	}
	if _, err := h.svc.Sign(buyer1, o.ID); err == nil {
		t.Fatal("重复签收应被拒绝")
	}
	// 未发货订单不能签收
	o2 := h.createPaidOrder(t, buyer1, "P1", 1)
	if _, err := h.svc.Sign(buyer1, o2.ID); err == nil {
		t.Fatal("待发货订单不能签收")
	}
}

// 评论：只有「本人 + 已签收 + 同一商品」才能发；一个订单只能一条一级评论，回复只支持两级。
func TestReview_Rules(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer1, "P1", 1)
	if _, err := h.svc.PostReview(buyer1, "P1", o.ID, "", "还没签收就来评论"); err == nil {
		t.Fatal("未签收订单不能评价")
	}
	if _, err := h.svc.Ship(wh, o.ID, "顺丰速运", "SF1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Sign(buyer1, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.PostReview(buyer2, "P1", o.ID, "", "别人的订单"); err == nil {
		t.Fatal("不能评价别人的订单")
	}
	top, err := h.svc.PostReview(buyer1, "P1", o.ID, "", "音质不错，续航也够用")
	if err != nil {
		t.Fatalf("签收后应可评价：%v", err)
	}
	if _, err := h.svc.PostReview(buyer1, "P1", o.ID, "", "重复评价"); err == nil {
		t.Fatal("同一订单不能发多条一级评论")
	}
	reply, err := h.svc.PostReview(buyer1, "P1", o.ID, top.ID, "补充：戴久了有点闷")
	if err != nil {
		t.Fatalf("回复一级评论应成功：%v", err)
	}
	if reply.ParentID != top.ID {
		t.Fatalf("回复的 ParentID 应为 %s，实际 %s", top.ID, reply.ParentID)
	}
	if _, err := h.svc.PostReview(buyer1, "P1", o.ID, reply.ID, "三级评论"); err == nil {
		t.Fatal("评论只支持两级")
	}
	list, err := h.svc.ReviewsOf(buyer2, "P1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("商品评论应有 2 条，实际 %d", len(list))
	}
	if _, err := h.svc.ReviewsOf(model.Actor{}, "P1"); err == nil {
		t.Fatal("未登录不能看评论")
	}
}

// 会话：买家发订单卡片由服务端生成快照；客服可回消息；其它角色 / 非本人订单拒绝访问。
func TestChat_Flow(t *testing.T) {
	h := newHarness(t)
	o := h.createPaidOrder(t, buyer1, "P1", 2)
	card, err := h.svc.SendChat(buyer1, o.ID, "order", "")
	if err != nil {
		t.Fatalf("发送订单卡片应成功：%v", err)
	}
	if card.Kind != "order" {
		t.Fatalf("卡片消息 kind 应为 order，实际 %s", card.Kind)
	}
	var snap OrderCard
	if err := json.Unmarshal([]byte(card.Text), &snap); err != nil {
		t.Fatalf("卡片内容应为订单快照 JSON：%v", err)
	}
	if snap.OrderID != o.ID || snap.AmountFen != o.AmountFen || snap.ProductName != o.ProductName {
		t.Fatalf("卡片快照字段不符：%+v", snap)
	}
	if _, err := h.svc.SendChat(buyer1, o.ID, "text", "   "); err == nil {
		t.Fatal("空文本消息应被拒绝")
	}
	if _, err := h.svc.SendChat(buyer2, o.ID, "text", "偷看别人订单"); err == nil {
		t.Fatal("不能在他人的订单会话里发消息")
	}
	if _, err := h.svc.SendChat(ops, o.ID, "text", "运营不该进客服会话"); err == nil {
		t.Fatal("运营无权进入客服会话")
	}
	if _, err := h.svc.SendChat(cs, o.ID, "text", "您好，我在的，稍等帮您查一下"); err != nil {
		t.Fatalf("客服应可回复：%v", err)
	}
	msgs, err := h.svc.ChatMessages(cs, o.ID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("会话应有 2 条消息，实际 %d %v", len(msgs), err)
	}
	threads, err := h.svc.ChatThreads(cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].OrderID != o.ID || threads[0].MsgCount != 2 {
		t.Fatalf("会话列表不符：%+v", threads)
	}
	if threads[0].BuyerName != buyer1.Name {
		t.Fatalf("会话应带买家信息，实际 %s", threads[0].BuyerName)
	}
	// 客服已回复时最后发言方是客服：前端据此判断「已回复 / 待回复」
	if threads[0].LastFromRole != model.RoleCS {
		t.Fatalf("最后发言方应为客服，实际 %s", threads[0].LastFromRole)
	}
	if _, err := h.svc.SendChat(buyer1, o.ID, "text", "麻烦尽快处理下"); err != nil {
		t.Fatal(err)
	}
	threads, _ = h.svc.ChatThreads(cs)
	if threads[0].LastFromRole != model.RoleBuyer {
		t.Fatalf("买家再次发言后最后发言方应变为买家，实际 %s", threads[0].LastFromRole)
	}
	// 买家也能拉会话摘要（用于「客服已回复」提示），但只能看到自己订单的会话
	bt, err := h.svc.ChatThreads(buyer1)
	if err != nil {
		t.Fatalf("买家应可查看本人会话：%v", err)
	}
	if len(bt) != 1 || bt[0].OrderID != o.ID || bt[0].BuyerID != buyer1.UserID {
		t.Fatalf("买家会话列表应只含本人订单：%+v", bt)
	}
	if _, err := h.svc.ChatThreads(ops); err == nil {
		t.Fatal("运营无权访问会话列表")
	}
}

// 收货地址与下单约束：地址必须本人、支付方式必须合法；默认地址唯一且可切换；订单快照地址。
func TestAddressAndOrderConstraints(t *testing.T) {
	h := newHarness(t)
	a1, err := h.svc.AddAddress(buyer1, AddressInput{Name: "张小买", Phone: "13800000001",
		Region: "浙江省 杭州市 西湖区", Detail: "文三路 1 号", IsDefault: false})
	if err != nil {
		t.Fatalf("新增地址失败：%v", err)
	}
	if !a1.IsDefault {
		t.Fatal("第一个地址应自动成为默认地址")
	}
	a2, err := h.svc.AddAddress(buyer1, AddressInput{Name: "张小买", Phone: "13800000001",
		Region: "上海市 浦东新区", Detail: "世纪大道 2 号", IsDefault: true})
	if err != nil {
		t.Fatalf("新增第二地址失败：%v", err)
	}
	list, _ := h.svc.Addresses(buyer1)
	if len(list) != 2 {
		t.Fatalf("应有 2 个地址，实际 %d", len(list))
	}
	if err := h.svc.SetDefaultAddress(buyer1, a2.ID); err != nil {
		t.Fatal(err)
	}
	def, _ := h.svc.Addresses(buyer1)
	dcnt := 0
	for _, a := range def {
		if a.IsDefault {
			dcnt++
			if a.ID != a2.ID {
				t.Fatal("默认地址应已切换")
			}
		}
	}
	if dcnt != 1 {
		t.Fatalf("默认地址应有且只有一个，实际 %d", dcnt)
	}
	// 他人地址不可用于下单；未选地址与非法支付方式同样拒绝
	if _, err := h.svc.CreateOrder(buyer2, "P1", 1, "alipay", a2.ID); errCode(err) != CodeBadRequest {
		t.Fatalf("他人地址不可下单，实际 %v", err)
	}
	if _, err := h.svc.CreateOrder(buyer1, "P1", 1, "alipay", ""); errCode(err) != CodeBadRequest {
		t.Fatalf("未选地址不可下单，实际 %v", err)
	}
	if _, err := h.svc.CreateOrder(buyer1, "P1", 1, "unionpay", h.addr(buyer1)); errCode(err) != CodeBadRequest {
		t.Fatalf("非法支付方式应拒绝，实际 %v", err)
	}
	if _, err := h.svc.AddAddress(ops, AddressInput{Name: "x", Phone: "13800000002",
		Region: "a", Detail: "b"}); err == nil {
		t.Fatal("地址管理只对买家开放")
	}
	// 修改地址：内容更新、可顺手设为默认；他人地址不可改
	upd, err := h.svc.UpdateAddress(buyer1, a1.ID, AddressInput{Name: "张小买（公司）", Phone: "13800000001",
		Region: "浙江省 杭州市 余杭区", Detail: "文一西路 999 号"})
	if err != nil {
		t.Fatalf("修改地址失败：%v", err)
	}
	if upd.Name != "张小买（公司）" || upd.Region != "浙江省 杭州市 余杭区" || upd.Detail != "文一西路 999 号" {
		t.Fatalf("地址修改未生效：%+v", upd)
	}
	if _, err := h.svc.UpdateAddress(buyer1, a1.ID, AddressInput{Name: "", Phone: "13800000001",
		Region: "x", Detail: "y"}); errCode(err) != CodeBadRequest {
		t.Fatalf("收货人必填，实际 %v", err)
	}
	if _, err := h.svc.UpdateAddress(buyer2, a1.ID, AddressInput{Name: "盗用", Phone: "13800000002",
		Region: "x", Detail: "y"}); errCode(err) != CodeNotFound {
		t.Fatalf("他人地址不可修改，实际 %v", err)
	}
	// 正常下单：支付方式与地址快照进订单；之后修改地址不影响订单
	o, err := h.svc.CreateOrder(buyer1, "P1", 1, "wechat", a2.ID)
	if err != nil {
		t.Fatalf("下单失败：%v", err)
	}
	if o.Channel != "wechat" || o.ReceiverName != "张小买" || o.ReceiverAddress != "上海市 浦东新区 世纪大道 2 号" {
		t.Fatalf("订单快照错误：%+v", o)
	}
	if err := h.svc.DeleteAddress(buyer1, a2.ID); err != nil {
		t.Fatal(err)
	}
	o2 := h.order(t, o.ID)
	if o2.ReceiverAddress != "上海市 浦东新区 世纪大道 2 号" {
		t.Fatalf("删除地址不应影响历史订单快照：%v", o2.ReceiverAddress)
	}
	// 删除默认地址后自动提升最新一条（h.addr 在下单用例里又建了一个地址，共剩 2 个）
	rest, _ := h.svc.Addresses(buyer1)
	if len(rest) != 2 || !rest[len(rest)-1].IsDefault {
		t.Fatalf("删除默认地址后应自动提升最新一条为默认，实际 %+v", rest)
	}
}

// 资金流水口径：商品动作不写入流水；实收 = 收款 − 退款。
func TestLedger_MoneyOnly(t *testing.T) {
	h := newHarness(t)
	p, err := h.svc.CreateProduct(ops, "测试用保温杯", "uploads/a.png", "家居生活", 5000, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetOnSale(ops, p.ID, false); err != nil {
		t.Fatal(err)
	}
	led, err := h.svc.Ledger(ops)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range led {
		if l.Type == "商品上架" || l.Type == "商品下架" {
			t.Fatalf("资金流水不应包含商品动作：%+v", l)
		}
	}
	o := h.createPaidOrder(t, buyer1, "P2", 1)
	r, err := h.svc.ApplyRefund(buyer1, o.ID, "买错了")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ReviewRefund(cs, r.ID, true, "测试退款"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.HandleRefundChannelNotify(cs, r.ID, true, "CH-REF-1"); err != nil {
		t.Fatalf("渠道退款成功应写入退款流水：%v", err)
	}
	sum, err := h.svc.Summary(ops)
	if err != nil {
		t.Fatal(err)
	}
	if sum.NetFen != sum.ReceivedFen-sum.RefundedFen {
		t.Fatalf("实收应等于收款减退款：%+v", sum)
	}
	if sum.ReceivedFen == 0 || sum.RefundedFen != o.AmountFen {
		t.Fatalf("退款金额应计入汇总：%+v", sum)
	}
}

// 商品售罄：可售归零即在售状态为「已售罄」，下架优先显示「已下架」。
func TestProduct_StatusLabel(t *testing.T) {
	p := &model.Product{OnSale: true, Available: 2}
	if p.StatusLabel() != "在售" || p.SoldOut() {
		t.Fatalf("有货应为在售，实际 %s", p.StatusLabel())
	}
	h := newHarness(t)
	// P1 可售 4（预置订单已占用 1 件）：两笔各 2 件正好把可售占满
	for _, qty := range []int{2, 2} {
		if o := h.createPaidOrder(t, buyer1, "P1", qty); o.ID == "" {
			t.Fatal("下单失败")
		}
	}
	ps, err := h.svc.Products(ops)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range ps {
		if x.ID == "P1" {
			if !x.SoldOut() || x.StatusLabel() != "已售罄" {
				t.Fatalf("库存归零应显示已售罄，实际 %s 可售=%d", x.StatusLabel(), x.Available)
			}
		}
	}
	if err := h.svc.SetOnSale(ops, "P1", false); err != nil {
		t.Fatal(err)
	}
	ps, _ = h.svc.Products(ops)
	for _, x := range ps {
		if x.ID == "P1" && x.StatusLabel() != "已下架" {
			t.Fatalf("下架应优先显示已下架，实际 %s", x.StatusLabel())
		}
	}
}
