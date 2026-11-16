package model

import "sync"

// User 是身份目录中的一条记录（不含密码）。
// 登录账号的真相源在数据库（users / staff 表），服务启动时由 app 层
// 通过 SetDirectory 同步到这里，供权限守卫做「ID + 角色」精确校验。
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role Role   `json:"role"`
}

// channelUser 是模拟支付渠道的内部身份，不属于任何登录端。
var channelUser = User{ID: "channel", Name: "模拟支付渠道", Role: RoleChannel}

var (
	dirMu sync.RWMutex
	dir   = builtinUsers()
)

// builtinUsers 是未接入账号表时的默认身份目录（单元测试与离线模式使用）。
func builtinUsers() []User {
	return []User{
		{ID: "u001", Name: "张小买", Role: RoleBuyer},
		{ID: "u002", Name: "李小购", Role: RoleBuyer},
		{ID: "u003", Name: "王小单", Role: RoleBuyer},
		{ID: "admin", Name: "运营专员", Role: RoleOps},
		{ID: "wh", Name: "仓库管理员", Role: RoleWarehouse},
		{ID: "cs", Name: "客服小花", Role: RoleCS},
		channelUser,
	}
}

// SetDirectory 用登录账号表覆盖身份目录（渠道内部身份始终保留）。
func SetDirectory(list []User) {
	dirMu.Lock()
	defer dirMu.Unlock()
	out := make([]User, 0, len(list)+1)
	for _, u := range list {
		if u.Role == RoleChannel {
			continue
		}
		out = append(out, u)
	}
	out = append(out, channelUser)
	dir = out
}

// AddUser 把新注册账号追加进身份目录（同 ID 覆盖），渠道内部身份保留。
func AddUser(u User) {
	if u.Role == RoleChannel || u.ID == "" {
		return
	}
	dirMu.Lock()
	defer dirMu.Unlock()
	out := make([]User, 0, len(dir)+1)
	for _, x := range dir {
		if x.ID != u.ID && x.ID != channelUser.ID {
			out = append(out, x)
		}
	}
	out = append(out, u, channelUser)
	dir = out
}

// Directory 返回当前身份目录的副本。
func Directory() []User {
	dirMu.RLock()
	defer dirMu.RUnlock()
	out := make([]User, len(dir))
	copy(out, dir)
	return out
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

// FindUser 按「ID + 角色」精确匹配身份目录。
func FindUser(id string, role Role) (User, bool) {
	dirMu.RLock()
	defer dirMu.RUnlock()
	for _, u := range dir {
		if u.ID == id && u.Role == role {
			return u, true
		}
	}
	return User{}, false
}
