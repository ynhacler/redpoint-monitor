#!/bin/sh
# VPS Monitor 面板一键安装 v@VERSION@（设计 25.1，修订第 208 条）。
# 由 cmd/vpsmon-release 在发布时生成为 vpsmon-install.sh：@…@ 占位符换成本版本的哈希、公钥与服务文件；
# 本文件（模板）本身不能直接运行。
#
# 安装（先校验本脚本再执行，不使用 curl | sh）：
#   curl -fsSLO https://github.com/ynhacler/redpoint-monitor/releases/latest/download/vpsmon-install.sh
#   curl -fsSLO https://github.com/ynhacler/redpoint-monitor/releases/latest/download/SHA256SUMS
#   grep ' vpsmon-install.sh$' SHA256SUMS | sha256sum -c - && sudo sh vpsmon-install.sh
#
# 用法：sudo sh vpsmon-install.sh [选项]           安装；已安装时升级到本版本（数据与配置保留）
#       sudo sh vpsmon-install.sh reset-password    重置管理员密码（显示新密码，首次登录须修改）
#       sudo sh vpsmon-install.sh uninstall [--purge]   卸载（--purge 同时删除数据、配置与 vpsmon 用户）
# 选项：
#   --domain 域名        用自己的域名（须已解析到本机）；面板自动申请 Let's Encrypt 证书
#   --email 邮箱         证书到期等通知的联系邮箱（可选）
#   --proxy              已有 Nginx / Caddy 等反向代理：面板只监听本机端口，由代理提供 HTTPS
#   --port 端口          --proxy 时面板监听的本机端口（默认 8080）
#   --public-url URL     --proxy 时对外的 https 地址（写进 Agent 安装命令），如 https://monitor.example.com
#   --mirror URL         从镜像下载（如无法访问 GitHub 时）；下载的程序仍按内置 SHA256 校验
#   --local 目录         使用本地已下载的 vpsmon-server-linux-<架构>（离线安装；仍按内置 SHA256 校验）
#   -y, --yes            不提问，全部用默认值
#
# 默认（不带 --domain / --proxy）：80 与 443 端口空闲时，使用免费域名 <本机 IP>.sslip.io 并自动申请证书，
# 不需要自己的域名；端口被其他网站占用时改为 --proxy 方式，并给出反向代理配置。
#
# 支持：Debian / Ubuntu / CentOS / RHEL / Rocky / Alma / Fedora / Amazon Linux / openSUSE / Arch（systemd），
#       Alpine（OpenRC）；amd64、arm64。保活：systemd Restart=always / OpenRC supervise-daemon，开机自启。
# 只依赖 uname、awk、sed、grep、curl 或 wget、sha256sum / shasum / openssl 之一；兼容 bash、dash、busybox ash。
set -eu

VERSION="@VERSION@"
REPO_URL="https://github.com/ynhacler/redpoint-monitor/releases/download/v$VERSION"
# 官方发布公钥（minisign）；主机装有 minisign 时额外校验 SHA256SUMS 的签名
PUBKEY="@PUBKEY@"
# 本版本面板各构建的 SHA256（发布时写入；脚本本身的哈希由上面的安装命令在执行前校验）
HASHES='
@HASHES@
'

BIN=/usr/local/bin/vpsmon-server
DATA=/var/lib/vpsmon
ENV_DIR=/etc/vpsmon
ENV_FILE=/etc/vpsmon/server.env
SVC=vpsmon-server

# ---- 输出 ----
if [ -t 1 ]; then B="$(printf '\033[1m')"; G="$(printf '\033[32m')"; Y="$(printf '\033[33m')"; R="$(printf '\033[31m')"; N="$(printf '\033[0m')"
else B=""; G=""; Y=""; R=""; N=""; fi
say() { printf '%s\n' "$*"; }
ok() { printf '%s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf '%s!%s %s\n' "$Y" "$N" "$*"; }
die() { printf '%s✗ %s%s\n' "$R" "$*" "$N" >&2; exit 1; }

# ---- 参数 ----
ACTION=install
DOMAIN=""; EMAIL=""; PROXY=0; PORT=""; PUBLIC_URL=""; MIRROR=""; LOCAL=""; YES=0; PURGE=0
need_arg() { if [ $# -lt 2 ] || [ -z "$2" ]; then die "$1 需要一个值"; fi; }
while [ $# -gt 0 ]; do
  case "$1" in
    install|uninstall|reset-password) ACTION="$1"; shift ;;
    --domain) need_arg "$@"; DOMAIN="$2"; shift 2 ;;
    --email) need_arg "$@"; EMAIL="$2"; shift 2 ;;
    --proxy) PROXY=1; shift ;;
    --port) need_arg "$@"; PORT="$2"; shift 2 ;;
    --public-url) need_arg "$@"; PUBLIC_URL="$2"; shift 2 ;;
    --mirror) need_arg "$@"; MIRROR="${2%/}"; shift 2 ;;
    --local) need_arg "$@"; LOCAL="${2%/}"; shift 2 ;;
    -y|--yes) YES=1; shift ;;
    --purge) PURGE=1; shift ;;
    -h|--help) sed -n '2,32p' "$0"; exit 0 ;;
    *) die "未知参数：$1（sh $0 --help 查看用法）" ;;
  esac
