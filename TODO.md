# 开发计划（MVP）

按设计 35.1 的顺序开发：**阶段 A 接口 + Agent → 阶段 B Web → 阶段 C App**。
每一项大致对应一个 PR（设计 40.8.2）。完成后勾选 `[x]`，随提交同步。开始前先 `git pull`，确认另一台 Mac 没有在做。

代码中的 TODO 使用下面的里程碑编号，例如 `// TODO(A3): …（设计 21）`（设计 39.4）。

| 编号 | 内容 |
|---|---|
| M1 | 骨架（已完成） |
| A0～A7 | 阶段 A：接口 + Agent |
| B | 阶段 B：Web |
| C | 阶段 C：App 与 App 相关的服务端能力 |
| N1 / N2 / P2 | MVP 之后第一批 / 第二批 / 第二阶段（设计 36.1、36.2、36.3） |

## M1 — 骨架 ✅

- [x] Agent：Linux 真实采集 + 假数据采集，HTTPS 上报，SIGTERM 补报
- [x] Server：SQLite（WAL）、批量写入、内存实时状态、基于 boot_id 的流量增量
- [x] Web：服务器列表（状态、指标、本周期流量）
- [x] App：连接 + 服务器列表
- [x] Makefile、dev.sh、deploy.sh、install.sh、systemd 单元
- [x] test-server（greenJp）：systemd + Caddy HTTPS；test-agent（jp-store）（设计 40.3、40.4）

## 阶段 A — 接口 + Agent

完成标准（设计 35.1）：test-agent 按产品流程安装并稳定上报一周；接口测试全部通过；所有接口在契约中有定义。

### A0 工程基础

- [x] 设计文档 v1.2 与 `scripts/check_design_refs.py`（设计 40.9）
- [x] GitHub Actions CI：go vet/test、Web 类型检查与构建、设计文档校验、shellcheck、交叉编译全部架构（设计 40.8.3）
- [ ] CI 补充注释检查：revive exported 规则、eslint-plugin-jsdoc（设计 39.7）
- [x] `api/openapi.yaml` 契约骨架，覆盖现有接口（设计 19.0.1）
- [ ] 服务端测试校验响应与契约一致（设计 19.0.1）
- [x] 列表接口改为 `{"items", "next_cursor"}`，Web 与 App 同步修改（设计 19.0.2）；契约测试遍历全部列表接口
- [x] 统一错误响应 `{"error":{"code","message","request_id","details"}}`、`X-Request-ID`、panic 恢复中间件（设计 19.0.2、43.3、43.4）
- [x] `log/slog` JSON 日志、统一脱敏函数及测试（设计 24.3、24.7）
- [x] 路由默认拒绝：每个路由声明允许的主体；权限矩阵表驱动测试（设计 17.5）
- [x] `/healthz` 返回版本号（git describe）（设计 40.3.2）
- [x] 构建 amd64 / arm64 / armv7 / armv6 / 386 / riscv64 / mips / mipsle（设计 27.5.4、35.2）

### A1 节点与注册（核心）

- [x] 迁移：servers 增加 expected_* / verify_mode / enroll_state / machine_id_hash 与 VPS 信息字段（设计 18.2、1.2.3）；enroll_codes 表（设计 18.13）
- [x] `POST /api/v1/servers` 新建节点（待安装）+ 注册码；重新生成 / 撤销注册码（设计 19.11、27.4）
- [x] `POST /api/v1/agent/enroll`：一次性注册码换 Agent Token、信息核对、10 分钟重试幂等、单独限流（设计 27.6）
- [x] `vpsmon-agent install / uninstall / status`，`POST /api/v1/agent/unregister`（设计 27.6.1、27.11）
- [ ] 在 test-agent（jp-store）上按产品流程验证 install / status / uninstall（设计 40.4.3）
- [x] `scripts/agent.sh.in`：架构识别、内置 SHA256、试运行、`--download-only`（设计 27.5）；发布由 `cmd/vpsmon-release prepare` 生成（随 A7 完成）
- [x] 审计日志精简版 `audit_logs`：节点新建、注册码生成 / 撤销、Agent 注册（设计 18.16、24.8）
- [x] `vpsmon-server audit` 查看审计日志：按类别、结果、操作前缀筛选，`--json`（设计 24.8）
- [x] 重装 / 重试注册时吊销旧 Token（设计 27.6.4、27.8）
- [x] Web 吊销 Agent Token；`vpsmon-agent rotate-token --enroll` 凭新注册码就地更换（设计 17.2、23.2）
- [x] 节点查看 / 修改 / 删除：`GET/PUT/DELETE /api/v1/servers/{id}` 与 Web 编辑页（设计 19.5）

