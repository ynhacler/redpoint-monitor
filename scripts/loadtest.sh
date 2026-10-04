#!/usr/bin/env bash
# 面板承载能力测试（开发工具）：在临时数据目录启动一个面板，创建 N 个节点，用 vpsmon-loadtest 模拟 N 个 Agent 上报。
# 不触碰 .dev 中的开发数据。
#
# 用法：scripts/loadtest.sh [节点数，默认 100] [上报间隔，默认 10s] [稳态时长，默认 60s] [每台补发份数，默认 180]
set -euo pipefail
cd "$(dirname "$0")/.."

n=${1:-100}
interval=${2:-10s}
duration=${3:-60s}
backlog=${4:-180}
port=18090
dir=$(mktemp -d)
trap 'kill "${pid:-}" 2>/dev/null || true; rm -rf "$dir"' EXIT

go build -o "$dir/server" ./cmd/server
go build -o "$dir/loadtest" ./cmd/vpsmon-loadtest

# 临时面板的管理员密码只用于本次测试，随临时目录删除
pw="loadtest-$(date +%s)-Pw"
VPSMON_INIT_PASSWORD=$pw "$dir/server" init --data "$dir/data" >/dev/null
for i in $(seq 1 "$n"); do
  "$dir/server" add-server --data "$dir/data" --name "load-$i" --limit-gb 1000 --reset-day 1 2>/dev/null | tail -1
done >"$dir/tokens"

"$dir/server" run --data "$dir/data" --listen "127.0.0.1:$port" --no-login-captcha --no-release-sync \
  --log-level warn >"$dir/server.log" 2>&1 &
pid=$!
for _ in $(seq 1 40); do curl -fs "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 && break; sleep 0.25; done

LOADTEST_PASSWORD=$pw "$dir/loadtest" -server "http://127.0.0.1:$port" -tokens "$dir/tokens" \
  -interval "$interval" -duration "$duration" -backlog "$backlog" -pid "$pid"

echo "== panel log (warn+) =="
tail -5 "$dir/server.log"
du -sh "$dir/data" | awk '{print "data dir: " $1}'
