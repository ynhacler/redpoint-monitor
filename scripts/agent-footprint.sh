#!/usr/bin/env bash
# Agent 资源占用检查（设计 4.2）：在 Linux 上用真实采集器运行 Agent 一段时间，报告常驻内存、CPU 时间与每轮采集开销，
# 常驻内存超过目标上限时失败。CI 的 ubuntu runner 上运行；也可在 test-agent 上手动运行。
#
# 用法：scripts/agent-footprint.sh [agent 二进制]（默认按 uname -m 选择 dist/ 下对应架构的构建）
# 环境变量：SECONDS_TO_RUN（默认 60）、INTERVAL_S（采集间隔秒数，默认 1，比默认的 10 秒更频繁以放大采集开销）、
#           MAX_RSS_KB（默认 30720）
#
# 面板地址指向本机一个不监听的端口：上报全部失败，同时覆盖退避重试与断网缓冲落盘的路径。
set -euo pipefail

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  armv7l | armv8l) arch=armv7 ;;
  armv6l) arch=armv6 ;;
  i?86) arch=386 ;;
  riscv64) arch=riscv64 ;;
  *) arch=unknown ;;
esac
bin=${1:-dist/vpsmon-agent-linux-$arch}
run=${SECONDS_TO_RUN:-60}
interval_s=${INTERVAL_S:-1}
max_rss=${MAX_RSS_KB:-30720}

[ "$(uname -s)" = Linux ] || { echo "需要 Linux：真实采集器只在 Linux 上运行" >&2; exit 1; }
[ -x "$bin" ] || { echo "找不到 Agent 二进制：$bin（先运行 make build-linux）" >&2; exit 1; }

state=$(mktemp -d)
trap 'kill "$pid" 2>/dev/null || true; rm -rf "$state"' EXIT

# 【安全】只是本机测试用的占位 Token，不对应任何面板
MONITOR_AGENT_TOKEN=agt_footprint_check "$bin" --server http://127.0.0.1:9 --interval "${interval_s}s" \
  --state-dir "$state" >"$state/agent.log" 2>&1 &
pid=$!

sleep "$run"
kill -0 "$pid" 2>/dev/null || { echo "Agent 提前退出："; cat "$state/agent.log"; exit 1; }

hz=$(getconf CLK_TCK)
rss=$(awk '/^VmRSS:/ {print $2}' "/proc/$pid/status")
hwm=$(awk '/^VmHWM:/ {print $2}' "/proc/$pid/status")
threads=$(awk '/^Threads:/ {print $2}' "/proc/$pid/status")
# /proc/PID/stat 第 14、15 列为用户态、内核态 CPU 时间（时钟滴答）；进程名可能含空格，从最后一个 “)” 之后取字段
read -r utime stime < <(sed 's/.*) //' "/proc/$pid/stat" | awk '{print $12, $13}')
cpu_ms=$(( (utime + stime) * 1000 / hz ))
cycles=$(( run / interval_s ))
[ "$cycles" -gt 0 ] || cycles=1

kill -TERM "$pid"
wait "$pid" 2>/dev/null || true

echo "运行 ${run}s，采集间隔 ${interval_s}s（约 ${cycles} 轮）"
echo "常驻内存 VmRSS   ${rss} KB（峰值 ${hwm} KB，上限 ${max_rss} KB）"
echo "线程数           ${threads}"
echo "CPU 时间         ${cpu_ms} ms（平均每轮 $(( cpu_ms / cycles )) ms；按默认 10s 间隔折算约 $(awk -v c="$cpu_ms" -v r="$run" -v n="$cycles" 'BEGIN{printf "%.3f", c/n/10000*100}')% CPU）"
echo "断网缓冲落盘     $( [ -f "$state/queue.json" ] && echo "queue.json $(stat -c %s "$state/queue.json") 字节，权限 $(stat -c %a "$state/queue.json")" || echo 无)"

[ -f "$state/queue.json" ] || { echo "退出时应把未送达的上报落盘（设计 1.6.14）" >&2; exit 1; }
[ "$rss" -le "$max_rss" ] || { echo "常驻内存超过设计 4.2 的目标" >&2; exit 1; }
