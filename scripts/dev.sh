#!/usr/bin/env bash
# Start server + fake agent + Vite together. Ctrl-C stops everything.
set -euo pipefail
cd "$(dirname "$0")/.."

[ -f .dev/admin.token ] || make dev-init

pids=()
cleanup() {
  echo; echo "stopping..."
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "→ server on :8080"
go run ./cmd/server run --data .dev/data --listen "${LISTEN:-127.0.0.1:8080}" 2>&1 | sed 's/^/[server] /' &
pids+=($!)

for _ in $(seq 1 60); do curl -fs http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break; sleep 0.5; done

echo "→ fake agent"
go run ./cmd/agent --server http://127.0.0.1:8080 --token-file .dev/agent.token --fake --interval 3s 2>&1 | sed 's/^/[agent]  /' &
pids+=($!)

echo "→ web on http://localhost:5173  (admin token: $(cat .dev/admin.token))"
(cd web && npm run dev -- --clearScreen false) 2>&1 | sed 's/^/[web]    /' &
pids+=($!)

wait
