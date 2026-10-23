#!/usr/bin/env bash
# 面板一键安装脚本的端到端检查（设计 25.1，修订第 208 条）：在一台装有 systemd 的 Linux（如 CI 的 ubuntu runner）上
# 用本地构建安装（--local --proxy，不需要域名与公网），检查：结果中有地址、账号与密码；服务运行且开机自启；
# 被 kill -9 后自动拉起（保活）；重新运行为升级且密码不变；reset-password；uninstall --purge 清理干净。
# 需要 root 或免密 sudo；会在本机安装并在结束时卸载面板，不要在正式使用的面板主机上运行。
#
# 用法：make build-linux VERSION=v0.0.0-check && go run ./cmd/vpsmon-release prepare --version 0.0.0 --dist dist
#       scripts/panel-install-check.sh dist
set -euo pipefail

dist=${1:-dist}
port=18080
inst="$dist/vpsmon-install.sh"
test -f "$inst" || { echo "missing $inst (run vpsmon-release prepare first)"; exit 1; }
SUDO=""; [ "$(id -u)" -eq 0 ] || SUDO=sudo
fail() { echo "✗ $*"; $SUDO journalctl -u vpsmon-server -n 30 --no-pager || true; exit 1; }
cleanup() { $SUDO sh "$inst" uninstall --purge >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== 1. 安装（反向代理方式，本地文件）"
out=$($SUDO sh "$inst" --local "$dist" --proxy --port "$port" --yes 2>&1) || { echo "$out"; fail "安装失败"; }
echo "$out" | grep -v '密码：'   # CI 日志中不打印密码
pw=$(echo "$out" | sed 's/\x1b\[[0-9;]*m//g' | sed -n 's/^ 密码：[[:space:]]*\([A-Za-z0-9]\{8,\}\)$/\1/p' | head -n 1)
[ -n "$pw" ] || fail "结果中没有初始密码"
echo "$out" | grep -q "访问地址" || fail "结果中没有访问地址"
echo "$out" | grep -q "账号：.*admin" || fail "结果中没有账号"
systemctl is-active --quiet vpsmon-server || fail "服务没有运行"
systemctl is-enabled --quiet vpsmon-server || fail "服务没有设为开机自启"
curl -fsS "http://127.0.0.1:$port/api/v1/site" >/dev/null || fail "面板没有响应"
[ "$($SUDO stat -c %U /var/lib/vpsmon/monitor.db)" = vpsmon ] || fail "数据库不归 vpsmon 所有"
[ "$(ps -o user= -p "$(systemctl show -p MainPID --value vpsmon-server)")" = vpsmon ] || fail "面板不是以 vpsmon 用户运行"
echo "✓ 安装、开机自启、非 root 运行"

echo "== 2. 保活：kill -9 后自动拉起"
before=$(systemctl show -p NRestarts --value vpsmon-server)
$SUDO systemctl kill -s KILL vpsmon-server
for _ in $(seq 1 20); do
  sleep 1
  [ "$(systemctl show -p NRestarts --value vpsmon-server)" -gt "$before" ] && systemctl is-active --quiet vpsmon-server && break
done
[ "$(systemctl show -p NRestarts --value vpsmon-server)" -gt "$before" ] || fail "进程被杀后没有自动重启"
for _ in $(seq 1 10); do curl -fsS "http://127.0.0.1:$port/api/v1/site" >/dev/null && break; sleep 1; done
curl -fsS "http://127.0.0.1:$port/api/v1/site" >/dev/null || fail "重启后面板没有响应"
echo "✓ 自动重启"

echo "== 3. 再次运行：升级，数据与密码保留"
out=$($SUDO sh "$inst" --local "$dist" --yes 2>&1) || { echo "$out"; fail "升级失败"; }
echo "$out" | grep -q "已升级" || fail "第二次运行应为升级"
echo "$out" | grep -q "沿用原来的密码" || fail "升级不应重置密码"
grep -q "^VPSMON_LISTEN=127.0.0.1:$port$" /etc/vpsmon/server.env || fail "升级改动了监听端口"
curl -fsS "http://127.0.0.1:$port/api/v1/site" >/dev/null || fail "升级后面板没有响应"
echo "✓ 升级"

echo "== 4. 重置密码"
out=$($SUDO sh "$inst" reset-password 2>&1) || { echo "$out"; fail "重置密码失败"; }
pw2=$(echo "$out" | sed 's/\x1b\[[0-9;]*m//g' | sed -n 's/^  密码：\([A-Za-z0-9]\{8,\}\)$/\1/p')
[ -n "$pw2" ] && [ "$pw2" != "$pw" ] || fail "没有得到新的密码"
[ -z "$($SUDO find /var/lib/vpsmon -maxdepth 1 -user root)" ] || fail "数据目录中有 root 所有的文件"
echo "✓ 重置密码"

echo "== 5. 卸载"
$SUDO sh "$inst" uninstall --purge >/dev/null
! systemctl is-active --quiet vpsmon-server || fail "卸载后服务仍在运行"
[ ! -e /usr/local/bin/vpsmon-server ] && [ ! -e /var/lib/vpsmon ] || fail "卸载后有残留"
! id vpsmon >/dev/null 2>&1 || fail "卸载后 vpsmon 用户仍在"
trap - EXIT
echo "✓ 面板一键安装检查全部通过"
