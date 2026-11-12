// 启动入口：配置解析、安全护栏与装配逻辑在 internal/app，可单元测试。
package main

import (
	"log"
	"os"

	"small-commerce-order/internal/app"
)

func main() {
	cfg, err := app.LoadConfig(os.Args[1:], os.Getenv)
	if err != nil {
		log.Fatalf("配置错误：%v", err)
	}
	if err := app.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
