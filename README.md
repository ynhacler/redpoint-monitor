# RedPoint Monitor

**中文** | [English](README.en.md)

App-first lightweight self-hosted monitoring platform for VPS enthusiasts.

面向拥有多台 VPS 的个人用户：自建一个面板，每台主机装一个很小的 Agent，在 Web 和手机 App 上查看状态、流量与告警。

- **轻量**：Agent 是单个静态二进制，以非 root 用户运行，只读 `/proc` 与 `/sys`；面板是单个二进制 + SQLite，不依赖其他服务
- **流量准确**：重启识别、计费周期、手动校准、超额预测；云厂商口径的流量包（AWS Lightsail、阿里云 / 腾讯云轻量、阿里云 CDT、Oracle 出站流量）
- **不止监控**：流媒体解锁检测、国内可达性与被墙检测、三网测速、云账户费用、订阅记账、浏览器端连通性测试
- **安全优先**：Agent 只采集上报、不执行任何面板下发的命令；Agent 升级只接受开发者离线签名的官方版本；凭证只保存哈希；没有任何遥测

完整设计见 [docs/design.md](docs/design.md)，开发进度见 [TODO.md](TODO.md)。

---

## 功能一览

### 节点监控

- **实时指标**：CPU（含被抢占 steal、IO 等待）、内存与 Swap、每个挂载点的磁盘用量、按磁盘的 IO（读写速率、IOPS、延迟、繁忙度）、
  网速、TCP / UDP 连接与进程数；打开节点详情时临时提高到 2 秒一次，离开后自动恢复
- **历史曲线**：1 小时到 30 天，长时间范围同时显示平均与峰值；断报的时段曲线断开，不会连成假数据
- **健康摘要**：把最近 24 小时 / 30 天的指标归纳成几句话（“CPU 平均 21%，峰值 30%”“预计周期结束用到 7.4 GB”），不用逐张图判断
- **节点 IP 信息**：自动查询公网 IP 的 ASN、运营方与位置（每周一次，地址变化立即更新）
- **关注的进程、监听端口**：指定的进程没在运行时提醒；监听端口按公网 / 内网 / 本机分组，Redis、MySQL 等暴露在公网时标为风险
- **容器**：从 cgroup 读取 Docker / Podman 容器的 CPU、内存与进程数（不连接 Docker 套接字），记录重启次数，容器停止时告警
- **代理程序**：识别 Xray、sing-box、Hysteria、WireGuard 等常见代理，读出协议与端口（不读取任何密钥），端口没在监听时提醒
- **安全**：防火墙状态与规则（ufw、firewalld、nftables、iptables、fail2ban、CSF）；端口基线——出现基线外的公网端口时告警，
  并可从面板逐个检测端口是否真能从公网访问

### 网络与解锁

- **流媒体与网站解锁检测**：从 VPS 检测 Netflix、Disney+、YouTube Premium、Prime Video、TikTok、ChatGPT、Claude、Google、Reddit、
  Steam、Yahoo、Truth Social、Telegram 能否使用及服务认定的地区；并按 VPS 所在地追加当地服务（日本 ABEMA / radiko、
  香港 myTV SUPER / Viu、台湾 KKTV、韩国 Coupang Play、美国 Hulu、英国 BBC iPlayer 等）
- **三网测速**：从每台 VPS 定时测到各省电信 / 联通 / 移动与境外城市的延迟与丢包，显示中位数与曲线；可对比哪台节点到某地最快
- **国内可达性与被墙检测**：把国内的节点设为探针，反向测其他节点的 Ping 与 TCP 端口，连续多轮异常才判定“疑似被墙”或“端口疑似被封”
- **连通性测试**：在浏览器中一键测试当前网络到中国 / 美国 / 全球 44 个常用网站（带真实标志）的连通性与延迟，用来检查代理分流规则

### 流量与费用

- **流量套餐**：按计费日与时区统计（入 + 出、只算出站等模式，可设系数），预测周期结束用量，填写服务商面板的数值即可校准；
  流量记录按计费周期保留