done
case "$VERSION" in @*) die "这是安装脚本的模板，请从 Releases 页面下载 vpsmon-install.sh" ;; esac

[ "$(id -u)" -eq 0 ] || die "请用 root 运行：sudo sh $0"
[ "$(uname -s)" = "Linux" ] || die "只支持 Linux（当前：$(uname -s)）"

# ---- 系统、架构与服务管理 ----
M="$(uname -m)"
case "$M" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "面板暂只提供 amd64 与 arm64 的构建（当前：${M}）；Agent 支持更多架构，可以装在这类主机上" ;;
esac
if [ -d /run/systemd/system ]; then INIT=systemd
elif command -v openrc-run >/dev/null 2>&1; then INIT=openrc
else die "需要 systemd 或 OpenRC 来保活面板（当前系统两者都没有）"; fi
# /etc/os-release 是主机上的系统文件，只读取名称用于显示
# shellcheck source=/dev/null
OS_NAME="$( (. /etc/os-release 2>/dev/null && printf '%s' "${PRETTY_NAME:-${ID:-linux}}") || echo linux)"

# ---- 工具 ----
fetch() { # fetch URL 输出文件
  if command -v curl >/dev/null 2>&1; then curl -fsSL --proto '=https' --tlsv1.2 --retry 2 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then wget -q -O "$2" "$1"
  else die "需要 curl 或 wget（Debian / Ubuntu：apt install curl；Alpine：apk add curl）"; fi
}
sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then openssl dgst -sha256 "$1" | awk '{print $NF}'
  else die "需要 sha256sum、shasum 或 openssl 之一"; fi
}
# 以 vpsmon 用户执行管理命令：数据库及其 WAL 文件始终归服务用户所有
as_vpsmon() {
  if command -v runuser >/dev/null 2>&1; then runuser -u vpsmon -- "$@"
  else su -s /bin/sh vpsmon -c "$*"; fi
}
# server.env：设置 / 删除一项，其余内容（包括用户自己加的参数）保留
env_set() {
  if grep -q "^$1=" "$ENV_FILE" 2>/dev/null; then
    tmp="$(mktemp)"; awk -v k="$1" -v v="$2" 'index($0, k"=") == 1 { print k"="v; next } { print }' "$ENV_FILE" > "$tmp"
    cat "$tmp" > "$ENV_FILE"; rm -f "$tmp"
  else printf '%s=%s\n' "$1" "$2" >> "$ENV_FILE"; fi
}
env_del() {
  [ -f "$ENV_FILE" ] || return 0
  tmp="$(mktemp)"; awk -v k="$1" 'index($0, k"=") != 1' "$ENV_FILE" > "$tmp"; cat "$tmp" > "$ENV_FILE"; rm -f "$tmp"
}
env_get() { if [ -f "$ENV_FILE" ]; then awk -F= -v k="$1" '$1 == k { sub(/^[^=]*=/, ""); print; exit }' "$ENV_FILE"; fi; }
port_busy() { # 端口是否被其他程序监听（本面板自己占用的不算）
  out=""
  if command -v ss >/dev/null 2>&1; then out="$(ss -Hltnp 2>/dev/null | awk -v p=":$1" '$4 ~ p"$"')"
  elif command -v netstat >/dev/null 2>&1; then out="$(netstat -ltnp 2>/dev/null | awk -v p=":$1" '$4 ~ p"$"')"
  fi
  [ -n "$out" ] && ! printf '%s' "$out" | grep -q vpsmon-server
}
svc() { # svc start|stop|restart|enable|disable|active
  if [ "$INIT" = systemd ]; then
    case "$1" in
      active) systemctl is-active --quiet "$SVC" ;;
      enable) systemctl daemon-reload; systemctl enable "$SVC" >/dev/null 2>&1 ;;
      disable) systemctl disable "$SVC" >/dev/null 2>&1 || true ;;
      *) systemctl "$1" "$SVC" ;;
    esac
  else
    case "$1" in
      active) rc-service "$SVC" status >/dev/null 2>&1 ;;
      enable) rc-update add "$SVC" default >/dev/null 2>&1 ;;
      disable) rc-update del "$SVC" default >/dev/null 2>&1 || true ;;
      *) rc-service "$SVC" "$1" >/dev/null ;;
    esac
  fi
}
logs_hint() { if [ "$INIT" = systemd ]; then echo "journalctl -u $SVC -n 50 --no-pager"; else echo "tail -n 50 /var/log/$SVC.log"; fi; }