### A2 Web 登录（替换开发用 admin token）

- [x] 单管理员、Argon2id、会话 Cookie、CSRF、登录限流、强制修改初始密码、`admin reset-password`（设计 8.2、17.4、23.4）
- [x] 删除 `adm_` 开发 token 及其接口
- [x] 敏感操作重新输入密码：删除节点（设计 17.4）
- [x] 登录滑动拼图验证码（自建，服务端校验，设计 17.4）
- [x] App 改为 AK 配对后才能重新连接面板（开发 token 已删除，随阶段 C）

### A3 数据质量

- [x] 降采样 metrics_1m / 5m / 1h + 保留期清理（未聚合的数据不删）+ incremental vacuum；历史接口 /metrics/history（设计 21、18.4、19.7）
- [x] Agent 有上限的重试缓冲（180 份 / 30 分钟）+ 指数退避；401 停止上报；429 按 Retry-After；日志防刷屏；面板按采集时间入库补发数据（设计 1.6.14、24.5、43.5）
- [x] Agent 时钟偏差超过 60 秒时产生 Agent 异常提示：sent_at、默认规则 agent_clock（迁移 19）（设计 16.1、43.5）
- [x] 多挂载点磁盘（本地文件系统白名单、同设备去重）、磁盘 IO（/proc/diskstats）、IO 历史（设计 4.6、4.7）
- [ ] 在 test-agent 上核对多挂载点与 IO 数值（对照 df、iostat，设计 40.4.3）

- [x] 丰富指标：CPU 时间占比 / 每核 / 温度 / 型号、内存缓存、磁盘 IOPS / 耗时 / 繁忙、进程与连接数（设计 4.4～4.9）
- [ ] 在 test-agent 上核对新指标（对照 top、free、iostat -x、ss -s）
- [x] 历史曲线增加 steal / iowait、连接数（迁移 18）

### A4 流量

- [x] 计费模式、单位口径（GB / GiB）、统计系数（设计 1.2.4、5.7、5.8）
- [x] 手动校准 traffic_adjustments、校准历史（设计 5.7、18.12）
- [x] 流量预测：不足 3 天不预测、满 7 天用最近 7 天日均（设计 32）
- [x] 接口 traffic/current、daily、monthly、calibrate、adjustments（设计 19.8）
- [x] 按节点时区的计费日（迁移 21，改时区只影响之后的流量）
- [x] 多次校准偏差稳定时提示设置系数；校准以服务商数值为锚点，改系数 / 计费模式不重复修正（设计 5.7）
- [ ] 预测超限推送：Telegram / Webhook 已由告警规则 traffic_forecast 覆盖，App 推送随阶段 C

### A5 告警与通知

- [x] 告警引擎：默认规则、三层覆盖（rule_key）、状态机与回差、持久化与重启恢复、NODATA、依赖抑制、启动宽限期（设计 16.1～16.3、16.7）
- [x] 接口 /alerts、/alert-rules（只读）；节点列表带活动告警，“需要关注”以服务端为准；详情页告警记录
- [x] 规则编辑：全局阈值与开关、分组 / 节点覆盖、预览“会对几台节点触发”（设计 16.2）
- [x] 降噪：抖动检测、重复提醒、批量离线合并、面板自检（设计 16.3、16.4）
- [x] 告警页面：全部节点的活动与历史告警、规则标签、导航角标
- [x] 静音与维护模式：节点 / 分组 / 规则 / 全部，1h / 8h / 24h / 手动；维护不产生告警（设计 16.6、18.14）
- [x] Telegram / Webhook：触发 / 恢复 / 重复提醒、最低级别、测试通知、重试与投递记录（设计 16.5、18.15、31）
- [x] 免打扰时段：严重按设置、警告结束后汇总、提示不发，按渠道裁剪（设计 16.5）

