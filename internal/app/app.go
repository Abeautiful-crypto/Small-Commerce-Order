// Package app 负责命令行/环境变量配置解析、生产模式安全护栏与运行时装配，
// 使 main.go 保持薄启动层，全部装配逻辑都可单元测试。
package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"small-commerce-order/internal/account"
	"small-commerce-order/internal/api"
	"small-commerce-order/internal/cache"
	"small-commerce-order/internal/model"
	"small-commerce-order/internal/service"
	"small-commerce-order/internal/store"
)

const (
	// DefaultDSN 本地开发默认 MySQL 连接串；生产模式显式禁止使用。
	DefaultDSN = "root:root@tcp(127.0.0.1:3306)/small_commerce_order?charset=utf8mb4&parseTime=true&loc=Local"
	// demoSecret 演示模式默认验签密钥；生产模式必须替换。
	demoSecret = "demo-channel-secret"
)

// Config 是进程启动所需的全部配置。
type Config struct {
	Addr         string // C 端（买家页）监听地址
	AddrB        string // B 端（工作台）监听地址，与 C 端分端口独立部署
	Secret       string
	Web          string
	StoreKind    string
	DSN          string
	Migrations   string
	RedisAddr    string
	RedisPass    string
	DataPath     string
	Env          string
	Passwordless bool
	PasswordsRaw string            // 生产模式账号密码原始串（APP_PASSWORDS）
	Passwords    map[string]string // 生产模式账号密码（解析校验后注入）
}

