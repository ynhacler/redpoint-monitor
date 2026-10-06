# RedPoint Monitor

App-first lightweight self-hosted monitoring platform for VPS enthusiasts.

面向拥有多台 VPS 的个人用户：自建一个面板，每台主机装一个很小的 Agent，在 Web 和手机 App 上查看状态、流量与告警。

- **轻量**：Agent 是单个静态二进制，以非 root 用户运行，只读 `/proc` 与 `/sys`；面板是单个二进制 + SQLite，不依赖其他服务
- **流量准确**：重启识别、计费周期、手动校准、超额预测；云厂商口径的流量包（AWS Lightsail、阿里云 / 腾讯云轻量、Oracle 出站流量）
- **告警与通知**：离线、CPU、内存、磁盘、流量等规则，Telegram / Webhook 通知，免打扰、静音与维护模式
- **安全优先**：Agent 只采集上报、不执行任何面板下发的命令；Agent 升级只接受开发者离线签名的官方版本；凭证只保存哈希；没有任何遥测

完整设计见 [docs/design.md](docs/design.md)，开发进度见 [TODO.md](TODO.md)。

---

## 目录

1. [部署面板](#1-部署面板)
2. [配置 HTTPS](#2-配置-https)
3. [添加节点（安装 Agent）](#3-添加节点安装-agent)
4. [连接手机 App](#4-连接手机-app)
5. [日常运维](#5-日常运维)
6. [安全说明](#6-安全说明)
7. [从源码构建与开发](#7-从源码构建与开发)

```text
  手机 App ──┐                         ┌── Agent（主机 A）
             ├── HTTPS ──► 面板 ◄── HTTPS ┼── Agent（主机 B）
  浏览器 ────┘    vpsmon-server         └── Agent（主机 C …）
                  （SQLite，单个二进制）
```

Agent 主动连接面板（出站 HTTPS），主机上不需要开放任何端口。

---

## 1. 部署面板

### 1.1 准备

| 项目 | 要求 |
|---|---|
| 系统 | Linux（systemd），amd64 或 arm64 |
| 资源 | 1 核 / 512 MB 内存即可；每 100 个节点约需数百 MB 磁盘（保留期可调） |
| 域名 | 一个解析到面板主机的域名，如 `monitor.example.com`（Agent 与 App 只通过 HTTPS 连接） |
| 端口 | 对外开放 443（内置 HTTPS 时另需 80，用于申请证书） |
| 工具 | `curl`、`sha256sum`、[`minisign`](https://jedisct1.github.io/minisign/)（Debian / Ubuntu：`sudo apt install minisign`） |

### 1.2 下载并校验

在 [Releases](https://github.com/ynhacler/redpoint-monitor/releases) 页面找到最新版本号。下载面板二进制与签名过的校验和文件，先验证签名，再校验文件哈希：

```bash
VER=0.3.0          # 替换为 Releases 页面上的最新版本号（不带 v）
ARCH=amd64         # 或 arm64
BASE=https://github.com/ynhacler/redpoint-monitor/releases/download/v$VER
curl -fsSLO $BASE/vpsmon-server-linux-$ARCH -O $BASE/SHA256SUMS -O $BASE/SHA256SUMS.minisig

# 1. 用官方公钥验证 SHA256SUMS 的签名（失败时不要继续）
minisign -Vm SHA256SUMS -P RWRvxJiJUet21SDEV8XFOSShkV7Wbn/ZsQhL/dpS5VVB781sfhsqwMu9
# 2. 按已验证的 SHA256SUMS 校验二进制
sha256sum -c --ignore-missing SHA256SUMS
```

两步都通过后再继续。官方公钥同时内置在 Agent 与面板中（[internal/release/keys.go](internal/release/keys.go)），用于验证所有官方发布。

### 1.3 安装为系统服务

```bash
# 程序
sudo install -m 0755 vpsmon-server-linux-$ARCH /usr/local/bin/vpsmon-server

# 专用的非 root 用户与数据目录
sudo useradd --system --home /var/lib/vpsmon --shell /usr/sbin/nologin vpsmon
sudo install -d -o vpsmon -g vpsmon -m 0750 /var/lib/vpsmon

# 初始化数据库并创建管理员（用户名 admin；初始密码只显示这一次，首次登录时必须修改）
sudo -u vpsmon vpsmon-server init --data /var/lib/vpsmon

# systemd 单元（带沙箱限制；默认只监听 127.0.0.1:8080，由第 2 节的 HTTPS 方式对外提供服务）
curl -fsSLo vpsmon-server.service \
  https://raw.githubusercontent.com/ynhacler/redpoint-monitor/v$VER/deploy/systemd/vpsmon-server.service
sudo install -m 0644 vpsmon-server.service /etc/systemd/system/vpsmon-server.service
```

**自定义端口**：面板的参数可以写在 `/etc/vpsmon/server.env`（`VPSMON_<参数名>`，升级不会覆盖），例如换到 9090：

```bash
sudo install -d -m 0755 /etc/vpsmon
echo 'VPSMON_LISTEN=127.0.0.1:9090' | sudo tee /etc/vpsmon/server.env
```

只写端口（`VPSMON_LISTEN=9090` 或 `--listen 9090`）表示只监听本机。下文中的 `8080` 换成你的端口即可。

先不要启动，接着配置 HTTPS。

---

## 2. 配置 HTTPS

二选一。Agent 与 App 都**只接受 HTTPS**（证书必须有效，不接受自签名证书）。

### 方式 A：面板内置 HTTPS（主机上没有其他网站时推荐）

面板自己向 Let's Encrypt 申请并续期证书，需要 80 与 443 端口可从公网访问（使用即表示接受 Let's Encrypt 的服务条款）：

```bash
sudo mkdir -p /etc/systemd/system/vpsmon-server.service.d
curl -fsSL https://raw.githubusercontent.com/ynhacler/redpoint-monitor/v$VER/deploy/systemd/vpsmon-server-https.conf \
  | sed 's/monitor.example.com/你的域名/' | sudo tee /etc/systemd/system/vpsmon-server.service.d/https.conf
sudo systemctl daemon-reload
sudo systemctl enable --now vpsmon-server
```

面板仍以 `vpsmon` 用户运行，只额外获得绑定 80 / 443 端口的能力。证书缓存在 `/var/lib/vpsmon/certs`。
HTTPS 不用 443 时，在 `/etc/vpsmon/server.env` 中加 `VPSMON_HTTPS_LISTEN=8443`（证书验证仍需要公网 80 端口）。

### 方式 B：Caddy 反向代理（主机上已有其他网站时）

保持面板监听 `127.0.0.1:8080`，在 Caddyfile 中加入：

```caddy
monitor.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

再告诉面板它的对外地址（写进安装命令与 App 配对链接）：

```bash
echo 'VPSMON_PUBLIC_URL=https://monitor.example.com' | sudo tee -a /etc/vpsmon/server.env
```

然后：

```bash
sudo systemctl enable --now vpsmon-server
sudo systemctl reload caddy
```

WebSocket（实时刷新）经 Caddy 自动转发，无需额外配置。Nginx 等其他反向代理同理：转发到 `127.0.0.1:8080`，并转发 WebSocket 升级请求。

### 2.1 首次登录

浏览器打开 `https://monitor.example.com`，用 `admin` 和第 1.3 节显示的初始密码登录，按提示修改密码（至少 12 位）。

> 忘记密码：在面板主机上执行 `sudo -u vpsmon vpsmon-server admin reset-password --data /var/lib/vpsmon`。

---

## 3. 添加节点（安装 Agent）

1. Web 中点 **仪表板 → 新建节点**，填写名称（以及可选的分组、流量套餐、计费日、到期日）。
2. 保存后页面显示**一条安装命令**，包含一次性注册码（默认 24 小时内有效）。
3. 登录要监控的主机，粘贴执行。命令会：下载指定版本的安装脚本 → **按面板从已验签清单中取得的哈希校验** → 校验通过才执行；脚本再下载对应架构的 Agent、按内置哈希校验、安装为服务并用注册码注册。
4. 几秒后 Web 上节点变为“在线”。

命令形如（以页面显示的为准，不要手工拼写）：

```text
curl -fsSLo agent.sh https://github.com/…/agent-0.3.1.sh && echo "<哈希>  agent.sh" | sha256sum -c - && sudo sh agent.sh --server https://monitor.example.com --enroll ENR-…
```

| 项目 | 支持 |
|---|---|
| 架构 | amd64、386、arm64、armv7、armv6、riscv64、mips、mipsle（静态编译，不区分 glibc / musl） |
| 服务管理 | systemd、OpenRC（Alpine）；其他系统用下面的用户模式 |
| 运行身份 | 专用的非 root 用户 `vpsmon-agent` |

**没有 root 权限的主机**：去掉命令中的 `sudo` 直接执行，Agent 会安装到当前用户（`~/.local/bin`），用 systemd 用户服务（需 `loginctl enable-linger`）或 crontab 保活。用户模式不支持从面板远程升级，在主机上执行 `vpsmon-agent upgrade` 即可。

**无法访问 GitHub 的主机**：面板以 `--release-mirror` 启动后，会把已验签的官方版本镜像到本地，安装命令改为从面板下载（哈希仍来自签名清单）。

Agent 常用命令：

```bash
vpsmon-agent status                  # 服务状态与最近一次上报
sudo vpsmon-agent doctor             # 诊断：服务、上报、DNS / HTTPS / 证书、时钟、Token 是否有效
sudo vpsmon-agent re-enroll --enroll ENR-…   # 换绑到其他节点；加 --server https://… 换到另一个面板
sudo vpsmon-agent upgrade            # 升级到最新的官方签名版本（失败自动回滚）
sudo vpsmon-agent uninstall          # 停止、删除并通知面板
```

也可以在 Web 的 **仪表板 → Agent 升级** 页面批量远程升级：面板只能选择官方签名的版本，主机上的 updater 用内置公钥复验并拒绝降级。
已安装的节点需要先在主机上执行一次 `sudo vpsmon-agent enable-remote-upgrade`（新安装默认启用）。Alpine（OpenRC）由 crond 每 15 分钟
检查一次升级请求，该命令会启动 crond。远程升级失败并提示暂存目录不可写时，同样执行这条命令修复；`sudo vpsmon-agent doctor` 会检查这些问题。

Agent 日志：systemd 主机 `journalctl -u vpsmon-agent`（只看警告与错误加 `-p warning`），Alpine 在系统日志中（`logread | grep vpsmon-agent`）。

---

## 4. 连接手机 App

App 不使用面板的用户名和密码，而是用一次性的配对 AK：

1. Web 中点 **App → 创建配对 AK**，选择 App 可查看的节点（全部 / 分组 / 指定节点）、是否允许静音与维护，输入当前密码确认。
2. 页面显示面板地址、AK（`MNT-…`）与配对链接，**只显示这一次**。
3. 在 App 中扫码，或手工填写面板地址与 AK，完成配对。

- AK 默认一次性、只能配对 1 台设备、1 天内有效；配对后设备使用独立的凭证，AK 过期不影响已配对的设备。
- App **只读**：最多允许静音告警、开启 / 结束维护模式（创建 AK 时可关闭），所有配置变更只能在 Web 中完成。
- 在 **App → 已连接设备** 中可随时吊销任一设备，该设备立即退出。

> App 正在开发中（阶段 C）：已支持扫码 / 手工配对与节点列表，尚未上架应用商店；开发者可用 `make app-setup && make app-run` 在模拟器或真机上运行。

---

## 5. 日常运维

### 升级面板

```bash
# 按第 1.2 节下载并校验新版本后：
sudo -u vpsmon vpsmon-server backup --data /var/lib/vpsmon      # 升级前先备份
sudo install -m 0755 vpsmon-server-linux-$ARCH /usr/local/bin/vpsmon-server
sudo systemctl restart vpsmon-server
journalctl -u vpsmon-server -n 20 --no-pager                     # 确认启动日志中的版本号
```

数据库结构随版本自动迁移（只追加，不修改已有数据）。

### 备份与恢复

```bash
# 在线备份（面板运行中也可执行），默认写入 /var/lib/vpsmon/backups/，--keep 只保留最近 N 份
sudo -u vpsmon vpsmon-server backup --data /var/lib/vpsmon --keep 7

# 恢复：必须先停止面板
sudo systemctl stop vpsmon-server
sudo -u vpsmon vpsmon-server restore --data /var/lib/vpsmon --from /var/lib/vpsmon/backups/monitor-….db
sudo systemctl start vpsmon-server
```

建议用 cron 每天备份一次，并把备份文件复制到另一台机器。备份包含加密保存的云账户凭证；解密用的 `secret.key` 也在数据目录中，请一并妥善保存。

### 排查问题

```bash
journalctl -u vpsmon-server -f                                  # 面板日志（凭证已自动脱敏）
sudo -u vpsmon vpsmon-server audit --data /var/lib/vpsmon       # 审计日志：登录与操作记录
sudo -u vpsmon vpsmon-server diag --data /var/lib/vpsmon        # 生成本地诊断包（不会上传到任何地方）
```

### 卸载面板

```bash
sudo systemctl disable --now vpsmon-server
sudo rm -f /etc/systemd/system/vpsmon-server.service /usr/local/bin/vpsmon-server
sudo rm -rf /etc/systemd/system/vpsmon-server.service.d
sudo rm -rf /var/lib/vpsmon      # 会删除全部数据，请先确认已备份
sudo userdel vpsmon
```

---

## 6. 安全说明

- **不远程执行**：Agent 只采集与上报，面板没有、也不会增加让主机执行命令或脚本的功能。
- **签名在开发者手中**：Agent 版本由开发者离线签名；面板只能同步、选择官方签名的版本，不能签名或分发未签名的程序。
- **凭证分离**：Web 会话（`ses_`）、只读 API Key（`api_`）、App 设备（`dev_` / `rt_`）、Agent（`agt_`）、注册码（`ENR-`）、App 配对 AK（`MNT-`）互不通用；所有凭证只显示一次，数据库中只保存哈希。
- **只走 HTTPS**：Agent 与 App 拒绝明文连接与无效证书。
- **安装可校验**：安装命令先校验哈希再执行，不使用 `curl | sh`；长期凭证不出现在命令行中。
- **没有遥测**：面板与 Agent 不向开发者发送任何数据；所有数据只保存在你自己的面板上。

发现安全问题请私下联系维护者，不要公开提交 Issue。

---

## 7. 从源码构建与开发

需要 Go ≥ 1.26 与 Node ≥ 20.19。

```bash
make build                  # 构建本机的面板与 Agent 到 bin/
make build-linux            # 交叉编译全部 Agent 架构与面板 amd64 / arm64 到 dist/
make dev                    # 本地开发：面板 + 模拟 Agent + Vite，打开 http://localhost:5173
make test && make lint      # 测试与静态检查
```

开发约定、目录结构与安全约束见 [CLAUDE.md](CLAUDE.md)，设计文档见 [docs/design.md](docs/design.md)。