### A6 部署与运维

- [x] 内置 HTTPS（ACME，`--domain`）（设计 25）；systemd drop-in deploy/systemd/vpsmon-server-https.conf
- [x] 请求日志记录真实客户端 IP：只信任回环代理的 X-Forwarded-For（设计 24.6、26）
- [x] `vpsmon-server backup / restore`、`diag`（设计 25、24.10）；Web 下载备份随系统设置页（阶段 B）

### A7 Agent 本地升级与发布

- [x] 发布流程：打标签 → Actions 生成草稿 Release → 离线 minisign 签名（scripts/sign-release.sh）→ 手动发布（设计 40.8.4、29.7）
- [x] 签名清单（internal/release）与安装脚本模板 scripts/agent.sh.in（设计 27.5、29.7.2）
- [x] `vpsmon-agent upgrade`：签名清单、防降级、健康检查与回滚（设计 29.7～29.12）
- [x] 开发者生成正式 minisign 密钥（current D576EB518998C46F + next 9A28D500E214987E），公钥已填入 internal/release/keys.go
- [x] v0.2.0：首个签名发布（2026-10-03）
- [x] 面板同步并验签官方发布，安装命令改为默认命令（下载 → 校验 → 执行）（设计 27.3.1、29.1）
- [x] 面板镜像与离线导入：--release-mirror、release import、/releases 下载（设计 27.5.3、29.1）
- [x] 远程升级：升级任务、Agent 轮询 /agent/upgrade、特权 updater（systemd path unit）、状态上报、节点详情升级按钮（设计 29.13、29.14）
- [x] 远程升级批量界面：“Agent 升级”页，按分组 / 全选批量升级、最近任务（设计 29.1、29.14）
- [x] 非 root 安装（用户模式）：家目录安装、systemd 用户服务或 crontab 保活、`vpsmon-agent keepalive`、运行锁（设计 27.13）
- [x] 386 构建改为软浮点，CI 在较老的 CPU 上运行各架构构建（设计 27.5.4）
- [ ] v0.3.0 发布后在 jp-store 实测：已安装节点先 `sudo vpsmon-agent enable-remote-upgrade`，再从面板升级（设计 40.4.2）

## 阶段 B — Web

完成标准（设计 35.1）：不使用命令行即可完成从新建节点到查看告警的全部操作；浅色 / 深色截图通过 41.7 检查。

- [x] 设计令牌 `design/tokens.json` → `web/src/styles/tokens.css`；自建组件（StatusDot、Metric、UsageBar、ServerCard、TrafficCard、Chart、EmptyState、CommandBlock）；深浅色（设计 41.2、41.3）
- [x] 请求类型由 OpenAPI 生成；统一错误处理（设计 19.0.1、43.6）
- [x] 登录页、修改密码页（设计 8.1）；窄屏顶栏保留账号入口
- [x] 节点卡片改为环形图（CPU / 内存 / 磁盘）+ 网速 + IO；详情页实时区块（CPU 占比与每核、内存分段、网络连接、磁盘 IO）（设计 11.1、41.3）
- [x] 日志页：登录日志、操作日志（只读、按结果筛选、分页），审计保留 1 年（设计 24.8）
- [x] 日志按主体 / 操作 / 日期筛选、CSV 导出；当前登录会话列表与踢出（设计 24.8）
- [x] 总览与节点列表合并为首页：统计行兼作状态筛选，去掉 Top N（修订第 30 条）
- [x] 总览；节点列表（搜索 / 分组 / 排序）；节点详情与历史曲线（ECharts、vue-router 已确认）（设计 9～11、41.5）
- [x] 图标改用 Lucide（@lucide/vue，设计 41.4.3）
- [x] 节点国家 / 地区与国旗（手动选择，flag-icons）
- [ ] 节点详情：健康摘要（设计 1.5.7）、告警记录（随 A5）
- [x] 新建节点表单 → 安装命令页（轮询显示注册结果）；待安装节点；重新生成注册码（设计 27.2、27.3.4）
- [x] 安装命令页改用 WebSocket `server.enrolled` 事件（设计 19.11、20）；/ws 已实现（标准库）
- [x] 节点列表改用 WebSocket 事件（server.metrics / online / offline，设计 20），替代 3 秒轮询
- [x] 流量套餐配置与校准（单位、系数、校准表单、30 天每日柱状图）
- [x] 校准历史列表、按周期的月度流量（节点详情“流量记录”）
- [x] 告警规则（全局默认 + 节点覆盖）、告警记录、静音
- [x] AK 与已连接设备管理（Web“App 接入”页，#86、#88）
- [x] 系统设置：通知渠道、免打扰（Relay 地址随阶段 C）

