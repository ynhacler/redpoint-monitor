# RedPoint Monitor

[中文](README.md) | **English**

App-first lightweight self-hosted monitoring platform for VPS enthusiasts.

For people who run several VPSes: host one panel yourself, put a tiny Agent on each server, and see status, traffic and alerts on the web and in the mobile app.

- **Lightweight**: the Agent is a single static binary that runs as a non-root user and only reads `/proc` and `/sys`; the panel is a single binary + SQLite with no other services
- **Accurate traffic**: reboot detection, billing cycles, manual calibration, overage forecasts; traffic packages as the cloud provider counts them (AWS Lightsail, Alibaba Cloud / Tencent Cloud lightweight servers, Alibaba Cloud CDT, Oracle egress)
- **More than monitoring**: streaming unlock checks, reachability and blocking checks from mainland China, three-carrier latency tests, cloud account costs, subscription tracking, in-browser connectivity tests
- **Security first**: the Agent only collects and reports and never runs commands from the panel; Agent upgrades only accept official releases signed offline by the developer; credentials are stored as hashes only; no telemetry of any kind

Full design: [docs/design.md](docs/design.md) (Chinese). Progress: [TODO.md](TODO.md).

---

## Features

### Node monitoring

- **Live metrics**: CPU (including steal and IO wait), memory and swap, disk usage per mount point, per-disk IO (throughput, IOPS, latency, utilization),
  network speed, TCP / UDP connections and process count; refreshes every 2 seconds while a node's detail page is open, then returns to normal
- **History charts**: 1 hour to 30 days; long ranges show both average and peak; gaps in reporting stay as gaps instead of being joined into fake data
- **Health summary**: the last 24 hours / 30 days condensed into a few sentences ("CPU averaged 21%, peaked at 30%", "expected to use 7.4 GB by the end of the cycle"), so you don't have to read every chart
- **Node IP info**: ASN, operator and location of the public IP, looked up automatically (weekly, and immediately when the address changes)
- **Watched processes, listening ports**: alerts when a watched process isn't running; listening ports grouped as public / private / local, with Redis, MySQL and similar flagged when exposed publicly
- **Containers**: CPU, memory and process count of Docker / Podman containers read from cgroups (no Docker socket access), restart counts, alerts when a container stops
- **Proxy software**: detects common proxies such as Xray, sing-box, Hysteria and WireGuard and reads their protocols and ports (never any keys); alerts when a port stops listening
- **Security**: firewall status and rules (ufw, firewalld, nftables, iptables, fail2ban, CSF); port baseline — alerts when a public port outside the baseline appears,
  and the panel can check each port's real reachability from the internet

### Network and unlocks

- **Streaming and site unlock checks**: from each VPS, checks whether Netflix, Disney+, YouTube Premium, Prime Video, TikTok, ChatGPT, Claude, Google, Reddit,
  Steam, Yahoo, Truth Social and Telegram work and which region they detect; adds local services based on the VPS location (Japan ABEMA / radiko,
  Hong Kong myTV SUPER / Viu, Taiwan KKTV, Korea Coupang Play, US Hulu, UK BBC iPlayer and more)
- **Three-carrier latency**: scheduled latency and packet-loss tests from each VPS to China Telecom / Unicom / Mobile in each province and to overseas cities, with medians and charts; compare which node is fastest to a given place
- **Reachability and blocking checks from mainland China**: use a node inside China as a probe to Ping and TCP-test your other nodes; only several consecutive failed rounds mark a node as "possibly blocked" or a port as "possibly blocked"
- **Connectivity test**: one click in the browser tests your current network against 44 common sites in China / the US / worldwide (with real logos) for reachability and latency — handy for checking proxy routing rules

### Traffic and costs

- **Traffic plans**: counted by billing day and time zone (in + out, outbound only and other modes, with an optional multiplier), end-of-cycle forecasts, calibrate by entering the number from your provider's panel;
  traffic history kept per billing cycle
- **Cloud**: connect read-only AWS, Alibaba Cloud, Tencent Cloud (China and international sites) and Oracle Cloud accounts to see this month's cost and forecast, balance, budget,
  plus instances, expiry dates and traffic packages as the provider reports them; pay-by-traffic Alibaba Cloud instances are marked CDT with their free-quota usage;
  adding an account comes with step-by-step instructions and what each permission is for, and "Check permissions" lists which APIs work and how many instances each region has
