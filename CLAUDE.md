# VPS Monitor — guide for AI-assisted development

App-first, self-hosted, lightweight monitoring for people who run many VPSes.
Full design: `docs/design.md` (v1.2). Milestones and next tasks: `TODO.md`.
When a task touches a feature, read the relevant design chapter first.
Development order is **A (API + Agent) → B (Web) → C (App)** (design 35.1); do not start App work early.

## Commands

```bash
make dev          # server + fake agent + Vite → http://localhost:5173 (login admin / .dev/admin.password)
make test         # Go tests — run after every Go change
make lint         # go vet + vue-tsc
make check-design # design numbering / references (run scripts/check_design_refs.py --write after editing design.md)
make build-linux  # agent for 6 linux arches + server amd64/arm64 into dist/
make vm-agent     # real Linux collector in an OrbStack VM
make agent-footprint # on Linux: run the real agent 60s, report RSS / CPU vs design 4.2 (also in CI)
make loadtest     # panel capacity: temp panel + 100 simulated agents (scripts/loadtest.sh N INTERVAL DURATION BACKLOG)
make app-run      # Flutter app
make deploy VPS=user@host                      # dev-only SSH deploy (design 27)
make install-server / install-agent            # on a VPS checkout, after `make build`
vpsmon-server backup --data DIR [--keep N]     # online backup; restore --from FILE (panel stopped); diag (local bundle)
```

Before saying a task is done: `make test && make lint && make check-design` pass, and for UI changes,
look at it in the browser. CI (`.github/workflows/ci.yml`) runs the same targets plus shellcheck.

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
  apierror.go          error codes (design 43.4); middleware.go: request_id, recovery, logs, clientIP
  nodes.go             create node / install command; enroll.go: enroll codes, /agent/enroll, rate limit
  store_nodes.go       nodes, enroll codes, audit log persistence
internal/logging/      slog setup + credential redaction (design 24.7)
internal/release/      minisign verification, signed release manifest, versions, official public keys (design 29.7)
internal/agent/upgrade/ local `vpsmon-agent upgrade`: verify → anti-downgrade → atomic swap → health check → rollback;
                       remote.go (agent stages signed release) + updater.go (root updater via systemd path unit, design 29.13)
internal/push/         encrypted push protocol shared by panel and relay: HPKE seal/open, padding, Ed25519 request signing (design 30)
internal/relay/        push relay: verify, rate limit, forward to APNs / FCM, no logs; cmd/push-relay is its binary (design 30.2)
cmd/vpsmon-release/    CI tool: agent-x.y.z.sh + manifest.json + SHA256SUMS (never signs; signing is offline)
api/openapi.yaml       API contract — change it before the code (design 19.0.1)
web/                   Vue 3 + Vite + TS; dist/ is embedded via go:embed
  src/pages/           Overview, Servers, ServerDetail, ServerEdit, Install (routes in src/router.ts)
  src/components/      design-system components (design 41.3)
design/tokens.json     design tokens shared by Web and App (design 41.2)
app/                   Flutter app (iOS/Android); run `make app-setup` once
  ios/NotificationService/ iOS push decryption extension (CryptoKit HPKE); `make ios-push-check` verifies it against Go vectors
deploy/systemd/        hardened unit files; deploy/openrc/ OpenRC script (Alpine, design 28)
scripts/               setup-mac.sh, dev.sh, deploy.sh, install.sh, check_design_refs.py
docs/design.md         the design; numbering rules in design 40.9
```

Release flow (design 40.8.4): tag vX.Y.Z → `.github/workflows/release.yml` draft → `scripts/sign-release.sh` offline → publish.
Install script template: `scripts/agent.sh.in` (POSIX sh, rendered per version).

## Security invariants — never violate, even if asked casually

These are the product's core promise. If a request conflicts with one, stop and say so.

1. **No remote execution.** The agent only collects and reports. Never add endpoints, config fields or
   upgrade paths that run shell commands, scripts or arbitrary binaries from the server.
2. **No signing key in the server.** Agent releases are signed offline by the developer. The server may
   sync/select official signed releases; it must never sign, upload or serve unsigned binaries.
   Agent/updater verify with embedded public keys and refuse downgrades.
3. **Separate credentials.** Web admin, App device token, Agent token, enroll code are never
   interchangeable (design 17.1). Agent tokens can only report for their own server; an enroll code can
   only claim its own node. Prefixes: `ses_` (Web session), `agt_`, `api_` (read-only API key, design 45.2), `ENR-`, future `dev_`/`rt_`/`MNT-`.
   Web admin = username + Argon2id password → HttpOnly session cookie + `X-CSRF-Token` on writes (design 17.4).
4. **Store hashes, show once.** Tokens, AKs and enroll codes are shown once; only SHA-256 hashes are
   stored. Never log credentials, Authorization headers or cookies; redact by prefix (design 24.7).
5. **App is read-only** (+ mute / maintenance mode). No App credential may change config or trigger upgrades.
6. **HTTPS only** outside loopback / explicit dev flags. Never disable certificate verification.
7. **No telemetry, no analytics SDKs, no developer-side storage of user data.** Push payloads are
   end-to-end encrypted before leaving the server (design ch. 30).
8. **Agent runs as non-root.** New collectors must work from /proc and /sys; if a capability is needed
   (e.g. CAP_NET_RAW for ICMP), make it optional.
9. **Install = download → verify → execute.** Never generate or document `curl … | sh`. Install scripts
   are versioned, and their hash comes from the server's signature-verified sync (design 27.3, 27.5.5).
   Long-lived credentials never appear on a command line (design 27.1).

## Conventions

- **Comments in Chinese** (design ch. 39): identifiers stay English; cite sections as `（设计 5.5）`;
  mark security code with `【安全】`; TODOs carry a TODO.md milestone, e.g. `// TODO(A3): …`.
  Existing English comments are converted when that code is next modified, not in bulk.