## 云厂商账户（设计 44，用户要求插入，阶段 B 之后、C 之前）

- [x] 第一步：凭证加密（secret.key）、账户管理接口与 Web 页面、同步任务；AWS Cost Explorer 费用、EC2 / Lightsail 实例、Lightsail 流量
  （待用真实 AWS 账户手动验证：Cost Explorer 返回值、Lightsail 流量与控制台一致）
- [x] 第二步：阿里云国内站与国际站：余额、账单概览、包年包月实例到期、轻量应用服务器流量包
  （待用真实阿里云账户手动验证：账单金额、轻量流量包与控制台一致；国际站账单月份的时区）
- [x] 第三步：腾讯云国内站与国际站：余额、按月账单、CVM 实例到期、轻量应用服务器流量包
  （待用真实腾讯云账户手动验证：完整签名、账单金额、余额权限与轻量流量包）
- [x] 第四步：Oracle Cloud 按月费用、出站数据量、实例
  （待用真实 OCI 账户手动验证：Usage API 金额、出站数据量的 skuName、多区间实例）
- [x] 第五步（一）：实例与节点关联、节点详情云厂商卡片、到期日写入节点
- [x] 第五步（二）：到期提醒、云账户提醒（费用超预算、流量包将用完、同步失败）
- [ ] 第五步（三）：余额不足提醒（需阈值设置）；可选的自动流量校准
- DMIT：没有公开 API，暂不接入

## 开放接口与 Agent 优化（设计 45、46，用户要求插入，参考 Komari）

- [x] 45 第一步：只读 API Key（api_ 前缀、节点范围、限流、审计）+ /api/v1/version；权限矩阵增加 API Key 主体
- [x] 45 第二步：WebSocket 事件 server.metrics / online / offline / alert.*；节点列表改为事件更新
- 45 第三步：公开状态页——用户确认暂不做
- [x] 46 第一步：虚拟化类型采集与显示
- [x] 46 第二步：按需实时模式（有人查看详情页时采样间隔临时降为 2 秒）
- 46 第三步：tcp / http 探测——用户确认暂不做（随第 42 章排期）

## 阶段 C — App

完成标准（设计 35.1）：真机扫码配对 test-server，收到端到端加密的离线告警推送。

- [x] AK 创建 / 吊销、配对接口、Device Token + Refresh Token、设备吊销（设计 8.4、12.3～12.7、19.2～19.4）；Web“App 接入”页
- [x] 配对二维码：浏览器本地生成（qrcode-generator，MIT，无依赖；用户已同意引入）
- [x] App 第一步：扫码 / 手工配对、凭证自动刷新与失效处理、节点列表（异常优先）、解除配对；CI 加入 flutter analyze / test
  （待真机验证：扫码配对 test-server）
- [x] App：节点详情、离线缓存、静音 / 维护操作（设计 13、14）
- [x] App：由 design/tokens.json 生成 Dart 令牌（app/lib/tokens.dart，CI 检查与 tokens.json 一致，设计 41.2）
- [ ] E2E 推送：配对时交换 X25519 公钥，HPKE 加密，iOS NSE / Android 数据消息（设计 30.3）
- [ ] push-relay：无状态、实例签名校验、内存限流（设计 30.2）

## MVP 之后

见设计第 36 章：第一批（N1）被墙检测与三网延迟、到期提醒、多监控中心、App 增强、批量新建；
第二批（N2）三网测速、UnifiedPush、审计完整版、远程升级与灰度；第二阶段（P2）服务探测、Docker、PostgreSQL 等。