- **Subscriptions**: track price, cycle, trial and charge day of streaming, tools, cloud services, SIM cards, VPSes and more, with reminders before renewal;
  VPS entries can record several IPs and specs and link automatically to monitored nodes; a stats page shows spending by category and month

### Alerts and notifications

- Rules for offline, CPU, memory, disk, traffic, expiry, certificates, stopped containers, port changes and more, with defaults that work out of the box
- Channels: Telegram, Webhook, email, Bark, ntfy, Discord, WeCom, DingTalk, Feishu; app push notifications are end-to-end encrypted
- Quiet hours, mute, maintenance mode; problems in the panel itself (crashed background jobs, low disk and so on) also alert; an external heartbeat can monitor the panel

### Experience

- **Mobile web and app are for viewing**: bottom tab bar on mobile web, compact node cards, live metrics on the first screen of the detail page; the Flutter app supports QR pairing,
  home-screen widgets, biometric lock and encrypted push
- **Configure in the browser**: System → Site settings changes the app name, interface language, login captcha, and the web port and a separate Agent port (applied immediately)
- **中文 / English**: switchable interface language; country names and dates follow the language
- **Accounts and permissions**: two-step verification (TOTP + recovery codes), login session management, read-only viewer accounts, read-only API keys that can be limited to some nodes
- **One-command Agent install**: the hash is checked before anything runs; Agents can be upgraded remotely in bulk from the web, with automatic rollback on failure

> The panel's command-line output and some server-generated messages are in Chinese; the web interface can be switched to English.

---

## Contents

