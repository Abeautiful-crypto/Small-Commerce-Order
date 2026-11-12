#!/usr/bin/env bash
# 一键部署（在服务器上从仓库根目录执行：bash deploy/install.sh）
# 幂等：.env 只在首次生成；重复执行只会更新容器，不会重置数据或密钥。
set -euo pipefail

cd "$(dirname "$0")/.."
echo "[deploy] root=$(pwd)"

command -v docker >/dev/null 2>&1 || { echo "[deploy] ERROR: docker 未安装"; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "[deploy] ERROR: docker compose 插件未安装"; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "[deploy] ERROR: python3 未安装"; exit 1; }

if [ ! -f .env ]; then
  echo "[deploy] 首次部署：生成强随机密钥到 .env（权限 600）"
  python3 - <<'PY'
import os
import secrets

def r(n=24):
    return secrets.token_hex(n)

# 演示/验收默认免密码登录；如需密码登录，先 export APP_LOGIN_PASSWORDLESS=0 再执行。
passwordless = "1" if os.environ.get("APP_LOGIN_PASSWORDLESS", "1") not in ("0", "false", "no", "off") else "0"

lines = [
    "APP_VERSION=v1.0.0",
    "MYSQL_DATABASE=small_commerce_order",
    "MYSQL_USER=appuser",
    "MYSQL_PASSWORD=" + r(),
    "MYSQL_ROOT_PASSWORD=" + r(),
    "REDIS_PASSWORD=" + r(),
    "APP_CHANNEL_SECRET=" + r(32),
    "APP_PASSWORDS=" + ",".join(
        "{}:{}".format(u, secrets.token_hex(12))
        for u in ["u001", "u002", "u003", "admin", "wh", "cs"]
    ),
    "APP_LOGIN_PASSWORDLESS=" + passwordless,
]
with open(".env", "w", encoding="utf-8") as f:
    f.write("\n".join(lines) + "\n")
PY
  chmod 600 .env
else
  echo "[deploy] 复用已有 .env（不重置密钥与数据）"
fi

echo "[deploy] 启动容器，首次构建需要拉取镜像，可能耗时数分钟"
docker compose --env-file .env up -d --build

echo "[deploy] 等待应用健康（最多 10 分钟）"
healthy=0
for i in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:8090/api/meta > /tmp/small-commerce-order-meta.json 2>/dev/null; then
    healthy=1
    echo "[deploy] 应用已就绪，等待约 $((i * 10)) 秒"
    cat /tmp/small-commerce-order-meta.json
    break
  fi
  sleep 10
done

if [ "$healthy" -ne 1 ]; then
  echo "[deploy] ERROR: 应用未在时限内就绪，容器日志如下"
  docker compose --env-file .env logs --tail=100 app || true
  exit 1
fi

echo "[deploy] 容器状态"
docker compose --env-file .env ps
if grep -q '^APP_LOGIN_PASSWORDLESS=1' .env; then
  echo "[deploy] 登录方式：免密码（选择角色即可进入；如需密码登录请把 APP_LOGIN_PASSWORDLESS 改为 0 后重启 app）"
else
  echo "[deploy] 登录密码（仅此处输出一次，请妥善保存）"
  grep '^APP_PASSWORDS=' .env
fi
echo "[deploy] DONE"
echo "[deploy] 公网访问请在 1Panel 配置反向代理到 127.0.0.1:8090 并开启 HTTPS"
