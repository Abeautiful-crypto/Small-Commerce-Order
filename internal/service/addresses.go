package service

import (
	"strings"

	"small-commerce-order/internal/account"
	"small-commerce-order/internal/model"
)

// 收货地址管理：每个买家可保存多个地址（上限 10 个），
// 默认地址在同一用户的地址中切换（有且只有一个）；
// 下单时订单快照地址内容，之后修改/删除地址不影响历史订单。

const (
	maxAddresses   = 10
	maxNameRunes   = 20
	maxRegionRunes = 60
	maxDetailRunes = 100
)

// AddressInput 新增 / 编辑地址入参。
type AddressInput struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	Region    string `json:"region"`
	Detail    string `json:"detail"`
	IsDefault bool   `json:"isDefault"`
}

func (in AddressInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Region = strings.TrimSpace(in.Region)
	in.Detail = strings.TrimSpace(in.Detail)
	if in.Name == "" || len([]rune(in.Name)) > maxNameRunes {
		return bad("收货人姓名必填且不超过 20 字")
	}
	if !account.IsPhone(in.Phone) {
		return bad("联系电话必须是 11 位手机号")
	}
	if in.Region == "" || len([]rune(in.Region)) > maxRegionRunes {
		return bad("所在地区必填且不超过 60 字")
	}
	if in.Detail == "" || len([]rune(in.Detail)) > maxDetailRunes {
		return bad("详细地址必填且不超过 100 字")
	}
	return nil
}

// Addresses 返回本人地址列表（按创建时间升序）。
func (s *Service) Addresses(actor model.Actor) ([]*model.Address, error) {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return nil, err
	}
	st, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	return st.AddressesOf(actor.UserID), nil
}

// AddAddress 新增地址；第一个地址自动设为默认，勾选「设为默认」会抢占默认位。
func (s *Service) AddAddress(actor model.Actor, in AddressInput) (*model.Address, error) {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	var out *model.Address
	err := s.st.Update(func(st *model.State) error {
		mine := st.AddressesOf(actor.UserID)
		if len(mine) >= maxAddresses {
			return conflict("最多保存 10 个收货地址")
		}
		now := s.now()
		a := &model.Address{
			ID: st.NextID("ADDR-"), UserID: actor.UserID,
			Name: strings.TrimSpace(in.Name), Phone: in.Phone,
			Region: strings.TrimSpace(in.Region), Detail: strings.TrimSpace(in.Detail),
			IsDefault: in.IsDefault || len(mine) == 0, // 第一个地址必然是默认
			CreatedAt: now,
		}
		if a.IsDefault {
			clearDefault(st, actor.UserID)
		}
		st.Addresses = append(st.Addresses, a)
		out = a
		return nil
	})
	return out, err
}

// UpdateAddress 修改地址内容（收货人 / 电话 / 地区 / 详细地址，可选设为默认）。
// 历史订单保存的是下单时的地址快照，因此修改不影响已存在订单。
func (s *Service) UpdateAddress(actor model.Actor, id string, in AddressInput) (*model.Address, error) {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	var out *model.Address
	err := s.st.Update(func(st *model.State) error {
		a := st.Address(id)
		if a == nil || a.UserID != actor.UserID {
			return notFound("地址不存在")
		}
		a.Name = strings.TrimSpace(in.Name)
		a.Phone = in.Phone
		a.Region = strings.TrimSpace(in.Region)
		a.Detail = strings.TrimSpace(in.Detail)
		if in.IsDefault && !a.IsDefault {
			clearDefault(st, actor.UserID)
			a.IsDefault = true
		}
		out = a
		return nil
	})
	return out, err
}

// SetDefaultAddress 把某个地址设为默认（同一时间只有一个默认）。
func (s *Service) SetDefaultAddress(actor model.Actor, id string) error {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return err
	}
	return s.st.Update(func(st *model.State) error {
		a := st.Address(id)
		if a == nil || a.UserID != actor.UserID {
			return notFound("地址不存在")
		}
		clearDefault(st, actor.UserID)
		a.IsDefault = true
		return nil
	})
}

// DeleteAddress 删除地址；删掉默认地址后自动把最新一条提升为默认。
func (s *Service) DeleteAddress(actor model.Actor, id string) error {
	if err := requireRole(actor, model.RoleBuyer); err != nil {
		return err
	}
	return s.st.Update(func(st *model.State) error {
		a := st.Address(id)
		if a == nil || a.UserID != actor.UserID {
			return notFound("地址不存在")
		}
		wasDefault := a.IsDefault
		out := st.Addresses[:0]
		for _, x := range st.Addresses {
			if x.ID != id {
				out = append(out, x)
			}
		}
		st.Addresses = out
		if wasDefault {
			mine := st.AddressesOf(actor.UserID)
			if len(mine) > 0 {
				mine[len(mine)-1].IsDefault = true
			}
		}
		return nil
	})
}

// clearDefault 清掉该用户当前的默认标记（不含待设置的地址）。
func clearDefault(st *model.State, userID string) {
	for _, a := range st.AddressesOf(userID) {
		a.IsDefault = false
	}
}