# ================= 卸载 =================
if [ "$ACTION" = uninstall ]; then
  svc stop 2>/dev/null || true
  svc disable
  rm -f "/etc/systemd/system/$SVC.service" "/etc/init.d/$SVC" "$BIN"
  rm -rf "/etc/systemd/system/$SVC.service.d"
  [ "$INIT" = systemd ] && systemctl daemon-reload
  if [ "$PURGE" -eq 1 ]; then
    rm -rf "$DATA" "$ENV_DIR" "/var/log/$SVC.log"
    if id vpsmon >/dev/null 2>&1; then userdel vpsmon 2>/dev/null || deluser vpsmon 2>/dev/null || true; fi
    ok "面板已卸载，数据、配置与 vpsmon 用户已删除"
  else
    ok "面板已卸载。数据保留在 $DATA，配置在 $ENV_FILE；彻底删除：sudo sh $0 uninstall --purge"
  fi
  exit 0
fi

# ================= 重置密码 =================
if [ "$ACTION" = reset-password ]; then
  if [ ! -x "$BIN" ] || [ ! -f "$DATA/monitor.db" ]; then die "没有找到已安装的面板（$BIN、$DATA）"; fi
  ERR="$(mktemp)"; trap 'rm -f "$ERR"' EXIT INT TERM
  OUT="$(as_vpsmon "$BIN" admin reset-password --data "$DATA" 2>"$ERR")" || die "重置失败：$(cat "$ERR")"
  PW="$(printf '%s\n' "$OUT" | tail -n 1)"
  say ""
  say "${B}管理员密码已重置${N}（其他设备上的登录已退出，首次登录后须修改）"
  say "  账号：admin"
  say "  密码：${B}${PW}${N}"
  exit 0
fi

# ================= 安装 / 升级 =================
say "${B}VPS Monitor 面板 v$VERSION 一键安装${N}"
ok "系统 $OS_NAME · linux/$ARCH · $INIT"

UPGRADE=0
[ -x "$BIN" ] && [ -f "$DATA/monitor.db" ] && UPGRADE=1
if [ "$UPGRADE" -eq 1 ]; then
  OLD="$("$BIN" version 2>/dev/null || echo unknown)"
  ok "已安装 v$OLD：升级到 v$VERSION，数据与配置保留"
fi

# ---- 访问方式：自有域名 / 免费域名（sslip.io）/ 反向代理 ----
public_ip() { # 本机公网 IPv4（Cloudflare trace，只读取 ip= 一行）
  t="$(mktemp)"
  if fetch https://www.cloudflare.com/cdn-cgi/trace "$t" 2>/dev/null; then
    sed -n 's/^ip=\([0-9.]*\)$/\1/p' "$t" | grep -E '^[0-9]+(\.[0-9]+){3}$' || true
  fi
  rm -f "$t"
}
valid_domain() { printf '%s' "$1" | grep -Eq '^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$'; }

if [ "$UPGRADE" -eq 1 ] && [ -z "$DOMAIN" ] && [ "$PROXY" -eq 0 ]; then
  # 升级：沿用原来的访问方式（按 README 手工安装时，域名写在 systemd drop-in 的 --domain 参数中）
  DOMAIN="$(env_get VPSMON_DOMAIN)"
  DROPIN="/etc/systemd/system/$SVC.service.d/https.conf"
  if [ -z "$DOMAIN" ] && [ -f "$DROPIN" ]; then
    DOMAIN="$(sed -n 's/^ExecStart=.*--domain[ =]\([^ ]*\).*/\1/p' "$DROPIN" | head -n 1)"
  fi
  [ -n "$DOMAIN" ] || PROXY=1