- **云平台**：接入 AWS、阿里云、腾讯云（国内站与国际站）、Oracle Cloud 的只读账户，查看本月费用与预估、余额、预算，
  以及云厂商口径的实例、到期时间与流量包；阿里云按流量计费的 ECS 标出 CDT 并显示免费额度用量；
  添加账户时给出分步说明与每条权限的用途，“检查权限”逐项列出哪些接口可用、各区域有几台实例
- **订阅**：记录影音、工具、云服务、SIM 电话卡、VPS 等订阅的价格、周期、试用期与扣费日，到期前提醒；
  VPS 类可记录多个 IP 与配置，并自动链接到已接入监控的节点；统计页按类别与月份显示花费

### 告警与通知

- 离线、CPU、内存、磁盘、流量、到期、证书、容器停止、端口变化等规则，默认规则开箱即用
- 通知渠道：Telegram、Webhook、邮件、Bark、ntfy、Discord、企业微信、钉钉、飞书；App 推送端到端加密
- 免打扰时段、静音、维护模式；面板自身异常（后台任务崩溃、磁盘不足等）同样告警；可配置外部心跳监控面板本身

### 使用体验

- **手机 Web 与 App 以查看为主**：手机 Web 底部标签栏、紧凑节点卡片、详情第一屏就是实时指标；Flutter App 支持扫码配对、
  首页小组件、生物识别锁与加密推送
- **网页中完成配置**：系统 → 网站配置可修改应用名称、界面语言、登录验证码，以及 Web 端口与 Agent 专用端口（立即生效）
- **中文 / English**：界面语言可切换，国家名称、日期等随语言显示
- **账号与权限**：两步验证（TOTP + 恢复码）、登录设备管理、只读查看者账号、可限定节点范围的只读 API Key
- **一条命令安装 Agent**：先校验哈希再执行；Agent 可在 Web 中批量远程升级，失败自动回滚

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

### 一键安装（推荐）

在一台 Linux 服务器上（root 或 sudo）执行下面三行：下载安装脚本 → 按发布的 SHA256SUMS 校验 → 执行。

```bash
curl -fsSLO https://github.com/ynhacler/redpoint-monitor/releases/latest/download/vpsmon-install.sh
curl -fsSLO https://github.com/ynhacler/redpoint-monitor/releases/latest/download/SHA256SUMS
grep ' vpsmon-install.sh$' SHA256SUMS | sha256sum -c - && sudo sh vpsmon-install.sh
```

脚本会：识别系统与架构 → 下载面板并校验 SHA256 → 创建专用用户 `vpsmon` → 配置 HTTPS → 安装为系统服务（崩溃自动重启、
开机自启）→ 放行本机防火墙的 80 / 443 → 等待面板就绪，最后显示：

```text
============================================================
 VPS Monitor 面板安装完成 v0.8.0
============================================================
 访问地址：https://203-0-113-7.sslip.io
 账号：    admin
 密码：    k7mXq2…（只显示这一次，请立即保存；首次登录后需要修改）
============================================================
```

- **不需要域名**：没有域名时自动使用免费域名 `<本机 IP>.sslip.io`，证书由 Let's Encrypt 签发。有自己的域名时先解析到本机，
  运行时输入域名，或 `sudo sh vpsmon-install.sh --domain monitor.example.com`。
- **已有 Nginx / Caddy**（80 / 443 被占用）：自动改为反向代理方式，面板只监听本机端口，并给出代理配置；也可以指定
  `--proxy --port 8080 --public-url https://monitor.example.com`。
- **支持的系统**：Debian、Ubuntu、CentOS / RHEL / Rocky / Alma、Fedora、Amazon Linux、openSUSE、Arch（systemd），
  Alpine（OpenRC）；amd64、arm64。
