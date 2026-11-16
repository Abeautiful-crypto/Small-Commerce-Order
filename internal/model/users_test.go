package model

import "testing"

// 角色到端的归属：买家=C 端，运营/仓库/客服=B 端，渠道不属于任何端。
func TestSideOf(t *testing.T) {
	cases := []struct {
		role Role
		want string
	}{
		{RoleBuyer, SideCustomer},
		{RoleOps, SideBusiness},
		{RoleWarehouse, SideBusiness},
		{RoleCS, SideBusiness},
		{RoleChannel, ""},
	}
	for _, c := range cases {
		if got := SideOf(c.role); got != c.want {
			t.Fatalf("SideOf(%s)=%q，期望 %q", c.role, got, c.want)
		}
	}
	if SideLabel(SideCustomer) == SideLabel(SideBusiness) {
		t.Fatal("两端标签应可区分")
	}
}
