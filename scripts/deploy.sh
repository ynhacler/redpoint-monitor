#!/usr/bin/env bash
# 通过 SSH 把二进制与 systemd 单元部署到 Linux VPS（不使用 Docker）。
# 仅限开发自测，不属于产品功能，不出现在用户文档中（设计 27、40.3.2）。
#
# 用法：
#   scripts/deploy.sh server user@host
#   scripts/deploy.sh agent  user@host https://monitor.example.com ./HK-1.token
#
# 需要 VPS 上的 sudo 权限。先运行 make build-linux（make deploy 会自动执行）。
set -euo pipefail
cd "$(dirname "$0")/.."

role=${1:?role: server|agent}
vps=${2:?user@host}

# 1. 识别远端架构，映射到构建名（设计 27.5.4）；面板只提供 amd64 / arm64 构建
arch=$(ssh "$vps" uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  armv7*|armhf|armv8l) arch=armv7 ;;
  armv6*) arch=armv6 ;;
  i386|i486|i586|i686|x86) arch=386 ;;
  riscv64) arch=riscv64 ;;
  *) echo "unsupported arch: $arch"; exit 1 ;;
esac
if [ "$role" = server ] && [ "$arch" != amd64 ] && [ "$arch" != arm64 ]; then
  echo "server is only built for amd64 / arm64 (got $arch)"; exit 1
fi
echo "→ $vps is linux/$arch"

# 【安全】远端脚本以 root 执行。heredoc 使用 <<'EOF' 原样发送，本地变量不会被拼进脚本；
# 参数通过 bash -s 的位置参数传入。ssh 会把参数拼成远端命令行，因此先在本地校验格式，
# 防止含 shell 特殊字符的参数在远端被解释执行。
case "$role" in
server)
  # 2. 上传二进制与单元文件，远端以 root 安装并启动
  scp -q "dist/vpsmon-server-linux-$arch" deploy/systemd/vpsmon-server.service "$vps":/tmp/
  ssh "$vps" sudo bash -s -- "$arch" <<'EOF'
set -e
arch=$1
id vpsmon >/dev/null 2>&1 || useradd --system --home /var/lib/vpsmon --shell /usr/sbin/nologin vpsmon
install -d -o vpsmon -g vpsmon -m 0750 /var/lib/vpsmon
install -m 0755 "/tmp/vpsmon-server-linux-$arch" /usr/local/bin/vpsmon-server
install -m 0644 /tmp/vpsmon-server.service /etc/systemd/system/vpsmon-server.service
if [ ! -f /var/lib/vpsmon/monitor.db ]; then
  echo "first deploy — admin login (username admin; password shown once, change it at first login):"
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
  # 2. 面板地址只允许 URL 字符，原因见上方【安全】说明
  if ! [[ "$server" =~ ^https?://[]A-Za-z0-9.:/[_-]+$ ]]; then
    echo "invalid server URL: $server"; exit 1
  fi
  # 3. 上传二进制、单元文件与 Token，远端以 root 安装并启动
  scp -q "dist/vpsmon-agent-linux-$arch" deploy/systemd/vpsmon-agent.service "$vps":/tmp/
  scp -q "$tokfile" "$vps":/tmp/vpsmon-agent.token
  ssh "$vps" sudo bash -s -- "$arch" "$server" <<'EOF'
set -e
arch=$1 server=$2
id vpsmon-agent >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin vpsmon-agent
install -d -m 0750 -g vpsmon-agent /etc/vpsmon-agent
# 【安全】Token 仅 vpsmon-agent 组可读，复制后立即删除临时文件（设计 27.10）
install -m 0640 -g vpsmon-agent /tmp/vpsmon-agent.token /etc/vpsmon-agent/token && rm -f /tmp/vpsmon-agent.token
echo "VPSMON_SERVER=$server" > /etc/vpsmon-agent/env
install -m 0755 "/tmp/vpsmon-agent-linux-$arch" /usr/local/bin/vpsmon-agent
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
