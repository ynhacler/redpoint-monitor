#!/usr/bin/env bash
# Install binaries built on this machine (bin/, from `make build`) as systemd services.
# The on-VPS counterpart of deploy.sh, for a checkout that builds from source:
#
#   git pull && make build && sudo scripts/install.sh server
#   sudo scripts/install.sh agent https://monitor.example.com ./jp-store.token
#
# Re-running upgrades the binary and keeps data and tokens.
set -euo pipefail
cd "$(dirname "$0")/.."

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)"; exit 1; }
role=${1:?role: server|agent}

case "$role" in
server)
  test -x bin/vpsmon-server || { echo "bin/vpsmon-server missing; run 'make build' first"; exit 1; }
  # Dedicated non-root user; the unit also sandboxes it (design 1.6.9).
  id vpsmon >/dev/null 2>&1 || useradd --system --home /var/lib/vpsmon --shell /usr/sbin/nologin vpsmon
  install -d -o vpsmon -g vpsmon -m 0750 /var/lib/vpsmon
  install -m 0755 bin/vpsmon-server /usr/local/bin/vpsmon-server
  install -m 0644 deploy/systemd/vpsmon-server.service /etc/systemd/system/vpsmon-server.service
  if [ ! -f /var/lib/vpsmon/monitor.db ]; then
    echo "first install — admin token (save it, shown once):"
    # Run as vpsmon so the DB files are owned by the service user.
    sudo -u vpsmon /usr/local/bin/vpsmon-server init --data /var/lib/vpsmon
  fi
  systemctl daemon-reload
  systemctl enable vpsmon-server >/dev/null
  systemctl restart vpsmon-server
  sleep 1
  systemctl --no-pager --lines=3 status vpsmon-server | head -n 5
  echo
  echo "Add a node:  sudo -u vpsmon vpsmon-server add-server --data /var/lib/vpsmon --name NAME > NAME.token"
  ;;

agent)
  server=${2:?server URL}
  tokfile=${3:?token file}
  test -x bin/vpsmon-agent || { echo "bin/vpsmon-agent missing; run 'make build' first"; exit 1; }
  id vpsmon-agent >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin vpsmon-agent
  install -d -m 0750 -g vpsmon-agent /etc/vpsmon-agent
  # Token readable by the agent's group only; never passed on the command line (visible in ps).
  install -m 0640 -g vpsmon-agent "$tokfile" /etc/vpsmon-agent/token
  echo "VPSMON_SERVER=$server" > /etc/vpsmon-agent/env
  install -m 0755 bin/vpsmon-agent /usr/local/bin/vpsmon-agent
  install -m 0644 deploy/systemd/vpsmon-agent.service /etc/systemd/system/vpsmon-agent.service
  systemctl daemon-reload
  systemctl enable vpsmon-agent >/dev/null
  systemctl restart vpsmon-agent
  sleep 1
  systemctl --no-pager --lines=3 status vpsmon-agent | head -n 5
  ;;

*)
  echo "unknown role: $role"; exit 1 ;;
esac
