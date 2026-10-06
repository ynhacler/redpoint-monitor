#!/usr/bin/env bash
# 本地开发：同时启动面板 + 假数据 Agent + Vite，Ctrl-C 全部停止（设计 40.1）。
#
# 用法：make dev（等同于 scripts/dev.sh）；LISTEN=0.0.0.0:8080 make dev 可让 OrbStack 虚拟机访问；
# LISTEN=127.0.0.1:9090（或只写 9090）换端口，Agent 与 Vite 代理随之改变（设计 28.1）。
set -euo pipefail
cd "$(dirname "$0")/.."

[ -f .dev/admin.password ] || make dev-init

pids=()
cleanup() {
  echo; echo "stopping..."
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# 给每行输出加上来源前缀。用 awk 并逐行 fflush：sed 在输出不是终端时按块缓冲，日志会迟迟不出现
prefix() { awk -v p="$1" '{ print p $0; fflush() }'; }

# 版本号与 make build 一致（git describe），/healthz 中可见（设计 40.3.2）
version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)

listen="${LISTEN:-127.0.0.1:8080}"
port="${listen##*:}"
api="http://127.0.0.1:$port"

# 1. 面板：开发时用易读的 text 日志格式（设计 24.3）
echo "→ server on :$port"
go run -ldflags "-X main.version=$version" ./cmd/server run --data .dev/data \
  --listen "$listen" --log-format text 2>&1 | prefix "[server] " &
pids+=($!)

for _ in $(seq 1 60); do curl -fs "$api/healthz" >/dev/null 2>&1 && break; sleep 0.5; done

# 2. 假数据 Agent，每 3 秒上报一次
echo "→ fake agent"
go run ./cmd/agent --server "$api" --token-file .dev/agent.token --fake --interval 3s 2>&1 | prefix "[agent]  " &
pids+=($!)

# 3. Vite（/api、/ws 代理到面板）
echo "→ web on http://localhost:5173  (login: admin / $(cat .dev/admin.password))"
(cd web && VPSMON_DEV_API="$api" npm run dev -- --clearScreen false) 2>&1 | prefix "[web]    " &
pids+=($!)

wait
