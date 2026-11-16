package account

import (
	"testing"

	"small-commerce-order/internal/model"
)

// 密码派生：同密码两次生成不同盐与密文，但都能校验通过；错密码一律失败。
func TestHashPassword_Roundtrip(t *testing.T) {
	h1, s1, err := HashPassword("123456")
	if err != nil {
		t.Fatal(err)
	}
	h2, s2, _ := HashPassword("123456")
	if h1 == h2 || s1 == s2 {
		t.Fatal("不同次派生应使用不同盐，密文也必须不同")
	}
	if !VerifyPassword("123456", h1, s1) {
		t.Fatal("正确密码应校验通过")
	}
	if VerifyPassword("1234567", h1, s1) {
		t.Fatal("错误密码必须校验失败")
	}
	if VerifyPassword("123456", h1, "not-hex") {
		t.Fatal("非法盐必须校验失败")
	}
}

// IsPhone：只有 11 位纯数字才是合法登录账号。
func TestIsPhone(t *testing.T) {
	for _, ok := range []string{"13800000001", "13900000003"} {
		if !IsPhone(ok) {
			t.Fatalf("%s 应为合法手机号", ok)
		}
	}
	for _, bad := range []string{"", "u001", "admin", "1380000000", "138000000011", "1380000000a", "1380000000 "} {
		if IsPhone(bad) {
			t.Fatalf("%q 不应被当作手机号", bad)
		}
	}
}

// 内存账号仓库：手机号 + 正确密码才放行；角色来自种子数据。
func TestMemRepo_Authenticate(t *testing.T) {
	r := NewMemRepo()
	for _, s := range Seed {
		acc, ok := r.Authenticate(s.Phone, s.Password)
		if !ok || acc.Role != s.Role || acc.Name != s.Name || acc.ID != s.ID {
			t.Fatalf("手机号 %s 应与种子一致，实际 %+v ok=%v", s.Phone, acc, ok)
		}
		if _, ok := r.FindByPhone(s.Phone); !ok {
			t.Fatalf("FindByPhone(%s) 应命中", s.Phone)
		}
	}
	if _, ok := r.Authenticate(Seed[0].Phone, "wrong"); ok {
		t.Fatal("错误密码必须拒绝")
	}
	if _, ok := r.Authenticate("13700000000", "123456"); ok {
		t.Fatal("不存在的手机号必须拒绝")
	}
	// 内部 ID 不再是登录账号
	if _, ok := r.Authenticate("u001", "123456"); ok {
		t.Fatal("内部 ID 不能作为登录账号")
	}
	if acc, ok := r.Find("wh"); !ok || acc.Role != model.RoleWarehouse || acc.Phone != "13900000002" {
		t.Fatalf("Find 应返回仓库角色与手机号，实际 %+v", acc)
	}
	if n := len(r.List()); n != len(Seed) {
		t.Fatalf("账号数量应为 %d，实际 %d", len(Seed), n)
	}
}

// 注册：手机号唯一、密码强度、注册后可直接用手机号 + 密码登录。
func TestMemRepo_Register(t *testing.T) {
	r := NewMemRepo()
	acc, err := r.Register("张三", "13712345678", "abc123456")
	if err != nil {
		t.Fatalf("注册应成功：%v", err)
	}
	if acc.Role != model.RoleBuyer || acc.Phone != "13712345678" || acc.ID != "u13712345678" {
		t.Fatalf("注册账号字段不符：%+v", acc)
	}
	if got, ok := r.Authenticate("13712345678", "abc123456"); !ok || got.ID != acc.ID {
		t.Fatalf("注册后应能用手机号登录：%+v %v", got, ok)
	}
	if _, err := r.Register("重复", "13712345678", "abc123456"); err != ErrPhoneTaken {
		t.Fatalf("重复手机号应返回 ErrPhoneTaken，实际 %v", err)
	}
	if _, err := r.Register("短密码", "13711112222", "123"); err != ErrWeakPassword {
		t.Fatalf("短密码应返回 ErrWeakPassword，实际 %v", err)
	}
	if _, err := r.Register("坏手机号", "1371111222", "abc123456"); err != ErrInvalidPhone {
		t.Fatalf("非 11 位手机号应返回 ErrInvalidPhone，实际 %v", err)
	}
	if acc, ok := r.Find(acc.ID); !ok || acc.Name != "张三" {
		t.Fatalf("Find 应能查到新账号：%+v %v", acc, ok)
	}
}

// 种子账号的手机号必须唯一且都是 11 位，角色必须能映射到唯一的端。
func TestSeed_PhoneAndSide(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Seed {
		if !IsPhone(s.Phone) {
			t.Fatalf("账号 %s 的手机号 %q 非法", s.ID, s.Phone)
		}
		if seen[s.Phone] {
			t.Fatalf("手机号 %s 重复", s.Phone)
		}
		seen[s.Phone] = true
		side := model.SideOf(s.Role)
		if side == "" {
			t.Fatalf("账号 %s 的角色 %s 没有归属端", s.ID, s.Role)
		}
		want := model.SideCustomer
		if s.Role != model.RoleBuyer {
			want = model.SideBusiness
		}
		if side != want {
			t.Fatalf("账号 %s 应属于 %s，实际 %s", s.ID, want, side)
		}
	}
}