1. [Deploy the panel](#1-deploy-the-panel)
2. [Set up HTTPS](#2-set-up-https)
3. [Add nodes (install the Agent)](#3-add-nodes-install-the-agent)
4. [Connect the mobile app](#4-connect-the-mobile-app)
5. [Operations](#5-operations)
6. [Security](#6-security)
7. [Build from source and develop](#7-build-from-source-and-develop)

```text
  Mobile app ──┐                          ┌── Agent (host A)
               ├── HTTPS ──► Panel ◄── HTTPS ┼── Agent (host B)
  Browser ─────┘    vpsmon-server          └── Agent (host C …)
                    (SQLite, single binary)
```

The Agent connects out to the panel (outbound HTTPS); hosts don't need any open ports.

---

## 1. Deploy the panel

### One-click install (recommended)

On a Linux server (as root or with sudo), run these three lines: download the installer → check it against the published SHA256SUMS → run it.

```bash
curl -fsSLO https://github.com/ynhacler/redpoint-monitor/releases/latest/download/vpsmon-install.sh
curl -fsSLO https://github.com/ynhacler/redpoint-monitor/releases/latest/download/SHA256SUMS
grep ' vpsmon-install.sh$' SHA256SUMS | sha256sum -c - && sudo sh vpsmon-install.sh
```

The script detects the OS and architecture → downloads the panel and checks its SHA256 → creates a dedicated `vpsmon` user → sets up HTTPS → installs a system service (restarts on crash,
starts on boot) → opens ports 80 / 443 in the local firewall → waits for the panel to be ready, then prints (the script's output is in Chinese):

```text
============================================================
 VPS Monitor 面板安装完成 v0.8.0
============================================================
 访问地址：https://203-0-113-7.sslip.io        ← URL
 账号：    admin                               ← username
 密码：    k7mXq2…                             ← password, shown only once; change it at first login
============================================================
```

- **No domain needed**: without a domain it uses the free `<server IP>.sslip.io` with a Let's Encrypt certificate. With your own domain, point it at the server first,
  then enter it when asked, or run `sudo sh vpsmon-install.sh --domain monitor.example.com`.
- **Interactive port setup**: when run in a terminal without `--port`, the script shows whether 80 / 443 are taken, suggests a port that can get a certificate and asks;
  if both 80 and 443 are taken it asks whether to switch to reverse-proxy mode (then asks for the local port and public address). `-y` skips all questions.
- **Custom port**: `--port` sets the HTTPS port (default 443), e.g. `sudo sh vpsmon-install.sh --domain monitor.example.com --port 8443`,
  giving `https://monitor.example.com:8443`. Certificates can only be validated through port 80 or 443, so: with 80 free, HTTPS can use any port;
  with 80 taken, only `--port 443` works (validated through 443, nothing listens on 80); with both taken, use reverse-proxy mode below.
- **Nginx / Caddy already running** (80 / 443 taken): switches to reverse-proxy mode, where the panel only listens on a local port, and prints the proxy config; or specify
  `--proxy --port 8080 --public-url https://monitor.example.com`.
- **Supported systems**: Debian, Ubuntu, CentOS / RHEL / Rocky / Alma, Fedora, Amazon Linux, openSUSE, Arch (systemd),
  Alpine (OpenRC); amd64, arm64.
- **Cloud servers**: allow TCP 80 and 443 in the security group.
- Running it again **upgrades** (data, config and password are kept); `sudo sh vpsmon-install.sh reset-password` resets the admin password;
  `sudo sh vpsmon-install.sh uninstall` uninstalls (add `--purge` to delete data too), see [Uninstall](#uninstall); `--help` lists all options (mirror download, offline install and more).
- With [minisign](https://jedisct1.github.io/minisign/) installed, the script also verifies the official signature; you can also verify SHA256SUMS by hand first (see 1.2).

After the one-click install, skip to [section 3, Add nodes](#3-add-nodes-install-the-agent). The manual steps follow.

### 1.1 Requirements

| Item | Requirement |
|---|---|
| OS | Linux (systemd), amd64 or arm64 |
| Resources | 1 core / 512 MB RAM is enough; a few hundred MB of disk per 100 nodes (retention is adjustable) |
| Domain | A domain pointing at the panel host, e.g. `monitor.example.com` (the Agent and app only connect over HTTPS) |
| Ports | 443 open to the internet (built-in HTTPS also needs 80 for certificates) |
| Tools | `curl`, `sha256sum`, [`minisign`](https://jedisct1.github.io/minisign/) (Debian / Ubuntu: `sudo apt install minisign`) |

### 1.2 Download and verify

Find the latest version on the [Releases](https://github.com/ynhacler/redpoint-monitor/releases) page. Download the panel binary and the signed checksum file, verify the signature, then check the file hash:

```bash
VER=0.3.0          # replace with the latest version from the Releases page (without the v)
ARCH=amd64         # or arm64
BASE=https://github.com/ynhacler/redpoint-monitor/releases/download/v$VER
curl -fsSLO $BASE/vpsmon-server-linux-$ARCH -O $BASE/SHA256SUMS -O $BASE/SHA256SUMS.minisig

# 1. Verify the SHA256SUMS signature with the official public key (stop if this fails)
minisign -Vm SHA256SUMS -P RWRvxJiJUet21SDEV8XFOSShkV7Wbn/ZsQhL/dpS5VVB781sfhsqwMu9
# 2. Check the binary against the verified SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
```

Continue only when both steps pass. The official public key is also built into the Agent and the panel ([internal/release/keys.go](internal/release/keys.go)) and verifies every official release.

### 1.3 Install as a system service

```bash
# Binary
sudo install -m 0755 vpsmon-server-linux-$ARCH /usr/local/bin/vpsmon-server

# Dedicated non-root user and data directory
sudo useradd --system --home /var/lib/vpsmon --shell /usr/sbin/nologin vpsmon
sudo install -d -o vpsmon -g vpsmon -m 0750 /var/lib/vpsmon

# Initialize the database and create the admin (username admin; the initial password is shown only once and must be changed at first login)
sudo -u vpsmon vpsmon-server init --data /var/lib/vpsmon

# systemd unit (sandboxed; listens only on 127.0.0.1:8080 by default, exposed through one of the HTTPS options in section 2)
curl -fsSLo vpsmon-server.service \
  https://raw.githubusercontent.com/ynhacler/redpoint-monitor/v$VER/deploy/systemd/vpsmon-server.service
sudo install -m 0644 vpsmon-server.service /etc/systemd/system/vpsmon-server.service
```

**Custom ports**: once the panel is running, change the web port in **System → Site settings → Listen ports**, and set a separate Agent port (applied immediately;
if a wrong value locks you out, run `sudo -u vpsmon vpsmon-server admin reset-listen --data /var/lib/vpsmon` on the panel host and restart the panel to return to the startup flags). Startup flags can also go in
`/etc/vpsmon/server.env` (`VPSMON_<FLAG>`, kept across upgrades), e.g. to move to 9090:

```bash
sudo install -d -m 0755 /etc/vpsmon
echo 'VPSMON_LISTEN=127.0.0.1:9090' | sudo tee /etc/vpsmon/server.env
```

A bare port (`VPSMON_LISTEN=9090` or `--listen 9090`) listens on localhost only. Replace `8080` below with your port.

Don't start it yet; set up HTTPS next.

---

## 2. Set up HTTPS

Pick one. The Agent and app **only accept HTTPS** (with a valid certificate; self-signed certificates are rejected).

### Option A: built-in HTTPS (recommended when no other sites run on the host)

The panel obtains and renews its own Let's Encrypt certificate; ports 80 and 443 must be reachable from the internet (using it means accepting the Let's Encrypt terms of service):

```bash
sudo mkdir -p /etc/systemd/system/vpsmon-server.service.d
curl -fsSL https://raw.githubusercontent.com/ynhacler/redpoint-monitor/v$VER/deploy/systemd/vpsmon-server-https.conf \
  | sed 's/monitor.example.com/your.domain/' | sudo tee /etc/systemd/system/vpsmon-server.service.d/https.conf
sudo systemctl daemon-reload
sudo systemctl enable --now vpsmon-server
```

The panel still runs as `vpsmon`, with only the extra ability to bind ports 80 / 443. Certificates are cached in `/var/lib/vpsmon/certs`.
To serve HTTPS on a port other than 443, add `VPSMON_HTTPS_LISTEN=8443` to `/etc/vpsmon/server.env` (certificate validation still needs public port 80).
If port 80 is taken you can use 443 alone: add `VPSMON_HTTP_LISTEN=off` and the certificate is validated through 443 (TLS-ALPN-01); http:// no longer redirects.

### Option B: Caddy reverse proxy (when the host already serves other sites)

Keep the panel on `127.0.0.1:8080` and add to your Caddyfile:

```caddy
monitor.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Then tell the panel its public address (used in install commands and app pairing links):

```bash
echo 'VPSMON_PUBLIC_URL=https://monitor.example.com' | sudo tee -a /etc/vpsmon/server.env
```

Then:

```bash
sudo systemctl enable --now vpsmon-server
sudo systemctl reload caddy
```

WebSocket (live updates) goes through Caddy automatically. Other reverse proxies such as Nginx work the same way: forward to `127.0.0.1:8080` and pass WebSocket upgrade requests.

### 2.1 First login

Open `https://monitor.example.com`, sign in with `admin` and the initial password from section 1.3, and change the password when asked (at least 8 characters). Two-step verification can then be turned on in **System → Account**.

> Forgot the password: run `sudo -u vpsmon vpsmon-server admin reset-password --data /var/lib/vpsmon` on the panel host.
> Lost the two-step device and the recovery codes: run `sudo -u vpsmon vpsmon-server admin disable-totp --data /var/lib/vpsmon`, then sign in with the password only.
> Alerts are off by default (no evaluation, no notifications, no alerts page in the web UI). Turn them on with `sudo -u vpsmon vpsmon-server admin alerts on --data /var/lib/vpsmon` and refresh the page; `off` turns them off again.

---

## 3. Add nodes (install the Agent)

1. In the web UI, go to **Dashboard → New node** and enter a name (plus an optional group, traffic plan, billing day and expiry date).
2. After saving, the page shows **one install command** with a one-time enroll code (valid for 24 hours by default).
3. Log in to the host you want to monitor and paste it. The command downloads a specific version of the install script → **checks it against the hash the panel took from the verified, signed manifest** → runs it only if that passes; the script then downloads the Agent for the right architecture, checks its built-in hash, installs it as a service and enrolls with the code.
4. A few seconds later the node shows as online.

The command looks like this (always copy it from the page; don't type it by hand):

```text
curl -fsSLo agent.sh https://github.com/…/agent-0.3.1.sh && echo "<hash>  agent.sh" | sha256sum -c - && sudo sh agent.sh --server https://monitor.example.com --enroll ENR-…
```

| Item | Supported |
|---|---|
| Architectures | amd64, 386, arm64, armv7, armv6, riscv64, mips, mipsle (static builds, glibc or musl doesn't matter) |
| Service managers | systemd, OpenRC (Alpine); other systems use user mode below |
| Runs as | A dedicated non-root user `vpsmon-agent` |

**Hosts without root**: drop the `sudo` from the command. The Agent installs for the current user (`~/.local/bin`) and stays alive through a systemd user service (needs `loginctl enable-linger`) or crontab. User mode can't be upgraded remotely from the panel; run `vpsmon-agent upgrade` on the host instead.

**Hosts that can't reach GitHub**: start the panel with `--release-mirror` and it keeps a local copy of each verified official release; install commands then download from the panel (hashes still come from the signed manifest).

Common Agent commands:

```bash
vpsmon-agent status                  # service status and the last report
sudo vpsmon-agent doctor             # diagnose: service, reporting, DNS / HTTPS / certificate, clock, whether the token is valid
sudo vpsmon-agent re-enroll --enroll ENR-…   # move to another node; add --server https://… to move to another panel
sudo vpsmon-agent upgrade            # upgrade to the latest officially signed version (rolls back on failure)
sudo vpsmon-agent uninstall          # stop, remove and notify the panel
```

Full uninstall instructions (including deleting nodes and removing the panel) are in section 5, [Uninstall](#uninstall).

You can also upgrade in bulk from **Dashboard → Agent upgrades** in the web UI: the panel can only pick officially signed versions, and the updater on the host re-verifies with its built-in public key and refuses downgrades.
Nodes installed earlier need `sudo vpsmon-agent enable-remote-upgrade` run once on the host (new installs have it on by default). On Alpine (OpenRC), crond checks for upgrade requests every 15 minutes,
and that command starts crond. If a remote upgrade fails saying the staging directory isn't writable, run the same command to fix it; `sudo vpsmon-agent doctor` checks for these problems.

Agent logs: `journalctl -u vpsmon-agent` on systemd hosts (add `-p warning` for warnings and errors only); on Alpine they're in the system log (`logread | grep vpsmon-agent`).

---

## 4. Connect the mobile app

The app doesn't use the panel's username and password; it pairs with a one-time AK:

1. In the web UI, go to **App → Create pairing AK** (创建配对 AK), choose which nodes the app can see (all / a group / specific nodes) and whether it may mute and use maintenance mode, then confirm with your current password.
2. The page shows the panel address, the AK (`MNT-…`) and a pairing link, **only once**.
3. Scan the code in the app, or enter the panel address and AK by hand.

- By default an AK is single-use, pairs one device and is valid for 1 day; paired devices get their own credentials, so an expired AK doesn't affect them.
- The app is **read-only**: at most it can mute alerts and start / end maintenance mode (you can disable this when creating the AK). If you tick "allow recording subscriptions in the App" when creating the AK, it can also add, edit and delete subscriptions and mark subscriptions or nodes as renewed (moving the expiry date forward one period). All other configuration changes happen in the web UI.
- **App → Connected devices** (已连接设备) lets you revoke any device at any time, signing it out immediately.

> The app is in development (phase C): QR / manual pairing and the node list work, but it isn't in the app stores yet; developers can run it in a simulator or on a device with `make app-setup && make app-run`.

---

## 5. Operations

### Upgrade the panel

```bash
# After downloading and verifying the new version as in section 1.2:
sudo -u vpsmon vpsmon-server backup --data /var/lib/vpsmon      # back up before upgrading
sudo install -m 0755 vpsmon-server-linux-$ARCH /usr/local/bin/vpsmon-server
sudo systemctl restart vpsmon-server
journalctl -u vpsmon-server -n 20 --no-pager                     # check the version in the startup log
```

The database schema migrates automatically with each version (append-only; existing data isn't changed).

### Backup and restore

```bash
# Online backup (works while the panel is running), written to /var/lib/vpsmon/backups/ by default; --keep keeps only the latest N
sudo -u vpsmon vpsmon-server backup --data /var/lib/vpsmon --keep 7

# Restore: stop the panel first
sudo systemctl stop vpsmon-server
sudo -u vpsmon vpsmon-server restore --data /var/lib/vpsmon --from /var/lib/vpsmon/backups/monitor-….db
sudo systemctl start vpsmon-server
```

Back up daily with cron and copy the backups to another machine. Backups contain the encrypted cloud account credentials; the `secret.key` that decrypts them is also in the data directory, so keep it safe as well.

### Troubleshooting

```bash
journalctl -u vpsmon-server -f                                  # panel logs (credentials are redacted automatically)
sudo -u vpsmon vpsmon-server audit --data /var/lib/vpsmon       # audit log: logins and actions
sudo -u vpsmon vpsmon-server diag --data /var/lib/vpsmon        # create a local diagnostic bundle (never uploaded anywhere)
```

### Uninstall

Before uninstalling, make a backup as in the previous section (`vpsmon-server backup`) and copy it to another machine.

**Uninstall the Agent** (on each monitored host):

```bash
sudo vpsmon-agent uninstall
```

This stops and removes the service, the remote-upgrade updater, the binary, the config (including the token), the state directory and the `vpsmon-agent` user, and tells the panel to revoke the node's token, so the node goes back to "Not installed".
If the panel can't be reached, the local uninstall still completes and you handle the node in the panel by hand. For a user-mode (non-root) Agent, run it without `sudo`; it also removes the keepalive (systemd user service or crontab entry).

The node's history stays in the panel. When you no longer need the node, open it in the web UI → **Edit** → **Delete node** (删除节点) at the bottom of the page (confirm with your password).
Its metrics history and traffic statistics are deleted with it and can't be recovered.

**Uninstall the panel** — installed with the one-click script:

```bash
sudo sh vpsmon-install.sh uninstall           # stop and remove the service and binary; keep data and config (a reinstall picks them up)
sudo sh vpsmon-install.sh uninstall --purge   # also delete the database, config and vpsmon user (can't be undone)
```

If the original `vpsmon-install.sh` is gone, download and verify it again as in "One-click install"; uninstalling doesn't depend on the version.

Installed by hand as in section 1.3:

```bash
sudo systemctl disable --now vpsmon-server
sudo rm -f /etc/systemd/system/vpsmon-server.service /usr/local/bin/vpsmon-server
sudo rm -rf /etc/systemd/system/vpsmon-server.service.d
sudo systemctl daemon-reload
# The following deletes all data; make sure you have a backup
sudo rm -rf /var/lib/vpsmon /etc/vpsmon
sudo userdel vpsmon
```

If you used a Caddy reverse proxy, also remove the panel's site from the Caddyfile and run `sudo systemctl reload caddy`.

If you remove the panel before the Agents, the Agents can't notify the panel, but their local uninstall still completes.

---

## 6. Security

- **No remote execution**: the Agent only collects and reports; the panel has no feature, and will never add one, that makes hosts run commands or scripts.
- **Signing stays with the developer**: Agent releases are signed offline by the developer; the panel can only sync and select officially signed versions, and can't sign or distribute unsigned programs.
- **Separate credentials**: web sessions (`ses_`), read-only API keys (`api_`), app devices (`dev_` / `rt_`), Agents (`agt_`), enroll codes (`ENR-`) and app pairing AKs (`MNT-`) are never interchangeable; every credential is shown once and only its hash is stored.
- **HTTPS only**: the Agent and app refuse plain-text connections and invalid certificates.
- **Verifiable installs**: install commands check the hash before running anything, never `curl | sh`; long-lived credentials never appear on the command line.
- **No telemetry**: the panel and Agent send nothing to the developer; all data stays on your own panel.

Please report security issues privately to the maintainer rather than opening a public issue.

---

## 7. Build from source and develop

Requires Go ≥ 1.26 and Node ≥ 20.19.

```bash
make build                  # build the panel and Agent for this machine into bin/
make build-linux            # cross-compile all Agent architectures and the panel (amd64 / arm64) into dist/
make dev                    # local development: panel + simulated Agent + Vite at http://localhost:5173
make test && make lint      # tests and static checks
```

Development conventions, layout and security constraints are in [CLAUDE.md](CLAUDE.md); the design is in [docs/design.md](docs/design.md).
