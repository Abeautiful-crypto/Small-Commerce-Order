package model

// User 是预置的演示账号。买家 3 名，运营/仓库/客服各 1 名。
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role Role   `json:"role"`
}

var Users = []User{
	{ID: "u001", Name: "张小买", Role: RoleBuyer},
	{ID: "u002", Name: "李小购", Role: RoleBuyer},
	{ID: "u003", Name: "王小单", Role: RoleBuyer},
	{ID: "admin", Name: "运营专员", Role: RoleOps},
	{ID: "wh", Name: "仓库管理员", Role: RoleWarehouse},
	{ID: "cs", Name: "客服小花", Role: RoleCS},
	// 模拟支付渠道：仅用于渠道回调接口的内部身份，不出现在登录页角色列表中。
	{ID: "channel", Name: "模拟支付渠道", Role: RoleChannel},
}

// 页面端划分：C 端（买家购物页）与 B 端（工作台）分别由独立端口托管，
// 登录接口按角色所属端做硬校验，仓库管理员无法在 C 端登录。
const (
	SideCustomer = "c" // C 端：仅买家
	SideBusiness = "b" // B 端：运营 / 仓库 / 客服
)

// SideOf 返回角色所属端；渠道身份不属于任何登录端（返回空串）。
func SideOf(role Role) string {
	switch role {
	case RoleBuyer:
		return SideCustomer
	case RoleOps, RoleWarehouse, RoleCS:
		return SideBusiness
	}
	return ""
}

// SideLabel 返回端的中文名称，用于登录页与错误提示。
func SideLabel(side string) string {
	switch side {
	case SideCustomer:
		return "C 端（买家）"
	case SideBusiness:
		return "B 端（工作台）"
	}
	return "任意端"
}

func FindUser(id string, role Role) (User, bool) {
	for _, u := range Users {
		if u.ID == id && u.Role == role {
			return u, true
		}
	}
	return User{}, false
}
