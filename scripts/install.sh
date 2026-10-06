#!/usr/bin/env bash
# 把本机构建的二进制（bin/，由 make build 生成）安装为 systemd 服务。
# 是 deploy.sh 在 VPS 本机上的对应版本，用于在 VPS 上从源码构建的场景。
# 仅限开发自测（设计 27、40.4.2）；正式安装走注册码流程（设计 27.3）。
#
# 用法：
#   git pull && make build && sudo scripts/install.sh server        （指定端口：sudo PORT=9090 scripts/install.sh server）
#   sudo scripts/install.sh agent https://monitor.example.com ./jp-store.token
#
# 重复执行即升级二进制，保留数据与 Token。
set -euo pipefail
cd "$(dirname "$0")/.."

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)"; exit 1; }
role=${1:?role: server|agent}

case "$role" in
server)
  test -x bin/vpsmon-server || { echo "bin/vpsmon-server missing; run 'make build' first"; exit 1; }
  # 1. 创建专用的非 root 用户；systemd 单元另有沙箱限制（设计 1.6.9、28.1）
  id vpsmon >/dev/null 2>&1 || useradd --system --home /var/lib/vpsmon --shell /usr/sbin/nologin vpsmon
  install -d -o vpsmon -g vpsmon -m 0750 /var/lib/vpsmon
  # 2. 安装二进制与 systemd 单元
  install -m 0755 bin/vpsmon-server /usr/local/bin/vpsmon-server
  install -m 0644 deploy/systemd/vpsmon-server.service /etc/systemd/system/vpsmon-server.service
  # 3. 端口等参数在 /etc/vpsmon/server.env（设计 28.1）：首次安装时创建，之后只在指定 PORT 时修改监听地址
  case "${PORT:-}" in
    '') ;;
    *[!0-9]*) echo "PORT must be a number (1-65535)"; exit 1 ;;
    *) [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || { echo "PORT must be 1-65535"; exit 1; } ;;
  esac
  install -d -m 0755 /etc/vpsmon
  if [ ! -f /etc/vpsmon/server.env ]; then
    printf '# vpsmon-server 参数（VPSMON_<参数名>），修改后 systemctl restart vpsmon-server\nVPSMON_LISTEN=127.0.0.1:%s\n' "${PORT:-8080}" > /etc/vpsmon/server.env
    chmod 0644 /etc/vpsmon/server.env
  elif [ -n "${PORT:-}" ]; then
    if grep -q '^VPSMON_LISTEN=' /etc/vpsmon/server.env; then
      sed -i "s|^VPSMON_LISTEN=.*|VPSMON_LISTEN=127.0.0.1:${PORT}|" /etc/vpsmon/server.env
    else
      echo "VPSMON_LISTEN=127.0.0.1:${PORT}" >> /etc/vpsmon/server.env
    fi
  fi
  if [ ! -f /var/lib/vpsmon/monitor.db ]; then
    echo "first install — admin login (username admin; password shown once, change it at first login):"
    # 4. 首次安装时初始化数据库；以 vpsmon 身份执行，数据库文件归服务用户所有
    sudo -u vpsmon /usr/local/bin/vpsmon-server init --data /var/lib/vpsmon
  fi
  # 5. 启用并重启服务
  systemctl daemon-reload
  systemctl enable vpsmon-server >/dev/null
  systemctl restart vpsmon-server
  sleep 1
  systemctl --no-pager --lines=3 status vpsmon-server | head -n 5
  # 从开发 token 版本升级后没有管理员账号：提示创建（设计 17.2）
  if journalctl -u vpsmon-server -n 30 --no-pager 2>/dev/null | grep -q "no admin account"; then
    echo
    echo "No admin account yet (upgraded from the dev-token version). Create one:"
    echo "  sudo -u vpsmon vpsmon-server admin reset-password --data /var/lib/vpsmon"
  fi
  echo
  echo "Listening on: $(grep '^VPSMON_LISTEN=' /etc/vpsmon/server.env | cut -d= -f2-)  (change: edit /etc/vpsmon/server.env or make install-server PORT=…)"
  echo "Add a node:  sudo -u vpsmon vpsmon-server add-server --data /var/lib/vpsmon --name NAME > NAME.token"
  ;;

agent)
  server=${2:?server URL}
  tokfile=${3:?token file}
  test -x bin/vpsmon-agent || { echo "bin/vpsmon-agent missing; run 'make build' first"; exit 1; }
  # 1. 创建专用的非 root 用户（设计 1.6.9）
  id vpsmon-agent >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin vpsmon-agent
  install -d -m 0750 -g vpsmon-agent /etc/vpsmon-agent
  # 2. 【安全】Token 只允许 vpsmon-agent 组读取；不通过命令行传递，否则会出现在 ps 中（设计 27.1）
  install -m 0640 -g vpsmon-agent "$tokfile" /etc/vpsmon-agent/token
  echo "VPSMON_SERVER=$server" > /etc/vpsmon-agent/env
  # 3. 安装二进制与 systemd 单元，启用并重启服务
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