fi

IP=""
if [ -z "$DOMAIN" ] && [ "$PROXY" -eq 0 ]; then
  IP="$(public_ip)"
  if [ -t 0 ] && [ "$YES" -eq 0 ]; then
    say ""
    if [ -n "$IP" ]; then
      say "面板需要一个域名来启用 HTTPS（App 与 Agent 只通过 HTTPS 连接）。"
      say "  · 有自己的域名：先把它解析到本机 ${B}$IP${N}，再输入域名"
      say "  · 没有域名：直接回车，使用免费域名 ${B}$(printf '%s' "$IP" | tr . -).sslip.io${N}"
      say "  · 本机已有 Nginx / Caddy：输入 proxy"
    else
      say "没有取得本机公网 IPv4。请输入已解析到本机的域名，或输入 proxy 使用已有的反向代理："
    fi
    printf '域名 [回车 = 默认]：'
    read -r ans || ans=""
    case "$ans" in
      proxy|PROXY) PROXY=1 ;;
      "") ;;
      *) DOMAIN="$ans" ;;
    esac
  fi
fi

if [ -z "$DOMAIN" ] && [ "$PROXY" -eq 0 ]; then
  if [ -z "$IP" ]; then
    warn "没有取得本机公网 IPv4，改用反向代理方式（--proxy）"
    PROXY=1
  elif port_busy 80 || port_busy 443; then
    warn "80 / 443 端口已被其他程序占用（可能已有网站），改用反向代理方式（--proxy）"
    PROXY=1
  else
    DOMAIN="$(printf '%s' "$IP" | tr . -).sslip.io"
    ok "使用免费域名 $DOMAIN（sslip.io 把它解析到 $IP，证书由 Let's Encrypt 签发）"
  fi
fi

if [ "$PROXY" -eq 0 ]; then
  valid_domain "$DOMAIN" || die "域名格式不正确：$DOMAIN"
  case "$DOMAIN" in *.sslip.io) ;; *)
    # 自有域名：确认已解析到本机（不能确认时只提醒，证书申请失败会在下面显示）
    [ -n "$IP" ] || IP="$(public_ip)"
    RES=""
    if command -v getent >/dev/null 2>&1; then RES="$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ')"
    elif command -v nslookup >/dev/null 2>&1; then RES="$(nslookup "$DOMAIN" 2>/dev/null | awk '/^Address/ && $NF !~ /#/ {print $NF}' | tr '\n' ' ')"; fi
    if [ -z "$RES" ]; then warn "无法解析 $DOMAIN：请确认 DNS 已生效，否则证书申请会失败"
    elif [ -n "$IP" ] && ! printf ' %s' "$RES" | grep -q " $IP\( \|$\)"; then warn "$DOMAIN 解析到 ${RES}，不是本机 $IP：证书申请可能失败"
    else ok "$DOMAIN 已解析到本机"; fi
    ;;
  esac
  for p in 80 443; do
    port_busy "$p" && die "端口 $p 已被其他程序占用：停止它，或改用 --proxy 由已有的网站服务器反向代理"
  done
