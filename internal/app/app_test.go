package app

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"small-commerce-order/internal/cache"
)

// fakeCache 测试用缓存桩，记录失效调用。
type fakeCache struct{ invalidated []string }

func (f *fakeCache) Stock(string) (int, bool)                  { return 0, false }
func (f *fakeCache) SetStock(string, int)                      {}
func (f *fakeCache) InvalidateStock(ids ...string)             { f.invalidated = append(f.invalidated, ids...) }
func (f *fakeCache) PaymentSeen(string) bool                   { return false }
func (f *fakeCache) MarkPayment(string)                        {}
func (f *fakeCache) SaveSession(string, string, string) error  { return nil }
func (f *fakeCache) LoadSession(string) (string, string, bool) { return "", "", false }
func (f *fakeCache) Ping() error                               { return nil }

func getenv(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func cloneEnv(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func fakeRedisOpener(c cache.Cache) redisOpener {
	return func(addr, pass string) (cache.Cache, io.Closer, error) {
		if addr == "fail:1" {
			return nil, nil, errors.New("redis down")
		}
		return c, io.NopCloser(nil), nil
	}
}

func TestLoadConfig_DemoDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig([]string{
		"-store", "memory",
		"-data", filepath.Join(dir, "state.json"),
		"-redis", "",
	}, getenv(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != "demo" || cfg.StoreKind != "memory" || cfg.Addr != ":8090" || cfg.Web != "./web" {
		t.Fatalf("demo 默认值不符：%+v", cfg)
	}
	if cfg.RedisAddr != "" || cfg.Passwordless {
		t.Fatalf("demo 默认应禁用 Redis 且关闭免密码：%+v", cfg)
	}
}

func TestLoadConfig_UnknownEnv(t *testing.T) {
	if _, err := LoadConfig([]string{"-env", "staging"}, getenv(nil)); err == nil || !strings.Contains(err.Error(), "未知 -env") {
		t.Fatalf("未知 env 应报错，实际 %v", err)
	}
}

func TestLoadConfig_BadFlag(t *testing.T) {
	if _, err := LoadConfig([]string{"-no-such-flag"}, getenv(nil)); err == nil {
		t.Fatal("非法 flag 应报错")
	}
}

// 生产模式安全护栏逐条验证。
func TestLoadConfig_ProductionGuards(t *testing.T) {
	base := map[string]string{
		"APP_ENV":            "production",
		"APP_CHANNEL_SECRET": "strong-secret",
		"APP_MYSQL_DSN":      "app:s3cret@tcp(127.0.0.1:3306)/db?parseTime=true",
		"APP_REDIS_ADDR":     "127.0.0.1:6379",
	}
	fullPwd := "u001:p1,u002:p2,u003:p3,admin:p4,wh:p5,cs:p6"

	// 全部护栏通过：密码解析成功
	env := cloneEnv(base)
	env["APP_PASSWORDS"] = fullPwd
	cfg, err := LoadConfig(nil, getenv(env))
	if err != nil {
		t.Fatalf("合法生产配置不应报错：%v", err)
	}
	if cfg.Passwords["u001"] != "p1" {
		t.Fatalf("密码应解析为 map，实际 %+v", cfg.Passwords)
	}

	// 缺独立验签密钥
	env2 := cloneEnv(env)
	env2["APP_CHANNEL_SECRET"] = ""
	if _, err := LoadConfig(nil, getenv(env2)); err == nil || !strings.Contains(err.Error(), "验签密钥") {
		t.Fatalf("缺强密钥应报错，实际 %v", err)
	}
	// 使用演示默认密钥
	env3 := cloneEnv(env)
	env3["APP_CHANNEL_SECRET"] = "demo-channel-secret"
	if _, err := LoadConfig(nil, getenv(env3)); err == nil {
		t.Fatal("演示默认密钥在生产模式应被拒绝")
	}
	// 默认 MySQL 口令
	env4 := cloneEnv(env)
	env4["APP_MYSQL_DSN"] = "root:root@tcp(127.0.0.1:3306)/db?parseTime=true"
	if _, err := LoadConfig(nil, getenv(env4)); err == nil || !strings.Contains(err.Error(), "默认 MySQL 口令") {
		t.Fatalf("默认口令应报错，实际 %v", err)
	}
	// Redis 未配置（必须用 -redis "" 显式禁用；置空环境变量会回退默认值）
	env5 := cloneEnv(env)
	args5 := []string{"-redis", ""}
	env5["APP_REDIS_ADDR"] = ""
	if _, err := LoadConfig(args5, getenv(env5)); err == nil || !strings.Contains(err.Error(), "Redis") {
		t.Fatalf("生产缺 Redis 应报错，实际 %v", err)
	}
	// 密码缺账号
	env6 := cloneEnv(env)
	env6["APP_PASSWORDS"] = "u001:p1"
	if _, err := LoadConfig(nil, getenv(env6)); err == nil || !strings.Contains(err.Error(), "缺少账号") {
		t.Fatalf("缺账号密码应报错，实际 %v", err)
	}
	// 密码格式错误
	env7 := cloneEnv(env)
	env7["APP_PASSWORDS"] = "u001,p2"
	if _, err := LoadConfig(nil, getenv(env7)); err == nil || !strings.Contains(err.Error(), "APP_PASSWORDS") {
		t.Fatalf("密码格式错误应报错，实际 %v", err)
	}
	// 渠道账号允许多余密码键，不报错
	env8 := cloneEnv(env)
	env8["APP_PASSWORDS"] = fullPwd + ",ch01:x"
	if _, err := LoadConfig(nil, getenv(env8)); err != nil {
		t.Fatalf("渠道账号多余密码不应报错：%v", err)
	}
}

// 免密码登录：无需 APP_PASSWORDS 也可通过校验，但密码格式错误仍会被发现。
func TestLoadConfig_PasswordlessProduction(t *testing.T) {
	env := map[string]string{
		"APP_ENV":                "production",
		"APP_CHANNEL_SECRET":     "strong-secret",
		"APP_MYSQL_DSN":          "app:s3cret@tcp(127.0.0.1:3306)/db?parseTime=true",
		"APP_REDIS_ADDR":         "127.0.0.1:6379",
		"APP_LOGIN_PASSWORDLESS": "1",
	}
	cfg, err := LoadConfig(nil, getenv(env))
	if err != nil {
		t.Fatalf("免密码生产配置应通过：%v", err)
	}
	if !cfg.Passwordless || cfg.Passwords != nil {
		t.Fatalf("免密码且未给密码串时 Passwords 应为空：%+v", cfg)
	}

	env["APP_PASSWORDS"] = "u001:,"
	env["APP_LOGIN_PASSWORDLESS"] = "true"
	if _, err := LoadConfig(nil, getenv(env)); err == nil {
		t.Fatal("免密码模式下密码串格式错误仍应报错")
	}
}

func TestIsTruthy(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", " yes ", "on"} {
		if !isTruthy(v) {
			t.Fatalf("%q 应为 true", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off"} {
		if isTruthy(v) {
			t.Fatalf("%q 应为 false", v)
		}
	}
}

func TestParsePasswords(t *testing.T) {
	m, err := parsePasswords("u001:a,u002:b , ,u003:c")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 || m["u002"] != "b" {
		t.Fatalf("解析结果不符：%+v", m)
	}
	if _, err := parsePasswords("u001"); err == nil {
		t.Fatal("缺冒号应报错")
	}
	if _, err := parsePasswords(":pwd"); err == nil {
		t.Fatal("空用户应报错")
	}
	if _, err := parsePasswords("u001:"); err == nil {
		t.Fatal("空密码应报错")
	}
}

func TestBuild_MemoryWithoutRedis(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig([]string{"-store", "memory", "-data", filepath.Join(dir, "s.json"), "-redis", ""}, getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	stack, err := Build(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	if stack.RedisState != "disabled" {
		t.Fatalf("Redis 留空应显示 disabled，实际 %s", stack.RedisState)
	}
	if stack.Server == nil || stack.Service == nil || stack.Store == nil {
		t.Fatal("装配结果不完整")
	}
}

// 演示模式下 Redis 不可用：降级为 Noop 而不报错。
func TestBuild_DemoRedisDegraded(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig([]string{"-store", "memory", "-data", filepath.Join(dir, "s.json"), "-redis", "fail:1"}, getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	stack, err := Build(cfg, fakeRedisOpener(&fakeCache{}))
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	if stack.RedisState != "unavailable" {
		t.Fatalf("演示模式 Redis 故障应降级，实际 %s", stack.RedisState)
	}
}

// 演示模式 + 可用 Redis：装配成功。
func TestBuild_DemoRedisConnected(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig([]string{"-store", "memory", "-data", filepath.Join(dir, "s.json"), "-redis", "ok:1"}, getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	stack, err := Build(cfg, fakeRedisOpener(&fakeCache{}))
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	if stack.RedisState != "connected(ok:1)" {
		t.Fatalf("应显示 connected，实际 %s", stack.RedisState)
	}
}

// 生产模式 + memory 存储：直接拒绝。
func TestBuild_ProductionRejectsMemory(t *testing.T) {
	dir := t.TempDir()
	args := []string{"-store", "memory", "-data", filepath.Join(dir, "s.json"), "-env", "production",
		"-secret", "strong-secret", "-dsn", "app:s3cret@tcp(127.0.0.1:3306)/db?parseTime=true",
		"-redis", "ok:1", "-passwordless"}
	cfg, err := LoadConfig(args, getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, fakeRedisOpener(&fakeCache{})); err == nil || !strings.Contains(err.Error(), "mysql 真相源") {
		t.Fatalf("生产+memory 应拒绝，实际 %v", err)
	}
}

// 生产模式 + Redis 故障：拒绝启动。
func TestBuild_ProductionRedisRequired(t *testing.T) {
	args := []string{"-env", "production", "-secret", "strong-secret",
		"-dsn", "app:s3cret@tcp(127.0.0.1:3306)/db?parseTime=true",
		"-redis", "fail:1", "-passwordless"}
	cfg, err := LoadConfig(args, getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, fakeRedisOpener(&fakeCache{})); err == nil || !strings.Contains(err.Error(), "Redis 不可用") {
		t.Fatalf("生产 Redis 故障应拒绝，实际 %v", err)
	}
}

// 未知存储类型报错。
func TestBuild_UnknownStore(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig([]string{"-store", "sqlite", "-data", filepath.Join(dir, "s.json"), "-redis", ""}, getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, nil); err == nil || !strings.Contains(err.Error(), "未知 -store") {
		t.Fatalf("未知存储应报错，实际 %v", err)
	}
}

// MySQL 不可达：装配报错（快速超时 DSN）。
func TestBuild_MySQLUnreachable(t *testing.T) {
	cfg, err := LoadConfig([]string{
		"-store", "mysql",
		"-dsn", "root:root@tcp(127.0.0.1:1)/db?timeout=300ms&readTimeout=300ms&writeTimeout=300ms&parseTime=true",
		"-migrations", "./migrations",
		"-redis", "",
	}, getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, nil); err == nil || !strings.Contains(err.Error(), "MySQL") {
		t.Fatalf("MySQL 不可达应报错，实际 %v", err)
	}
}

// 内存存储初始化失败：数据路径是目录时无法写入状态文件。
func TestBuild_MemoryOpenFails(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig([]string{"-store", "memory", "-data", dir, "-redis", ""}, getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, nil); err == nil {
		t.Fatal("data 指向目录时应报错")
	}
}