- Go: stdlib first (`net/http` ServeMux patterns, `database/sql`, `log/slog`). Ask before adding a
  dependency. SQLite driver is `github.com/ncruces/go-sqlite3` (pure Go via wasm, no CGO) so
  `CGO_ENABLED=0` cross-compiles work. Keep the server a single static binary.
- API: contract first — change `api/openapi.yaml`, then the code (design 19.0.1). JSON snake_case,
  times in Unix seconds, list responses `{"items", "next_cursor"}`.
- Errors to clients: `{"error": {"code", "message", "request_id", "details"}}` with stable codes from
  design 43.4 and Chinese messages; internals (SQL, paths, stacks) only in server logs. Security checks
  fail closed (design 43.1).
- Auth: register routes only through `handle(pattern, access, h)` in server.go routes(); add every new
  route to the expected table in TestPermissionMatrix (design 17.2, 17.5).
- DB: add a new entry to `migrations` in store.go; never edit an existing one. Writes go through the
  batched flush; realtime reads come from memory (`Server.latest`), not the DB.
- Protocol: only add optional fields. Old agents must keep working with new servers.
- Traffic logic lives in pure functions with table tests (see traffic_test.go). Keep it that way —
  traffic accuracy is a core selling point.
- Web: Vue 3 `<script setup lang="ts">` + vue-router, self-built components in `web/src/components` on
  design tokens (design 41). Colors / sizes / spacing only via `var(--…)` from `design/tokens.json`
  (generated into `web/src/styles/tokens.css` by `npm run tokens`; never edit the CSS by hand).
  Charts: ECharts via `components/Chart.vue` (tree-shaken imports). Number formats only via `src/format.ts`
  (design 41.4.1); health rules via `src/metrics.ts`. Abnormal-first sorting. Check light + dark + 375px.
- App: Flutter, Material 3 themed from tokens. Credentials only in flutter_secure_storage.

## Git workflow (design 40.2, 40.8)

- Work on `feature/<name>` or `fix/<name>`; open a PR to `main`, squash merge after CI passes.
  One PR per TODO.md item, PR description cites design sections. Do not push to `main` directly.
- Commit small, working steps. Never commit `.dev/`, `*.token`, `dist/`, `bin/`, `node_modules/`.
- Deploy only committed code; the version is `git describe` (design 40.3.2).

## Current state (M1 done, phase A starting)

Working: agent (Linux real + fake) → server ingest → SQLite → `/api/v1/servers` → Web list and Flutter list.
Traffic deltas with boot_id reset detection and billing cycles. Web login with sessions (A2); the old `adm_` dev
token is gone. Forgot password: `vpsmon-server admin reset-password --data DIR` on the panel host.
Node creation + enroll codes + `/agent/enroll` are in; `vpsmon-agent install` and the Web pages are next.
Until signed releases exist (A7), the install command is the manual form `sudo vpsmon-agent install
--server … --enroll …` and the binary must already be on the host (design 27.3.1).
App pairing server side (AK → `/app/pair` → dev_ + rt_ tokens, design 19.4.1) and the Web "App 接入" page are in.
The Flutter app pairs (scan or manual), refreshes device tokens and lists servers (design 12.7.1).
Not yet: push, App detail page / offline cache (phase C). See TODO.md.

## Dev environment notes (design 40)

- Mac dev server: `127.0.0.1:8080`. iOS simulator uses 127.0.0.1, Android emulator uses 10.0.2.2.
- test-server (greenJp): `vpsmon-server` via systemd on loopback, Caddy in front for public HTTPS
  (UDP 443 is taken by another service there, so Caddy runs with `protocols h1 h2`).
- test-agent (jp-store): installed with the dev scripts until the enroll flow (A1) exists; after that,
  always through the product install command (design 40.4.2).
- Building on a VPS needs Go ≥ 1.26 (required by the SQLite driver since v0.33) and Node ≥ 20.19 (Ubuntu's apt versions are too old).