// LoadConfig 解析命令行参数与环境变量，并执行生产模式安全护栏校验。
// args 传命令行参数（main 传 os.Args[1:]），getenv 注入环境变量读取便于测试。
func LoadConfig(args []string, getenv func(string) string) (*Config, error) {
	get := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}
	fs := flag.NewFlagSet("small-commerce-order", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", get("APP_ADDR", ":8090"), "C 端页面（买家）监听地址")
	addrB := fs.String("addr-b", get("APP_ADDR_B", ":8091"), "B 端页面（工作台）监听地址")
	secret := fs.String("secret", get("APP_CHANNEL_SECRET", demoSecret), "模拟支付渠道验签密钥")
	web := fs.String("web", get("APP_WEB", "./web"), "前端静态目录")
	storeKind := fs.String("store", get("APP_STORE", "mysql"), "存储：mysql（真相源）| memory（零依赖回退）")
	dsn := fs.String("dsn", get("APP_MYSQL_DSN", DefaultDSN), "MySQL DSN")
	migrations := fs.String("migrations", get("APP_MIGRATIONS", "./migrations"), "版本化建表迁移目录")
	redisAddr := fs.String("redis", get("APP_REDIS_ADDR", "127.0.0.1:6379"), "Redis 地址，留空则禁用缓存")
	redisPass := fs.String("redis-password", getenv("APP_REDIS_PASSWORD"), "Redis 密码")
	data := fs.String("data", get("APP_DATA", "./data/state.json"), "memory 模式的状态快照文件")
	envMode := fs.String("env", get("APP_ENV", "demo"), "运行环境：demo | production")
	passwordsRaw := fs.String("passwords", getenv("APP_PASSWORDS"), "生产模式账号密码，格式 用户ID:密码,...")
	passwordless := fs.Bool("passwordless", isTruthy(getenv("APP_LOGIN_PASSWORDLESS")), "生产模式下免密码登录（仅演示/预览用，仍保留会话令牌与角色隔离）")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg := &Config{
		Addr: *addr, AddrB: *addrB, Secret: *secret, Web: *web, StoreKind: *storeKind,
		DSN: *dsn, Migrations: *migrations,
		RedisAddr: *redisAddr, RedisPass: *redisPass, DataPath: *data,
		Env: *envMode, Passwordless: *passwordless, PasswordsRaw: *passwordsRaw,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate 生产模式安全护栏：密钥、数据库口令、Redis 依赖与账号密码配置。
func (c *Config) validate() error {
	if c.Env != "demo" && c.Env != "production" {
		return fmt.Errorf("未知 -env 值：%s（可选 demo / production）", c.Env)
	}
	if c.Addr == "" || c.AddrB == "" {
		return errors.New("C 端与 B 端监听地址都不能为空")
	}
	if c.Addr == c.AddrB {
		return fmt.Errorf("C 端与 B 端必须使用不同端口：%s", c.Addr)
	}
	if c.Env != "production" {
		return nil
	}
	if c.Secret == "" || c.Secret == demoSecret {
		return errors.New("生产模式必须通过 APP_CHANNEL_SECRET 设置独立强验签密钥")
	}
	if strings.Contains(c.DSN, "root:root@") {
		return errors.New("生产模式不得使用默认 MySQL 口令，请通过 APP_MYSQL_DSN 配置专用账号")
	}
	if c.RedisAddr == "" {
		return errors.New("生产模式身份依赖 Redis 会话，请配置 APP_REDIS_ADDR")
	}

	// 免密码登录：账号密码不再是必填项，但仍然解析（留空即可），
	// 便于随时把 APP_LOGIN_PASSWORDLESS 改回 0 恢复密码登录。
	if c.Passwordless {
		if strings.TrimSpace(c.PasswordsRaw) != "" {
			p, err := parsePasswords(c.PasswordsRaw)
			if err != nil {
				return err
			}
			c.Passwords = p
		}
		return nil
	}

	p, err := parsePasswords(c.PasswordsRaw)
	if err != nil {
		return err
	}
	// APP_PASSWORDS 以「11 位手机号」为键（登录账号即手机号）
	for _, s := range account.Seed {
		if _, ok := p[s.Phone]; !ok {
			return fmt.Errorf("生产模式缺少手机号 %s 的密码配置", s.Phone)
		}
	}
	c.Passwords = p
	return nil
}

// isTruthy 识别常见的布尔开关写法（1/true/yes/on），其余一律视为关闭。
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// parsePasswords 解析 APP_PASSWORDS="u001:pwd1,u002:pwd2,..."，密码只从环境变量注入，不落代码/仓库。
func parsePasswords(raw string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 || kv[0] == "" || kv[1] == "" {
			return nil, fmt.Errorf("APP_PASSWORDS 应为「用户ID:密码」逗号分隔，错误片段 %q", part)
		}
		out[kv[0]] = kv[1]
	}
	return out, nil
}

// redisOpener 便于测试注入的 Redis 打开函数。
type redisOpener func(addr, pass string) (cache.Cache, io.Closer, error)

func defaultRedisOpener(addr, pass string) (cache.Cache, io.Closer, error) {
	r, err := cache.Open(addr, pass, 0)
	if err != nil {
		return nil, nil, err
	}
	return r, r, nil
}

// Stack 是装配完成的应用组件集合。
type Stack struct {
	Server     *api.Server // C 端（买家页）
	ServerB    *api.Server // B 端（工作台）
	Service    *service.Service
	Cache      cache.Cache
	Store      store.StateStore
	Accounts   account.Repo
	RedisState string // disabled / unavailable / connected(addr)

	redisCloser io.Closer
}

// Close 释放 Redis 连接等资源（存储由 Run 的 defer 关闭）。
func (s *Stack) Close() {
	if s.redisCloser != nil {
		_ = s.redisCloser.Close()
	}
}

// Build 按 Config 装配缓存、存储、服务与 API 服务端。openRedis 为 nil 时使用真实 Redis。
func Build(cfg *Config, openRedis redisOpener) (*Stack, error) {
	if openRedis == nil {
		openRedis = defaultRedisOpener
	}
	production := cfg.Env == "production"

	// Redis 只做缓存 / 辅助去重 / 登录态，不可用时降级，正确性仍由 MySQL 保证。
	var c cache.Cache = cache.Noop{}
	stack := &Stack{RedisState: "disabled"}
	if cfg.RedisAddr != "" {
		rc, closer, err := openRedis(cfg.RedisAddr, cfg.RedisPass)
		if err != nil {
			if production {
				return nil, fmt.Errorf("生产模式 Redis 不可用，拒绝启动：%w", err)
			}
			log.Printf("警告：Redis 不可用，已降级为直连 MySQL：%v", err)
			stack.RedisState = "unavailable"
		} else {
			c = rc
			stack.redisCloser = closer
			stack.RedisState = "connected(" + cfg.RedisAddr + ")"
		}
	}
	if production && stack.RedisState != "connected("+cfg.RedisAddr+")" {
		return nil, errors.New("生产模式必须连接 Redis，拒绝以降级模式启动")
	}

	var st store.StateStore
	var accounts account.Repo
	switch cfg.StoreKind {
	case "memory":
		if production {
			return nil, errors.New("生产模式必须使用 mysql 真相源，不接受 memory 存储")
		}
		mem, err := store.Open(cfg.DataPath, time.Now)
		if err != nil {
			return nil, fmt.Errorf("初始化内存存储失败：%w", err)
		}
		st = mem.WithCache(c) // memory 存储同样遵循「写提交后失效库存缓存」
		accounts = account.NewMemRepo()
	case "mysql":
		ms, err := store.OpenMySQL(store.MySQLConfig{
			DSN:            cfg.DSN,
			MigrationsPath: cfg.Migrations,
			MaxOpenConns:   20,
			MaxIdleConns:   5,
		}, c, time.Now)
		if err != nil {
			return nil, fmt.Errorf("初始化 MySQL 真相源失败：%w", err)
		}
		st = ms
		// 账号真相源：users（买家）/ staff（运营、仓库、客服）
		repo := account.NewMySQLRepo(ms.DB())
		if err := repo.EnsureSchema(time.Now()); err != nil {
			return nil, fmt.Errorf("初始化账号表失败：%w", err)
		}
		accounts = repo
	default:
		return nil, fmt.Errorf("未知 -store 值：%s（可选 mysql / memory）", cfg.StoreKind)
	}
	stack.Store = st
	stack.Cache = c
	stack.Accounts = accounts
	// 账号表是身份真相源：同步到身份目录，权限守卫按「ID + 角色」精确匹配。
	dir := make([]model.User, 0, 8)
	for _, a := range accounts.List() {
		dir = append(dir, model.User{ID: a.ID, Name: a.Name, Role: a.Role})
	}
	model.SetDirectory(dir)

	stack.Service = service.New(st, cfg.Secret, nil).WithCache(c)

	// 两个端各自一个 Server 实例：共享服务、缓存与账号表，但登录端与首页不同。
	// 聊天 WebSocket 的在线连接表也共享：买家连 C 端、客服连 B 端，消息才能互推。
	hub := api.NewChatHub()
	newServer := func(side string) *api.Server {
		srv := api.New(stack.Service, cfg.Web, c, accounts).WithSide(side).WithChatHub(hub)
		if production {
			srv = srv.WithSecurity(cfg.Passwords)
			if cfg.Passwordless {
				srv = srv.WithPasswordless()
			}
		}
		return srv
	}
	stack.Server = newServer(model.SideCustomer)
	stack.ServerB = newServer(model.SideBusiness)
	return stack, nil
}

// Run 装配并启动 HTTP 服务，含 R2 超时关单后台扫描。
func Run(cfg *Config) error {
	stack, err := Build(cfg, nil)
	if err != nil {
		return err
	}
	defer stack.Store.Close()
	defer stack.Close()

	// R2：后台每秒按服务端时间扫描并幂等关闭超时未付款订单。
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := stack.Service.CloseExpiredOrders(); err != nil {
				log.Printf("超时关闭扫描失败 action=closeExpired err=%v", err)
			}
		}
	}()

	srvC := &http.Server{
		Addr:              cfg.Addr,
		Handler:           stack.Server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	srvB := &http.Server{
		Addr:              cfg.AddrB,
		Handler:           stack.ServerB.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 2)
	go func() {
		log.Printf("C 端买家页面已启动：http://localhost%s  env=%s store=%s redis=%s web=%s",
			cfg.Addr, cfg.Env, cfg.StoreKind, stack.RedisState, cfg.Web)
		errCh <- srvC.ListenAndServe()
	}()
	go func() {
		log.Printf("B 端工作台已启动：http://localhost%s", cfg.AddrB)
		errCh <- srvB.ListenAndServe()
	}()
	return <-errCh
}
