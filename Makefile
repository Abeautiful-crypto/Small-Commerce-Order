# 常用开发命令
.PHONY: test coverage gate acceptance build

# 全量测试（单元测试无需外部 MySQL/Redis）
test:
	go vet ./...
	go test ./... -race

# 生成覆盖率 profile 并执行门禁（整体与核心业务包 ≥85%）
coverage:
	go test ./... -covermode=atomic -coverprofile=coverage.out
	python scripts/coverage_gate.py coverage.out

gate:
	python scripts/coverage_gate.py coverage.out

# 五大场景一键验收（需本地或远程服务已启动）
acceptance:
	python scripts/acceptance_check.py --base http://127.0.0.1:8090

# 本地构建产物
build:
	go build -o .build/small-commerce-order .