- **云服务器**：在安全组中放行 TCP 80 与 443。
- 再次运行即**升级**（数据、配置与密码保留）；`sudo sh vpsmon-install.sh reset-password` 重置管理员密码；
  `sudo sh vpsmon-install.sh uninstall` 卸载（加 `--purge` 同时删除数据），详见 [卸载](#卸载)；`--help` 查看全部选项（镜像下载、离线安装等）。
- 装有 [minisign](https://jedisct1.github.io/minisign/) 时，脚本还会验证官方签名；也可以先手工验签 SHA256SUMS（见 1.2）。

一键安装后可以直接跳到 [第 3 节添加节点](#3-添加节点安装-agent)。下面是手工安装的步骤。

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

**自定义端口**：面板启动后可以直接在 **系统 → 网站配置 → 监听端口** 中修改 Web 端口，并单独设置 Agent 专用端口（立即生效，
改错连不上时在面板主机上运行 `sudo -u vpsmon vpsmon-server admin reset-listen --data /var/lib/vpsmon` 并重启面板，回到启动参数）。启动参数也可以写在
`/etc/vpsmon/server.env`（`VPSMON_<参数名>`，升级不会覆盖），例如换到 9090：

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

浏览器打开 `https://monitor.example.com`，用 `admin` 和第 1.3 节显示的初始密码登录，按提示修改密码（至少 8 位）。之后可在 **系统 → 账号** 中开启两步验证。

> 忘记密码：在面板主机上执行 `sudo -u vpsmon vpsmon-server admin reset-password --data /var/lib/vpsmon`。
> 丢失两步验证设备且没有恢复码：执行 `sudo -u vpsmon vpsmon-server admin disable-totp --data /var/lib/vpsmon`，之后只用密码登录。
> 告警默认关闭（不判定、不发任何通知，Web 不显示告警页面）。需要时执行 `sudo -u vpsmon vpsmon-server admin alerts on --data /var/lib/vpsmon`，刷新页面即可；`off` 再关闭。

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

完整的卸载说明（含删除节点、卸载面板）见第 5 节 [卸载](#卸载)。

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

### 卸载

卸载前建议先按上一节做一次备份（`vpsmon-server backup`），并把备份文件复制到其他机器。

**卸载 Agent**（在每台被监控的主机上执行）：

```bash
sudo vpsmon-agent uninstall
```

这条命令会停止并删除服务、远程升级用的 updater、程序、配置（含 Token）、状态目录和 `vpsmon-agent` 用户，并通知面板吊销该节点的 Token，节点回到“待安装”。
面板连不上时本地照样卸载完成，节点需要在面板中手动处理。用户模式（非 root）安装的 Agent 去掉 `sudo` 执行即可，同时会移除保活（systemd 用户服务或 crontab 项）。

节点的历史数据仍保留在面板中。不再需要这个节点时，在 Web 中打开节点 → **编辑** → 页面底部 **删除节点**（需输入密码确认）。
历史指标和流量统计会一起删除，无法恢复。

**卸载面板**——用一键安装脚本安装的：

```bash
sudo sh vpsmon-install.sh uninstall           # 停止并删除服务与程序，保留数据和配置（重新安装后可继续使用）
sudo sh vpsmon-install.sh uninstall --purge   # 同时删除数据库、配置和 vpsmon 用户（不可恢复）
```

找不到原来的 `vpsmon-install.sh` 时，按“一键安装”一节重新下载并校验同一个脚本即可，卸载不依赖版本。

按第 1.3 节手动安装的：

```bash
sudo systemctl disable --now vpsmon-server
sudo rm -f /etc/systemd/system/vpsmon-server.service /usr/local/bin/vpsmon-server
sudo rm -rf /etc/systemd/system/vpsmon-server.service.d
sudo systemctl daemon-reload
# 以下会删除全部数据，请先确认已备份
sudo rm -rf /var/lib/vpsmon /etc/vpsmon
sudo userdel vpsmon
```

使用 Caddy 反向代理的，再从 Caddyfile 中删除面板的站点并 `sudo systemctl reload caddy`。

先卸载面板、后卸载 Agent 时，Agent 无法通知面板，但本地卸载照常完成。

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