else
  [ -n "$PORT" ] || PORT="$(env_get VPSMON_LISTEN | sed 's/.*://')"
  [ -n "$PORT" ] || PORT=8080
  case "$PORT" in *[!0-9]*|'') die "--port 必须是数字" ;; esac
  if [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then die "--port 必须在 1-65535 之间"; fi
  case "$PUBLIC_URL" in ''|https://*) ;; *) die "--public-url 必须以 https:// 开头" ;; esac
  if [ "$UPGRADE" -eq 0 ] && port_busy "$PORT"; then die "本机端口 $PORT 已被占用，换一个：--port 8090"; fi
fi

# ---- 下载并校验 ----
FILE="vpsmon-server-linux-$ARCH"
WANT="$(printf '%s\n' "$HASHES" | awk -v f="$FILE" '$2 == f {print $1}')"
[ -n "$WANT" ] || die "本版本没有 linux/$ARCH 的面板构建"
TMP="$(mktemp -d 2>/dev/null || mktemp -d -t vpsmon)"
trap 'rm -rf "$TMP"' EXIT INT TERM
if [ -n "$LOCAL" ]; then
  [ -f "$LOCAL/$FILE" ] || die "没有找到 $LOCAL/$FILE"
  cp "$LOCAL/$FILE" "$TMP/vpsmon-server"
  ok "使用本地文件 $LOCAL/$FILE"
else
  BASE="$REPO_URL"
  [ -n "$MIRROR" ] && BASE="$MIRROR/v$VERSION"
  say "下载 $BASE/$FILE"
  fetch "$BASE/$FILE" "$TMP/vpsmon-server" || die "下载失败：$BASE/$FILE（无法访问 GitHub 时可用 --mirror 或 --local）"
fi
GOT="$(sha256 "$TMP/vpsmon-server")"
[ "$GOT" = "$WANT" ] || die "SHA256 校验失败，已中止安装（期望 ${WANT}，实际 ${GOT}）"
ok "SHA256 校验通过"
if [ -z "$LOCAL" ] && command -v minisign >/dev/null 2>&1 && [ -n "$PUBKEY" ]; then
  if fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" && fetch "$BASE/SHA256SUMS.minisig" "$TMP/SHA256SUMS.minisig"; then
    minisign -Vm "$TMP/SHA256SUMS" -P "$PUBKEY" >/dev/null 2>&1 || die "官方签名校验失败，已中止安装"
    grep -q "^$WANT  $FILE\$" "$TMP/SHA256SUMS" || die "签名的 SHA256SUMS 与本脚本不一致，已中止安装"
    ok "官方签名校验通过"
  fi
fi
chmod 0755 "$TMP/vpsmon-server"
"$TMP/vpsmon-server" version >/dev/null 2>&1 || die "下载的程序无法在本机运行（uname -m：${M}）"

# ---- 用户、目录与程序 ----
if ! id vpsmon >/dev/null 2>&1; then
  NOLOGIN=/usr/sbin/nologin; [ -x "$NOLOGIN" ] || NOLOGIN=/sbin/nologin
  if command -v useradd >/dev/null 2>&1; then useradd --system --home-dir "$DATA" --no-create-home --shell "$NOLOGIN" vpsmon
  else addgroup -S vpsmon 2>/dev/null || true; adduser -S -D -H -h "$DATA" -s "$NOLOGIN" -G vpsmon vpsmon; fi
  ok "创建系统用户 vpsmon（面板以它运行，不使用 root）"
fi
install -d -o vpsmon -g vpsmon -m 0750 "$DATA"
install -d -m 0755 "$ENV_DIR"
if [ "$UPGRADE" -eq 1 ] && svc active; then svc stop || true; fi
install -m 0755 "$TMP/vpsmon-server" "$BIN"
ok "程序已安装到 $BIN"

# ---- 配置（/etc/vpsmon/server.env，升级不覆盖用户自己加的参数） ----
if [ ! -f "$ENV_FILE" ]; then
  printf '# vpsmon-server 参数：VPSMON_<参数名>=值（vpsmon-server run --help）。修改后重启面板。\n' > "$ENV_FILE"
  chmod 0644 "$ENV_FILE"
fi
if [ "$PROXY" -eq 0 ]; then
  env_set VPSMON_DOMAIN "$DOMAIN"
  [ -n "$EMAIL" ] && env_set VPSMON_ACME_EMAIL "$EMAIL"
  env_del VPSMON_PUBLIC_URL
else
  env_del VPSMON_DOMAIN
  env_set VPSMON_LISTEN "127.0.0.1:$PORT"
  [ -n "$PUBLIC_URL" ] && env_set VPSMON_PUBLIC_URL "$PUBLIC_URL"
fi

# ---- 首次安装：初始化数据库与管理员 ----
PW=""
if [ "$UPGRADE" -eq 0 ]; then
  OUT="$(as_vpsmon "$BIN" init --data "$DATA" 2>"$TMP/init.err")" || die "初始化数据库失败：$(cat "$TMP/init.err")"
  PW="$(printf '%s\n' "$OUT" | tail -n 1)"
  [ -n "$PW" ] || die "初始化数据库失败：没有得到初始密码"
  ok "数据库已初始化（$DATA）"
fi

# ---- 服务与保活 ----
if [ "$INIT" = systemd ]; then
  cat > "/etc/systemd/system/$SVC.service" <<'UNIT'
@SERVER_UNIT@
UNIT
  if [ "$PROXY" -eq 0 ]; then
    # 内置 HTTPS 要绑定 80 / 443：只额外授予这一项能力，仍以 vpsmon 运行
    install -d -m 0755 "/etc/systemd/system/$SVC.service.d"
    printf '[Service]\nAmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE\n' \
      > "/etc/systemd/system/$SVC.service.d/https.conf"
  else
    rm -f "/etc/systemd/system/$SVC.service.d/https.conf"
  fi
else
  cat > "/etc/init.d/$SVC" <<'UNIT'
@SERVER_OPENRC@
UNIT
  chmod 0755 "/etc/init.d/$SVC"
fi
svc enable
svc restart 2>/dev/null || svc start
ok "服务已启动，崩溃自动重启，开机自启（$INIT）"

# ---- 防火墙（只处理本机的 ufw / firewalld；云厂商的安全组需要在控制台放行） ----
if [ "$PROXY" -eq 0 ]; then
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    ufw allow 80/tcp >/dev/null && ufw allow 443/tcp >/dev/null && ok "ufw 已放行 80、443"
  elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --add-service=http --add-service=https >/dev/null && firewall-cmd --reload >/dev/null && ok "firewalld 已放行 80、443"
  fi
fi

# ---- 等待就绪 ----
READY=0
say "等待面板就绪…"
i=0
while [ $i -lt 60 ]; do
  if [ "$PROXY" -eq 1 ]; then
    if command -v curl >/dev/null 2>&1; then curl -fsS -o /dev/null "http://127.0.0.1:$PORT/api/v1/site" 2>/dev/null && READY=1
    else wget -q -O /dev/null "http://127.0.0.1:$PORT/api/v1/site" 2>/dev/null && READY=1; fi
  elif command -v curl >/dev/null 2>&1; then
    # 证书由 Let's Encrypt 验证本机的 80 端口后签发，首次约需 10～60 秒
    curl -fsS -o /dev/null --max-time 10 --resolve "$DOMAIN:443:127.0.0.1" "https://$DOMAIN/api/v1/site" 2>/dev/null && READY=1
  else
    svc active && READY=1
  fi
  [ "$READY" -eq 1 ] && break
  svc active || break
  i=$((i + 1)); sleep 2
done
if [ "$READY" -eq 1 ]; then ok "面板已就绪"
elif ! svc active; then warn "面板没有运行起来，查看日志：$(logs_hint)"
elif [ "$PROXY" -eq 0 ]; then warn "证书还没有签发成功：确认云厂商安全组已放行 80 与 443、域名解析到本机；日志：$(logs_hint)"
else warn "面板还没有响应，稍后再试；日志：$(logs_hint)"; fi

# ---- 结果 ----
if [ "$PROXY" -eq 0 ]; then URL="https://$DOMAIN"
elif [ -n "$PUBLIC_URL" ]; then URL="$PUBLIC_URL"
else URL="http://127.0.0.1:$PORT（只监听本机，需配置反向代理）"; fi
LINE="============================================================"
say ""
say "$G$LINE$N"
if [ "$UPGRADE" -eq 1 ]; then say "${B} VPS Monitor 面板已升级到 v$VERSION${N}"; else say "${B} VPS Monitor 面板安装完成 v$VERSION${N}"; fi
say "$G$LINE$N"
say " 访问地址：${B}$URL${N}"
say " 账号：    ${B}admin${N}"
if [ -n "$PW" ]; then
  say " 密码：    ${B}$PW${N}"
  say "           （只显示这一次，请立即保存；首次登录后需要修改）"
else
  say " 密码：    沿用原来的密码（忘记时：sudo sh $0 reset-password）"
fi
say "$G$LINE$N"
if [ "$PROXY" -eq 1 ]; then
  say " 反向代理：把你的 HTTPS 域名转发到 127.0.0.1:$PORT，例如 Caddy："
  say "     monitor.example.com {"
  say "         reverse_proxy 127.0.0.1:$PORT"
  say "     }"
  [ -n "$PUBLIC_URL" ] || say " 然后：sudo sh $0 --proxy --public-url https://monitor.example.com（写进 Agent 安装命令）"
  say "$G$LINE$N"
else
  say " 提示：云服务器请在安全组中放行 TCP 80 与 443（证书签发与访问都需要）"
fi
say ' 添加节点：登录后点“仪表板 → 新建节点”，在要监控的主机上运行页面给出的安装命令'
if [ "$INIT" = systemd ]; then
  say " 管理：    systemctl status|restart|stop $SVC   日志：journalctl -u $SVC -f"
else
  say " 管理：    rc-service $SVC status|restart|stop   日志：tail -f /var/log/$SVC.log"
fi
say " 配置：    $ENV_FILE（修改后重启）  数据：$DATA"
say " 升级：    下载新版 vpsmon-install.sh 再运行一次   卸载：sudo sh $0 uninstall"
say "$G$LINE$N"
