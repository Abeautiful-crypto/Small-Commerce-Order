package service

import (
	"strings"

	"small-commerce-order/internal/model"
)

// Categories 是商品可选的分类（与 C 端逛逛页的分类筛选一致）。
var Categories = []string{"家用电器", "电子产品", "儿童玩具", "书本", "食品", "家居生活", "其他"}

// DefaultCategory 未指定分类时的归属。
const DefaultCategory = "其他"

// normalizeCategory 校验分类是否在白名单内；空值归入「其他」。
func normalizeCategory(raw string) (string, error) {
	c := strings.TrimSpace(raw)
	if c == "" {
		return DefaultCategory, nil
	}
	for _, ok := range Categories {
		if c == ok {
			return c, nil
		}
	}
	return "", bad("商品分类不在允许范围内：" + c)
}

// normalizeImage 校验商品图片来源：只接受 http(s) 链接或站内 images/、uploads/ 相对路径，
// 避免把 javascript: / data: 这类可执行伪协议写进 <img src>。
// 新建商品必须上传图片（由 /api/uploads 落盘后返回站内 URL），不再提供默认占位图。
func normalizeImage(raw string) (string, error) {
	img := strings.TrimSpace(raw)
	if img == "" {
		return "", bad("请上传商品图片")
	}
	if len(img) > 255 {
		return "", bad("商品图片地址过长（最多 255 字符）")
	}
	lower := strings.ToLower(img)
	for _, prefix := range []string{"https://", "http://", "images/", "/images/", "uploads/", "/uploads/"} {
		if strings.HasPrefix(lower, prefix) {
			return img, nil
		}
	}
	return "", bad("商品图片必须是上传后的站内地址或 http(s) 链接")
}

// CreateProduct 运营新建商品（R8：商品维护属运营职责，创建后默认在售并记流水）。
func (s *Service) CreateProduct(actor model.Actor, name, image, category string, priceFen, stock int64) (*model.Product, error) {
	if err := requireRole(actor, model.RoleOps, model.RoleAdmin); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, bad("商品名称不能为空")
	}
	img, err := normalizeImage(image)
	if err != nil {
		return nil, err
	}
	cat, err := normalizeCategory(category)
	if err != nil {
		return nil, err
	}
	if priceFen <= 0 {
		return nil, bad("价格必须大于 0")
	}
	if stock < 0 {
		return nil, bad("库存不能为负数")
	}
	var out *model.Product
	err = s.st.Update(func(st *model.State) error {
		p := &model.Product{
			ID:          st.NextID("P"),
			Name:        name,
			Image:       img,
			Category:    cat,
			PriceFen:    priceFen,
			OldPriceFen: priceFen,
			Total:       int(stock),
			Available:   int(stock),
			OnSale:      true,
		}
		st.Products = append(st.Products, p)
		st.AppendProductOp("新增商品", p.ID, p.Name, actor.Name, s.now())
		out = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AddStock 仓库给商品补货：总库存与可售数量同时增加，并写入商品操作留痕。
// 与下单扣减共用同一把写事务，保证 available 与 total 始终一致。
func (s *Service) AddStock(actor model.Actor, productID string, qty int64) (*model.Product, error) {
	if err := requireRole(actor, model.RoleWarehouse, model.RoleOps, model.RoleAdmin); err != nil {
		return nil, err
	}
	if qty <= 0 {
		return nil, bad("添加数量必须大于 0")
	}
	if qty > 9999 {
		return nil, bad("单次最多添加 9999 件")
	}
	var out *model.Product
	err := s.st.Update(func(st *model.State) error {
		p := st.Product(productID)
		if p == nil {
			return notFound("商品不存在")
		}
		p.Total += int(qty)
		p.Available += int(qty)
		st.AppendProductOp("添加库存", p.ID, p.Name, actor.Name, s.now())
		out = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
