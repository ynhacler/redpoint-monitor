# VPS Monitor — guide for AI-assisted development

App-first, self-hosted, lightweight monitoring for people who run many VPSes.
Full design: `docs/design.md` (v1.1). Milestones and next tasks: `TODO.md`.
When a task touches a feature, read the relevant design chapter first.

## Commands

```bash
make dev          # server + fake agent + Vite → http://localhost:5173 (token in .dev/admin.token)
make test         # Go tests — run after every Go change
make lint         # go vet + vue-tsc
make build-linux  # cross-compile linux amd64/arm64 into dist/
make vm-agent     # real Linux collector in an OrbStack VM
make app-run      # Flutter app
make deploy VPS=user@host
```

Before saying a task is done: `make test && make lint` pass, and for UI changes, look at it in the browser.

## Layout

```
cmd/server/            vpsmon-server CLI (init, add-server, run)
cmd/agent/             vpsmon-agent
internal/protocol/     agent ↔ server wire format (backward compatible only)
internal/agent/collector/
  parse.go             pure /proc parsers (unit tested, any OS)
  linux.go             real collector (build tag linux)
  fake.go, other.go    fake metrics; used automatically on macOS
internal/server/
  server.go            HTTP routes, ingest, batched flush, retention
  store.go             SQLite schema (append-only migrations) and queries
  traffic.go           traffic delta / billing cycle logic (unit tested)
  auth.go              token generation + hashing
web/                   Vue 3 + Vite + TS; dist/ is embedded via go:embed
app/                   Flutter app (iOS/Android); run `make app-setup` once
deploy/systemd/        hardened unit files
scripts/               setup-mac.sh, dev.sh, deploy.sh
```

## Security invariants — never violate, even if asked casually

These are the product's core promise. If a request conflicts with one, stop and say so.

1. **No remote execution.** The agent only collects and reports. Never add endpoints, config fields or
   upgrade paths that run shell commands, scripts or arbitrary binaries from the server.
2. **No signing key in the server.** Agent releases are signed offline by the developer. The server may
   sync/select official signed releases; it must never sign, upload or serve unsigned binaries.
   Agent/updater verify with embedded public keys and refuse downgrades.
3. **Three separate credentials.** Web admin, App device token, Agent token are never interchangeable.
   Agent tokens can only report for their own server. Prefixes: `adm_`, `agt_`, future `dev_`/`rt_`/`ak_`.
4. **Store hashes, show once.** Tokens/AKs are shown once at creation; only SHA-256 hashes are stored.
   Never log tokens, Authorization headers, or full AKs.
5. **App is read-only** (+ mute / maintenance mode). No App credential may change config or trigger upgrades.
6. **HTTPS only** outside loopback / explicit dev flags. Never disable certificate verification.
7. **No telemetry, no analytics SDKs, no developer-side storage of user data.** Push payloads are
   end-to-end encrypted before leaving the server (design ch. 30).
8. **Agent runs as non-root.** New collectors must work from /proc and /sys; if a capability is needed
   (e.g. CAP_NET_RAW for ICMP), make it optional.

## Conventions

- Go: stdlib first (`net/http` ServeMux patterns, `database/sql`). Ask before adding a dependency.
  SQLite driver is `github.com/ncruces/go-sqlite3` (pure Go via wasm, no CGO) so `CGO_ENABLED=0`
  cross-compiles work. Keep the server a single static binary.
- DB: add a new entry to `migrations` in store.go; never edit an existing one. Writes go through the
  batched flush; realtime reads come from memory (`Server.latest`), not the DB.
- Protocol: only add optional fields. Old agents must keep working with new servers.
- Traffic logic lives in pure functions with table tests (see traffic_test.go). Keep it that way —
  traffic accuracy is a core selling point.
- Web: Vue 3 `<script setup lang="ts">`, no UI framework yet (Element Plus/ECharts planned, ask first).
  Decimal units for bytes (design 5.8). Abnormal-first sorting.
- App: Flutter, Material 3. Credentials only in flutter_secure_storage.
- Errors to clients are generic JSON `{"error": "..."}`; details go to server logs (without secrets).
- Comments explain *why*, and reference design sections like `(design 5.5)`.
- Commit small, working steps. Do not commit `.dev/`, `*.token`, `dist/`, `bin/`.

## Current state (M1 done)

Working: agent (Linux real + fake) → server ingest → SQLite → `/api/v1/servers` → Web list and Flutter list.
Traffic deltas with boot_id reset detection and billing cycles. Dev admin token auth (temporary).
Not yet: login, AK pairing, alerts, push, downsampling, calibration, HTTPS. See TODO.md.

## Dev environment notes

- Mac dev server: `127.0.0.1:8080`. iOS simulator uses 127.0.0.1, Android emulator uses 10.0.2.2.
- Remote VPS server listens on loopback; reach it via `ssh -N -L 8080:127.0.0.1:8080 user@vps`.
- Agents on other VPSes need HTTPS to the server: until M3 (built-in ACME), put Caddy in front
  (`monitor.example.com { reverse_proxy 127.0.0.1:8080 }`).
