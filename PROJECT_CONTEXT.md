# PROJECT_CONTEXT — 新会话交接摘要

> 用途：开新会话时只贴这份摘要 + 当前问题，节省上下文。每完成一个 PR 后更新“当前状态”与“下一步”。
> 规则与约定以 `CLAUDE.md` 为准（安全不变量、提交流程、注释规范）；设计以 `docs/design.md` 为准；任务清单见 `TODO.md`。

## 项目一句话

RedPoint Monitor（vpsmon）：面向多 VPS 用户的自托管轻量监控。Go 单二进制面板（SQLite，内嵌 Vue 3 Web）+ 非 root 的 Go Agent，
App（Flutter）在阶段 C。开发顺序 A（API + Agent）→ B（Web）→ C（App）。

## 当前状态（2026-10-03，main = `8704a11` 之后）

已完成并合入 main：

| 范围 | 内容 |
|---|---|
| A1 注册 | 新建节点 → 注册码 ENR-… → `vpsmon-agent install` 注册换 agt_ Token；重装 / 卸载 |
| A2 登录 | Argon2id、HttpOnly 会话 Cookie + CSRF、二次验证、强制改初始密码、自建滑动拼图验证码 |
| A3 数据 | 断网缓冲补发、降采样（1m/5m/1h）、多挂载点与磁盘 IO；丰富指标：CPU 占比 / 每核 / 温度 / 型号、内存缓存、IOPS / 耗时 / 繁忙、进程、TCP/UDP/TIME_WAIT |
| A4 流量 | 计费模式、GB/GiB 口径、统计系数、手动校准（只作用于当前周期）、预测（不足 3 天不预测，满 7 天用近 7 天日均） |
| A7 第二步 | 面板同步并验签官方版本（迁移 12），安装命令为一行“下载 → 校验哈希 → 执行”；v0.2.0 已签名发布 |
| A7 第一步 | 签名发布链路：internal/release（minisign 验签、清单、版本比较、官方公钥）、cmd/vpsmon-release、release.yml 草稿、scripts/sign-release.sh 离线签名；安装脚本 agent.sh.in；`sudo vpsmon-agent upgrade`（验签、防降级、健康检查、回滚）。官方公钥已填入（current D576EB518998C46F，next 9A28D500E214987E；私钥只在开发者 Mac 的 ~/.minisign，next 应离线备份后移走） |
| A5 第三步 | 静音与维护（迁移 11）：静音照常记录、只标记；维护节点不评估；详情页按钮与横幅，告警页“静音”标签 |
| A5 第二步 | 规则编辑：PUT / POST（分组、节点覆盖）/ DELETE / preview；Web “告警”页（告警 / 规则标签，导航角标） |
| A5 第一步 | 告警引擎：迁移 10 默认规则（rule_key 三层覆盖）、状态机 + 回差、firing 持久化与重启恢复、NODATA、离线依赖抑制、启动宽限期；`GET /alerts`、`GET /alert-rules`；“需要关注”以服务端告警为准；详情页告警记录 |
| Web | 首页 = 节点列表（统计行即筛选，无 Top N）；NeoServer 风格卡片（CPU/Mem/Disk/Net/I/O，周期流量）；详情页 = Monito 概况 + 速览 + ServerCat 指标块（每块左上角名称：CPU / Mem / Net / Disk）+ 流量卡 + 告警记录 + 历史；日志页（登录 / 操作）；节点表单只收 Agent 采集不到的字段（含带宽） |

数据库迁移到第 12 号。设计修订记录到第 36 条。发布新版本：打 vX.Y.Z 标签 → 草稿 → Mac 上 scripts/sign-release.sh vX.Y.Z。仓库已公开，官方发布地址为 GitHub Releases。

## 下一步

1. A7 第三步：远程升级（升级任务、Agent 轮询、特权 updater、健康检查与回滚、Web 页面）
4. A5 剩余：Telegram / Webhook 通知与投递记录；重复提醒、批量离线合并、抖动检测、面板自检

其他待办：在 jp-store 上核对新指标（对照 top、free、iostat -x、ss -s）；A6 内置 HTTPS / 备份；A7 签名发布与 Agent 升级。

## 工作方式（用户偏好）

- 用户说“提交 / 合并”= 等 CI 通过后 squash 合并 PR；说“继续”= 做 TODO.md 中的下一项。
- 每个 PR：先改 `api/openapi.yaml` 与 `docs/design.md`（加修订记录行，跑 `python3 scripts/check_design_refs.py --write`），
  再改代码；完成前 `make test && make lint && make check-design`，界面改动要在浏览器里看（375px + 深色）。
- 不要叠 PR；如果叠了，合并父 PR 时不要 `--delete-branch`，先把子 PR 改到 main 并变基（#18 曾因此被自动关闭）。
- 本机端口 8080 常被用户自己的 `make dev` 占用：临时测试面板用 `127.0.0.1:18080`，数据放在会话的 scratchpad 目录。
- 浏览器验证尽量在后台新标签页中做，不要占用用户正在看的标签页。
- 界面参考：列表 NeoServer、详情 Monito + ServerCat；数值等宽字体、单位小一号（Qty 组件）；列表列标题用英文 CPU / Mem / Disk / Net / I/O。

## 测试环境

- greenJp：面板（systemd + Caddy 反代 HTTPS；Caddy 只开 h1/h2）。更新：`git pull && make build && make install-server`；
  未部署过 A2 时再执行 `sudo -u vpsmon vpsmon-server admin reset-password --data /var/lib/vpsmon`。
- jp-store：Agent 节点，需要换新版 `vpsmon-agent` 才有 A3 新指标。
- SSH 端口与密钥等细节见本机记忆，不写进仓库。
