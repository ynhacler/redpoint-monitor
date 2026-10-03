#!/usr/bin/env bash
# Agent 的 systemd 存活检测（设计 43.5）：以 Type=notify + 短 WatchdogSec 运行 Agent，
# 确认它报告就绪（否则启动超时失败）、按时喂看门狗（否则被重启，NRestarts > 0）。
# 需要 systemd 与 sudo；CI 的 ubuntu runner 上运行。
#
# 用法：scripts/agent-systemd-check.sh [agent 二进制]（默认 dist/vpsmon-agent-linux-amd64）
set -euo pipefail

src=${1:-dist/vpsmon-agent-linux-amd64}
unit=vpsmon-agent-check-$$
state=$(mktemp -d)
# 以 nobody 运行：二进制放到 /tmp 下（runner 的主目录对其他用户不可访问），状态目录可写
bindir=$(mktemp -d)
cp "$src" "$bindir/vpsmon-agent"
chmod 0755 "$bindir" "$bindir/vpsmon-agent"
chmod 0777 "$state"
bin=$bindir/vpsmon-agent
trap 'sudo systemctl stop "$unit" 2>/dev/null || true; sudo rm -rf "$state" "$bindir"' EXIT

# 面板指向不监听的本机端口：上报失败、退避重试期间也必须按时喂看门狗。
# 【安全】占位 Token，不对应任何面板；以 nobody 运行，与正式单元一样不需要 root。
sudo systemd-run --unit="$unit" --quiet \
  -p Type=notify -p NotifyAccess=main -p WatchdogSec=5 -p Restart=always -p RestartSec=1 \
  -p User=nobody -p Environment=MONITOR_AGENT_TOKEN=agt_systemd_check \
  "$bin" --server http://127.0.0.1:9 --interval 2s --state-dir "$state"

sleep 20
active=$(systemctl is-active "$unit" || true)
restarts=$(systemctl show -p NRestarts --value "$unit")
echo "状态 ${active}，重启次数 ${restarts}（WatchdogSec=5，运行 20 秒）"
journalctl -u "$unit" --no-pager -n 5 2>/dev/null || true

[ "$active" = active ] || { echo "Agent 未处于运行状态：Type=notify 启动后应报告 READY=1" >&2; exit 1; }
[ "$restarts" = 0 ] || { echo "Agent 被重启过：主循环应每轮喂看门狗" >&2; exit 1; }
