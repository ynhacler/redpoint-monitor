#!/usr/bin/env bash
# Alpine 兼容检查（设计 28）：在 alpine 容器（musl、BusyBox、OpenRC）中
#   1. 运行真实采集器的集成测试（容器内的 /proc、overlay 根分区）
#   2. 用 OpenRC 安装、运行、卸载 Agent（TestOpenRCE2E）
# 需要 docker 与 make build-linux 产出的 dist/vpsmon-agent-linux-amd64；CI 的 ubuntu runner 上运行。
set -euo pipefail
cd "$(dirname "$0")/.."

image=${ALPINE_IMAGE:-alpine:3.20}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$work/collector.test" ./internal/agent/collector
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$work/setup.test" ./internal/agent/setup
cp dist/vpsmon-agent-linux-amd64 "$work/vpsmon-agent"

# shellcheck disable=SC2016  # 单引号中的命令在容器内执行
docker run --rm -v "$work:/w:ro" "$image" sh -euc '
  cat /etc/alpine-release | sed "s/^/alpine /"
  /w/collector.test -test.run TestLinuxCollect -test.v
  apk add --no-cache openrc >/dev/null
  mkdir -p /run/openrc && touch /run/openrc/softlevel
  VPSMON_E2E_OPENRC=1 AGENT_BIN=/w/vpsmon-agent /w/setup.test -test.run OpenRCE2E -test.v
'
