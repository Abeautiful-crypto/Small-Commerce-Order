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

func FindUser(id string, role Role) (User, bool) {
	for _, u := range Users {
		if u.ID == id && u.Role == role {
			return u, true
		}
	}
	return User{}, false
}
