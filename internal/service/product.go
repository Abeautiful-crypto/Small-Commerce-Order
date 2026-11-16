package service

import (
	"strings"

	"small-commerce-order/internal/model"
)

// CreateProduct 运营新建商品（R8：商品维护属运营职责，创建后默认在售并记流水）。
func (s *Service) CreateProduct(actor model.Actor, name string, priceFen, stock int64) (*model.Product, error) {
	if err := requireRole(actor, model.RoleOps); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, bad("商品名称不能为空")
	}
	if priceFen <= 0 {
		return nil, bad("价格必须大于 0")
	}
	if stock < 0 {
		return nil, bad("库存不能为负数")
	}
	var out *model.Product
	err := s.st.Update(func(st *model.State) error {
		p := &model.Product{
			ID:          st.NextID("P"),
			Name:        name,
			Image:       "images/product1.png", // 新建商品统一占位图，买家端可正常展示
			PriceFen:    priceFen,
			OldPriceFen: priceFen,
			Total:       int(stock),
			Available:   int(stock),
			OnSale:      true,
		}
		st.Products = append(st.Products, p)
		st.AppendLedger("商品上架", 0, "", p.ID, "新建商品："+p.Name, s.now())
		out = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
