#!/usr/bin/env bash
# Deploy to a Linux VPS over SSH (binary + systemd, no Docker).
#
#   scripts/deploy.sh server user@host
#   scripts/deploy.sh agent  user@host https://monitor.example.com ./HK-1.token
#
# Requires sudo on the VPS. Run `make build-linux` first (make deploy does it for you).
set -euo pipefail
cd "$(dirname "$0")/.."

role=${1:?role: server|agent}
vps=${2:?user@host}

arch=$(ssh "$vps" uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported arch: $arch"; exit 1 ;;
esac
echo "→ $vps is linux/$arch"

case "$role" in
server)
  bin=dist/vpsmon-server-linux-$arch
  scp -q "$bin" deploy/systemd/vpsmon-server.service "$vps":/tmp/
  ssh "$vps" "sudo bash -s" <<EOF
set -e
id vpsmon >/dev/null 2>&1 || useradd --system --home /var/lib/vpsmon --shell /usr/sbin/nologin vpsmon
install -d -o vpsmon -g vpsmon -m 0750 /var/lib/vpsmon
install -m 0755 /tmp/vpsmon-server-linux-$arch /usr/local/bin/vpsmon-server
install -m 0644 /tmp/vpsmon-server.service /etc/systemd/system/vpsmon-server.service
if [ ! -f /var/lib/vpsmon/monitor.db ]; then
  echo "first deploy — admin token (save it, shown once):"
  sudo -u vpsmon /usr/local/bin/vpsmon-server init --data /var/lib/vpsmon
fi
systemctl daemon-reload
systemctl enable --now vpsmon-server >/dev/null
systemctl restart vpsmon-server
sleep 1
systemctl --no-pager --lines=3 status vpsmon-server | head -n 5
EOF
  echo
  echo "Open the Web via SSH tunnel:  ssh -N -L 8080:127.0.0.1:8080 $vps   →  http://localhost:8080"
  echo "Add a node:                   make remote-add-server VPS=$vps NAME=HK-1"
  ;;

agent)
  server=${3:?server URL}
  tokfile=${4:?token file}
  bin=dist/vpsmon-agent-linux-$arch
  scp -q "$bin" deploy/systemd/vpsmon-agent.service "$vps":/tmp/
  scp -q "$tokfile" "$vps":/tmp/vpsmon-agent.token
  ssh "$vps" "sudo bash -s" <<EOF
set -e
id vpsmon-agent >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin vpsmon-agent
install -d -m 0750 -g vpsmon-agent /etc/vpsmon-agent
install -m 0640 -g vpsmon-agent /tmp/vpsmon-agent.token /etc/vpsmon-agent/token && rm -f /tmp/vpsmon-agent.token
echo "VPSMON_SERVER=$server" > /etc/vpsmon-agent/env
install -m 0755 /tmp/vpsmon-agent-linux-$arch /usr/local/bin/vpsmon-agent
install -m 0644 /tmp/vpsmon-agent.service /etc/systemd/system/vpsmon-agent.service
systemctl daemon-reload
systemctl enable --now vpsmon-agent >/dev/null
systemctl restart vpsmon-agent
sleep 1
systemctl --no-pager --lines=3 status vpsmon-agent | head -n 5
EOF
  ;;
*)
  echo "unknown role: $role"; exit 1 ;;
esac
