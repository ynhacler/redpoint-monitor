#!/usr/bin/env bash
# 非 root 安装检查（设计 27.13）：在 Debian 容器中以普通用户安装、运行、保活、卸载 Agent。
# 真实的 crontab（Debian 的 crontab 命令允许普通用户写入）、后台启动、运行锁与首次上报。
# 需要 docker 与 make build-linux 产出的 dist/vpsmon-agent-linux-amd64；CI 的 ubuntu runner 上运行。
# shellcheck disable=SC2016  # 单引号中的命令在容器内执行
set -euo pipefail
cd "$(dirname "$0")/.."

image=${DEBIAN_IMAGE:-debian:bookworm-slim}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$work/setup.test" ./internal/agent/setup
cp dist/vpsmon-agent-linux-amd64 "$work/vpsmon-agent"
# mktemp 的目录是 0700：容器中的普通用户需要能进入并执行其中的程序
chmod 0755 "$work" "$work/setup.test" "$work/vpsmon-agent"

docker run --rm -v "$work:/w:ro" "$image" sh -euc '
  apt-get update -qq >/dev/null && apt-get install -y -qq cron procps >/dev/null
  useradd -m alice
  su alice -s /bin/sh -c "cd ~ && VPSMON_E2E_USER=1 AGENT_BIN=/w/vpsmon-agent /w/setup.test -test.run UserModeE2E -test.v"
'
