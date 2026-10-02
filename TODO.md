# Roadmap (Phase 1 / MVP)

Each item is sized for one vibe-coding session. Paste the **Prompt** line into Claude Code.
Mark done with `[x]`. Design references point to `docs/design.md`.

## M1 — Skeleton ✅

- [x] Agent: Linux collector + fake collector, HTTPS-only reporter, SIGTERM final report
- [x] Server: SQLite (WAL), batched writes, in-memory realtime state, traffic deltas with boot_id
- [x] Web: server list with status, metrics, cycle traffic
- [x] App: connect + server list
- [x] Makefile, dev.sh, deploy.sh, systemd units

## M2 — Data quality (agent + traffic)

- [ ] Downsampling into metrics_1m / 5m / 1h + retention (design 21, 18.4)
  - Prompt: `Implement downsampling per design ch.21: tables metrics_1m/5m/1h with avg+max columns, an in-process job, batched deletes, and tests.`
- [ ] Agent bounded retry buffer for failed reports (design 1.6.14)
- [ ] Multiple disk mounts, disk IO (design 4.6, 4.7)
- [ ] Traffic calibration + traffic_factor + decimal/binary unit (design 5.7, 5.8, 18.12)
  - Prompt: `Implement traffic calibration per design 5.7 and table traffic_adjustments (18.12), with API endpoints and table tests.`
- [ ] Traffic forecast (design ch.32)
- [ ] Server detail page in Web with history charts (ECharts — confirm dependency first)

## M3 — Make it safe to expose

- [ ] Web login: single admin, Argon2id, session cookie, CSRF, login rate limit (design 8.2, 23.4, 23.5); remove admin token from Web
- [ ] Built-in HTTPS via ACME (`--domain`) (design 25)
- [ ] Web: add/edit/delete servers, generate install command (manual verified + script) (design 27)
- [ ] Agent token revoke / rotate
- [ ] `vpsmon-server backup` / `restore` (design 25)

## M4 — App pairing (the core App experience)

- [ ] AK create/list/revoke + QR in Web (design 8.4, 18.9, 19.2)
  - Prompt: `Implement App AK per design 8.4/18.9/19.2: one-time, hashed, shown once, QR payload monitor://pair?... ; read-only scope + allow_low_risk_ops.`
- [ ] `/api/v1/app/pair` → device access token (short) + refresh token (rotating) (design 12.3–12.5, 19.3)
- [ ] Device list + revoke in Web; revocation kills REST/WS/refresh/push (design 1.6.5)
- [ ] App: QR scan pairing, token refresh, multiple monitoring centers (design 12.x, 1.5.2)
- [ ] App: server detail with realtime mode via WebSocket (design 1.5.9, ch.20)
- [ ] App: biometric lock, privacy mode, offline cache (design 1.5.10–1.5.12)

## M5 — Alerts and push

- [ ] Alert engine: offline, CPU, mem, disk, traffic thresholds, expiry; states + events (design 16)
- [ ] Mute / maintenance mode (design 1.5.14, 1.5.15)
- [ ] Telegram / Webhook / ntfy channels (design 31)
- [ ] E2E push: X25519 key at pairing, HPKE payloads, iOS NSE / Android data messages (design 30)
- [ ] push-relay service (stateless, in-memory rate limit) (design 30.2)
- [ ] UnifiedPush for Android (design 30.1)

## M6 — Release

- [ ] VPS asset fields: provider, price, expiry + reminders (design 1.2.3, 1.2.5)
- [ ] `vpsmon-agent upgrade` (local, signed manifest, anti-downgrade, rollback) (design 29.7)
- [ ] Release pipeline: reproducible builds, offline minisign signing, SHA256SUMS (design 27.1, 29.7)
- [ ] Install script hosted on official domain (design 27.2)
- [ ] Optional Docker image (`FROM scratch`)

Phase 2 (remote upgrade + canary, widgets, multi-center aggregate view, probes, Docker monitoring,
PostgreSQL) — see design ch.36.
