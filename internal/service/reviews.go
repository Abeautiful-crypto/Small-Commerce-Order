package service

import (
	"strings"

	"small-commerce-order/internal/model"
)

const maxReviewRunes = 500

// ReviewsOf 返回某商品的评论（一级 + 回复，平铺返回，前端按 ParentID 组树）。
// 登录用户均可查看。
func (s *Service) ReviewsOf(actor model.Actor, productID string) ([]*model.Review, error) {
	if actor.Role == "" {
		return nil, forbidden("请先登录后再查看评论")
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	if st.Product(productID) == nil {
		return nil, notFound("商品不存在")
	}
	out := make([]*model.Review, 0, 8)
	for _, r := range st.Reviews {
		if r.ProductID == productID {
			out = append(out, r)
		}
	}
	return out, nil
}

// PostReview 发表评论或回复。规则：
//   - 只有买家本人、且该订单已「已签收」、订单确实包含该商品，才能评论；
//   - 一个订单只能发一条一级评论，之后可在评论区继续回复；
//   - 回复只支持两级：parent 必须是一级评论。
func (s *Service) PostReview(actor model.Actor, productID, orderID, parentID, content string) (*model.Review, error) {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return nil, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, bad("评论内容不能为空")
	}
	if n := len([]rune(content)); n > maxReviewRunes {
		return nil, bad("评论最多 500 字")
	}
	parentID = strings.TrimSpace(parentID)
	var out *model.Review
	err := s.st.Update(func(st *model.State) error {
		if st.Product(productID) == nil {
			return notFound("商品不存在")
		}
		o := st.Order(orderID)
		if o == nil {
			return notFound("订单不存在")
		}
		if err := requireOwner(actor, o); err != nil {
			return err
		}
		if o.ProductID != productID {
			return bad("该订单不包含这个商品，不能评价")
		}
		if o.Status != model.StatusSigned {
			return conflict("只有已签收的订单才能评价，当前状态：" + o.Status.Label())
		}
		if parentID == "" {
			for _, r := range st.Reviews {
				if r.OrderID == orderID && r.ParentID == "" {
					return conflict("该订单已评价过，可以点「回复」继续补充")
				}
			}
		} else {
			parent := st.Review(parentID)
			if parent == nil {
				return notFound("要回复的评论不存在")
			}
			if parent.ProductID != productID {
				return bad("评论与商品不匹配")
			}
			if parent.ParentID != "" {
				return bad("评论只支持两级，请回复一级评论")
			}
		}
		r := &model.Review{
			ID:        st.NextID("RV"),
			ProductID: productID,
			OrderID:   orderID,
			ParentID:  parentID,
			BuyerID:   actor.UserID,
			BuyerName: actor.Name,
			Content:   content,
			CreatedAt: s.now(),
		}
		st.Reviews = append(st.Reviews, r)
		out = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
