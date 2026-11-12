package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"small-commerce-order/internal/api"
	"small-commerce-order/internal/cache"
	"small-commerce-order/internal/model"
	"small-commerce-order/internal/service"
	"small-commerce-order/internal/store"
)

const (
	defaultDSN = "root:root@tcp(127.0.0.1:3306)/small_commerce_order?charset=utf8mb4&parseTime=true&loc=Local"
	demoSecret = "demo-channel-secret"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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

func main() {
	addr := flag.String("addr", envOr("APP_ADDR", ":8090"), "HTTP 监听地址")
	secret := flag.String("secret", envOr("APP_CHANNEL_SECRET", demoSecret), "模拟支付渠道验签密钥")
	web := flag.String("web", envOr("APP_WEB", "./web"), "前端静态目录")
	storeKind := flag.String("store", envOr("APP_STORE", "mysql"), "存储：mysql（真相源）| memory（零依赖回退）")
	dsn := flag.String("dsn", envOr("APP_MYSQL_DSN", defaultDSN), "MySQL DSN")
	migrations := flag.String("migrations", envOr("APP_MIGRATIONS", "./migrations"), "版本化建表迁移目录")
	redisAddr := flag.String("redis", envOr("APP_REDIS_ADDR", "127.0.0.1:6379"), "Redis 地址，留空则禁用缓存")
	redisPass := flag.String("redis-password", os.Getenv("APP_REDIS_PASSWORD"), "Redis 密码")
	data := flag.String("data", envOr("APP_DATA", "./data/state.json"), "memory 模式的状态快照文件")
	envMode := flag.String("env", envOr("APP_ENV", "demo"), "运行环境：demo | production")
	passwordsRaw := flag.String("passwords", os.Getenv("APP_PASSWORDS"), "生产模式账号密码，格式 用户ID:密码,...")
	passwordless := flag.Bool("passwordless", isTruthy(os.Getenv("APP_LOGIN_PASSWORDLESS")), "生产模式下免密码登录（仅演示/预览用，仍保留会话令牌与角色隔离）")
	flag.Parse()

	production := *envMode == "production"
	if *envMode != "demo" && !production {
		log.Fatalf("未知 -env 值：%s（可选 demo / production）", *envMode)
	}

	passwords := map[string]string{}
	if production {
		if *secret == "" || *secret == demoSecret {
			log.Fatal("生产模式必须通过 APP_CHANNEL_SECRET 设置独立强验签密钥")
		}
		if strings.Contains(*dsn, "root:root@") {
			log.Fatal("生产模式不得使用默认 MySQL 口令，请通过 APP_MYSQL_DSN 配置专用账号")
		}
		if *redisAddr == "" {
			log.Fatal("生产模式身份依赖 Redis 会话，请配置 APP_REDIS_ADDR")
		}
		if *passwordless {
			// 免密码登录：账号密码不再是必填项，但仍然解析（留空即可），
			// 便于随时把 APP_LOGIN_PASSWORDLESS 改回 0 恢复密码登录。
			if strings.TrimSpace(*passwordsRaw) != "" {
				p, err := parsePasswords(*passwordsRaw)
				if err != nil {
					log.Fatalf("账号密码配置错误：%v", err)
				}
				passwords = p
			}
			log.Printf("警告：APP_LOGIN_PASSWORDLESS 已开启，登录不再校验密码（仅限演示/预览环境）")
		} else {
			p, err := parsePasswords(*passwordsRaw)
			if err != nil {
				log.Fatalf("账号密码配置错误：%v", err)
			}
			for _, u := range model.Users {
				if u.Role != model.RoleChannel {
					if _, ok := p[u.ID]; !ok {
						log.Fatalf("生产模式缺少账号 %s 的密码配置", u.ID)
					}
				}
			}
			passwords = p
		}
	}

	// Redis 只做缓存 / 辅助去重 / 登录态，不可用时降级，正确性仍由 MySQL 保证。
	var c cache.Cache = cache.Noop{}
	redisState := "disabled"
	if *redisAddr != "" {
		rc, err := cache.Open(*redisAddr, *redisPass, 0)
		if err != nil {
			if production {
				log.Fatalf("生产模式 Redis 不可用，拒绝启动：%v", err)
			}
			log.Printf("警告：Redis 不可用，已降级为直连 MySQL：%v", err)
			redisState = "unavailable"
		} else {
			c = rc
			defer rc.Close()
			redisState = "connected(" + *redisAddr + ")"
		}
	}
	if production && redisState != "connected("+*redisAddr+")" {
		log.Fatal("生产模式必须连接 Redis，拒绝以降级模式启动")
	}

	var st store.StateStore
	switch *storeKind {
	case "memory":
		if production {
			log.Fatal("生产模式必须使用 mysql 真相源，不接受 memory 存储")
		}
		mem, err := store.Open(*data, time.Now)
		if err != nil {
			log.Fatalf("初始化内存存储失败：%v", err)
		}
		st = mem
	case "mysql":
		ms, err := store.OpenMySQL(store.MySQLConfig{
			DSN:            *dsn,
			MigrationsPath: *migrations,
			MaxOpenConns:   20,
			MaxIdleConns:   5,
		}, c, time.Now)
		if err != nil {
			log.Fatalf("初始化 MySQL 真相源失败：%v", err)
		}
		st = ms
	default:
		log.Fatalf("未知 -store 值：%s（可选 mysql / memory）", *storeKind)
	}
	defer st.Close()

	svc := service.New(st, *secret, nil).WithCache(c)

	// R2：后台每秒按服务端时间扫描并幂等关闭超时未付款订单。
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := svc.CloseExpiredOrders(); err != nil {
				log.Printf("超时关闭扫描失败 action=closeExpired err=%v", err)
			}
		}
	}()

	server := api.New(svc, *web, c)
	if production {
		server = server.WithSecurity(passwords)
		if *passwordless {
			server = server.WithPasswordless()
		}
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("订单交易系统已启动：http://localhost%s  env=%s store=%s redis=%s web=%s",
		*addr, *envMode, *storeKind, redisState, *web)
	log.Fatal(srv.ListenAndServe())
}
