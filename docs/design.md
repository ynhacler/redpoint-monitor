# 多服务器监控平台详细设计文档

> 文档版本：v1.1  
> 适用范围：Linux VPS / 云服务器 / 物理服务器监控  
> 产品形态：Agent + 中央服务端（单二进制）+ 内嵌 Web 管理端 + iOS/Android App + 可选 Push Relay  
> 推荐技术栈：Go + SQLite（默认）/ PostgreSQL（可选）+ Vue 3 + Flutter

---

## 修订记录

### v1.1 主要变更

| # | 变更 | 涉及章节 |
|---|---|---|
| 1 | Agent 发布签名私钥改为**开发者离线保管**，monitor-server 只分发官方签名版本，不具备签名或上传二进制的能力 | 1.6.8、29.1、29.7、29.18、29.21 |
| 2 | 明确升级权限模型：Agent 以非 root 运行，替换二进制由 systemd 触发的特权 updater 完成，updater 独立复验签名并防降级；节点本地可关闭远程升级 | 1.6.9、28、29.13 |
| 3 | 原生 Push 改为**默认启用端到端加密的无状态 Relay**，Relay 开源；对外表述由“不经过开发者”改为“开发者看不到内容”；Android 增加 UnifiedPush / ntfy 完全自持选项 | 1.1.1、1.6.12、1.6.18、15、18.11、30 |
| 4 | 默认数据库改为 **SQLite（WAL）**，PostgreSQL 作为可选后端，移除 TimescaleDB / Redis 依赖；服务端改为单二进制内嵌 Web，Docker 可选 | 2.1、3.2、3.5、18.4、21、25、37 |
| 5 | 安装：带签名校验的手动安装与一键脚本同等主推；脚本只从官方发布地址获取 | 27 |
| 6 | 流量统计：引入 boot_id 识别重启、退出前补报、网卡默认过滤、**手动校准已用量**、GB/GiB 口径设置 | 5.5～5.8、6.2、18.12 |
| 7 | v1 App 只读，仅允许静音 / 维护模式等低风险操作；AK 不再提供“管理”权限 | 8.4.1、12.5、19.2、23.3 |
| 8 | Widget 明确受系统刷新预算限制，展示“最后更新时间”，不承诺秒级 | 1.5.4 |
| 9 | 多监控中心统一为核心能力（v1 支持多中心切换，聚合视图放第二阶段） | 1.5.2、12.8 |
| 10 | 新增商业模式章节 | 1.12 |
| 11 | 重新划定第一阶段（MVP）与第二阶段范围；灰度升级、Widget、多中心聚合移至第二阶段 | 35、36 |
| 12 | 修正章节编号错乱（33.x / 32.x / 34.x） | 32～34 |

---

## 1. 项目概述

### 1.1 项目背景

随着服务器数量增加，单独登录服务器查看 CPU、内存、磁盘、网络流量、在线状态等信息效率较低，且无法形成统一的历史趋势、告警和资产视图。

本项目拟建设一套轻量级多服务器监控平台，通过在各服务器部署低资源占用的 Agent，由 Agent 定期采集系统状态并主动上报至中央服务端。中央服务端负责数据存储、历史聚合、告警判断、设备管理和 API 服务；Web 管理端用于完整管理；移动 App 用于快速查看状态、历史趋势和接收告警。


### 1.1.1 核心隐私承诺

```text
用户保存自己的数据
开发者不保存用户数据
App 直接连接用户自己的 monitor-server
原生 Push 端到端加密，开发者看不到内容
默认无遥测
安全可靠优先
```

本项目从架构层面避免形成集中式用户监控数据平台。

唯一会经过开发者基础设施的是原生 Push（iOS APNs 必须使用开发者凭证发送，见第 30 章）。对此不做模糊表述：Push 经过开发者运营的无状态 Relay，但内容在用户的 monitor-server 上端到端加密，Relay 只能看到密文和一次性投递目标，不落库、不记日志，且代码开源可审计。

---

### 1.2 建设目标

系统应满足以下目标：

- 支持多服务器集中管理。
- 支持 Linux 服务器轻量 Agent。
- 支持 CPU、内存、磁盘、网络、负载、运行时间等基础监控。
- 支持实时上传/下载速率。
- 支持日流量、月流量、计费周期流量统计。
- 支持历史趋势查询。
- 支持服务器在线/离线检测。
- 支持多级告警。
- 支持 Web 管理端登录。
- 支持 iOS 和 Android App。
- App 支持通过 Web 生成 AK 后扫码或手工填写完成配对，无需用户名密码登录。
- 支持 App 设备独立授权与吊销。
- 支持 App Push 告警。
- 支持 Telegram、Webhook 等扩展通知渠道。
- 支持服务器分组、标签、地区、供应商、套餐等资产信息。
- Agent 对低配置 VPS 资源影响应尽可能低。
- 后续可扩展 Docker、进程、端口、SSL、Ping、丢包、资产到期等功能。

---


## 1.2.1 目标用户：VPS 爱好者

核心用户：

```text
拥有多台 VPS 的个人用户
VPS / 主机 / 网络爱好者
自建服务用户
代理 / 中转 / 落地节点维护者
轻量 Homelab 用户
```

典型特点：

```text
服务器数量多
分布在香港、日本、新加坡、美国、欧洲等地区
供应商多
套餐流量不同
续费周期不同
经常使用低配 VPS
手机查看频率高
非常关注网络质量和流量
```

因此本项目不追求“大而全”，而追求：

```text
轻
快
省资源
App 好用
多 VPS 管理方便
流量和线路信息直观
```

---

## 1.2.2 VPS 爱好者最关心的信息

第一优先级：

```text
在线 / 离线
CPU
内存
磁盘
实时上传 / 下载
月流量
套餐流量额度
流量重置日
剩余流量
IPv4 / IPv6
地区
供应商
延迟
丢包
续费日期
续费价格
```

第二优先级：

```text
Docker 状态
端口 / HTTP 可达性
SSL 到期
Agent 版本
历史趋势
```

非核心方向：

```text
企业 CMDB
ITIL
工单
审批
复杂 RBAC
大型拓扑
SIEM
APM
硬件服务器深度监控
```

---

## 1.2.3 VPS 资产模型

每台 VPS 可记录：

```text
名称
供应商
套餐
国家 / 地区 / 城市
机房
IPv4
IPv6
ASN

CPU
内存
磁盘
带宽
月流量
流量重置日

购买价格
续费价格
币种
计费周期
购买日期
到期日期

标签
备注
```

---

## 1.2.4 VPS 流量是一级功能

流量与 CPU、内存、磁盘同级。

支持：

```text
本周期已用
套餐总量
剩余流量
使用百分比
距离重置天数
日均流量
预计周期结束用量
```

计费模式：

```text
RX + TX
仅 RX
仅 TX
MAX(RX, TX)
不限流量
```

---

## 1.2.5 VPS 到期与续费

支持：

```text
到期日期
续费价格
计费周期
自动续费标记
```

提醒：

```text
30 天
14 天
7 天
3 天
1 天
```

---

## 1.2.6 多地区线路体验

支持：

```text
Agent → 公网目标
Agent → Agent
```

指标：

```text
Latency
Packet Loss
TCP Connect
HTTP Response
IPv4 / IPv6 Reachability
```

典型用途：

```text
香港 → 日本
香港 → 新加坡
日本 → 美国
```

---

## 1.2.7 NAT / IPv6-only / 低配 VPS

必须友好支持：

```text
NAT VPS
IPv6-only VPS
动态 IP
多网卡
256MB / 512MB / 1GB VPS
```

Agent 主动上报，因此不要求中心主动访问节点。

Agent 目标：

```text
低 CPU
低内存
低磁盘 IO
低上报流量
单文件
无额外运行时依赖
```

---

## 1.2.8 产品取舍

优先做：

```text
VPS 状态
VPS 流量
VPS 网络
VPS 到期
App Push
App Widget
多监控中心
轻量 Agent
Docker 基础监控
端口 / HTTP / SSL 检测
```

暂不做：

```text
复杂企业 CMDB
ITIL
工单审批
大型组织权限体系
SNMP 网络设备管理
SIEM
APM
GPU 深度监控
RAID / ZFS 深度监控
SMART 深度硬盘监控
复杂硬件传感器
```

产品定位：

> 面向 VPS 爱好者的 App-first 轻量自托管监控平台。

---

## 1.3 产品定位：App-first

本项目可以参考 Komari 等轻量自托管监控产品的成熟思路，包括：

```text
轻量 Agent
实时指标
历史趋势
自托管
节点管理
Agent 自动升级
Web 管理
```

但本项目不以复制现有产品界面、交互、代码结构或数据模型为目标。

核心差异化定位：

```text
Web = 配置与管理后台
App = 日常监控主入口
```

即：

```text
传统轻量监控：
Agent → Server → Web Dashboard

本项目：
Agent → Server
          ├── Web 管理后台
          └── iOS / Android App（核心体验）
```

产品目标不是单纯做“另一个 Web 服务器探针”，而是打造：

```text
面向多 VPS / 多服务器用户的
移动端优先服务器监控平台
```

---

## 1.4 与传统 Web-first 监控的差异

| 能力 | 传统 Web-first 监控 | 本项目 |
|---|---|---|
| 主要使用入口 | 浏览器 | App |
| Web | 监控 + 管理 | 重点负责管理 |
| iOS / Android | 辅助或无 | 核心产品 |
| 接入 | Web 登录 | Web 生成 AK，App 扫码/填写 |
| 实时状态 | 浏览器查看 | App 实时查看 |
| 离线告警 | 通知渠道 | 原生 Push |
| 多套监控中心 | 较弱 | App 原生支持 |
| 手机桌面组件 | 通常无 | 重点支持 |
| 移动端交互 | 响应式网页 | 原生移动体验 |
| 流量套餐 | 基础统计 | 套餐、重置日、预测、预警 |
| VPS 资产 | 一般较弱 | 监控 + 资产一体化 |
| Agent 升级 | 支持或手工 | 离线签名 + 特权分离 + 自动回滚（灰度为第二阶段） |
| 部署 | 不一 | 单二进制 + SQLite，一条命令启动 |
| 日常巡检 | 登录网页 | App 首页直接完成 |

---

## 1.5 App 核心竞争力

App 端必须成为本项目最有辨识度的部分。

### 1.5.1 打开即状态

用户打开 App 后，不进入复杂 Dashboard，首先看到：

```text
全部服务器：26

🟢 在线 24
🔴 离线 1
🟠 告警 1

需要关注
──────────────────
Oracle-SG    🔴 离线 4 分钟
Zoro-JP      🟠 流量 92%
DMIT-HK      🟠 磁盘 87%

其他服务器
──────────────────
DMIT-US      🟢
HK-NAT       🟢
JP-01        🟢
...
```

设计原则：

```text
异常优先
状态优先
操作少
信息密度高
```

---

### 1.5.2 多监控中心

App 原生支持同时连接多套自建后台。

例如：

```text
监控中心

个人 VPS
monitor.example.com
26 台服务器

家庭网络
home-monitor.example.com
5 台服务器

实验环境
lab.example.com
12 台服务器
```

不同后台：

```text
独立 Device Token
独立权限
独立 Push
独立服务器列表
```

App 可以提供：

```text
全部监控中心聚合视图
```

例如：

```text
总计 43 台
在线 41
离线 1
告警 1
```

这是 App-first 架构的重要能力。

分阶段实现：

```text
第一阶段：数据模型按多中心设计，App 可添加多个监控中心并切换，凭证与 Push 相互隔离
第二阶段：全部监控中心聚合视图、跨中心统一事件流
```

---

### 1.5.3 原生 Push 告警

App Push 是一级功能，而不是外部通知的附属功能。

支持：

```text
服务器离线
服务器恢复
CPU 高负载
内存不足
磁盘不足
Swap 异常
月流量预警
网络流量异常
Agent 版本异常
SSL 即将过期
服务不可用
```

通知支持直接跳转：

```text
Push
 ↓
指定监控中心
 ↓
指定服务器
 ↓
指定告警详情
```

---

### 1.5.4 手机桌面 Widget

iOS 和 Android 均应规划桌面组件。

小组件：

```text
🟢 25
🔴 1
🟠 2

服务器状态正常
```

中组件：

```text
DMIT-HK      🟢
CPU  18%
RAM  43%
NET  ↓12M ↑2M
```

大组件：

```text
服务器     状态    CPU    RAM

DMIT-HK    🟢      18%    43%
Zoro-JP    🟢       9%    31%
Oracle-SG  🔴       --     --
US-DMIT    🟢      27%    51%
```

Widget 数据应由 App 缓存更新，不允许 Widget 直接长期保存 App AK。

刷新频率受系统限制，不能承诺秒级：

```text
iOS WidgetKit：系统分配刷新预算，通常每 15～60 分钟一次
Android：updatePeriodMillis 最小 30 分钟，WorkManager 周期最小 15 分钟
```

因此：

```text
Widget 定位为“概览”，不是实时仪表盘
每个 Widget 显示“最后更新：HH:mm”
收到告警 Push 时，App 顺带刷新 Widget 缓存（iOS 通过 Notification Service Extension 触发 WidgetCenter 刷新）
点击 Widget 进入 App 获取实时数据
```

Widget 放在第二阶段实现。

---

### 1.5.5 收藏服务器

服务器可设置：

```text
⭐ 收藏
```

App 首页支持：

```text
收藏
异常
全部
```

用户服务器很多时，无需每次翻几十台机器。

---

### 1.5.6 智能排序

服务器列表不仅按名称排序，还支持：

```text
异常优先
离线优先
CPU 使用率
内存使用率
磁盘使用率
流量使用率
实时下载
实时上传
地区
供应商
手工排序
```

默认推荐：

```text
异常 > 离线 > 收藏 > 普通在线
```

---

### 1.5.7 服务器健康摘要

App 不仅展示原始数字，还生成简单健康摘要。

例如：

```text
DMIT-HK

状态：正常

CPU：
过去 24 小时平均 12%
峰值 68%

内存：
长期稳定在 41%～48%

磁盘：
已使用 71%
最近 30 天增长 4.2 GB

流量：
本周期已使用 638 GB / 1000 GB
预计周期结束 891 GB
```

避免用户逐张图判断。

---

### 1.5.8 流量套餐体验

针对 VPS 用户强化：

```text
本周期
638 / 1000 GB

剩余
362 GB

距离重置
12 天

日均
21.8 GB

预计周期结束
891 GB
```

支持：

```text
80%
90%
95%
100%
```

流量阈值 Push。

支持不同计费模型：

```text
RX + TX
仅 RX
仅 TX
MAX(RX, TX)
不限流量
```

---

### 1.5.9 App 实时模式与省电模式

App 不需要永久保持秒级连接。

提供两种状态：

```text
普通模式
列表 10～30 秒刷新

实时模式
进入某台服务器详情后
使用 WebSocket 秒级更新
```

退出详情页后自动停止高频实时订阅。

这样能够：

```text
降低手机耗电
减少移动网络流量
降低服务端 WebSocket 压力
```

---

### 1.5.10 离线缓存

App 保存最近一次监控状态：

```text
服务器列表
最后指标
历史图表缓存
最近告警
```

如果网络不可用：

```text
当前离线

最后更新：
16:32:18
```

而不是整个页面无法使用。

---

### 1.5.11 生物识别锁

支持：

```text
Face ID
Touch ID
Android 生物识别
```

可设置：

```text
打开 App 时验证
进入敏感管理功能时验证
```

即使手机已解锁，也可保护服务器信息。

---

### 1.5.12 隐私模式

App 支持隐藏敏感信息：

```text
公网 IP
IPv6
服务器价格
供应商信息
域名
```

适合截图或向他人展示 App 时使用。

例如：

```text
103.***.***.12
```

---

### 1.5.13 App 快捷操作

服务器卡片长按：

```text
查看详情
收藏
复制 IP
静音告警
查看流量
```

不建议第一版加入：

```text
任意远程 Shell
任意远程命令
```

监控 App 与远程控制工具保持安全边界。

---

### 1.5.14 告警静音

支持：

```text
静音 1 小时
静音 8 小时
静音 24 小时
直到手工恢复
```

例如维护服务器时：

```text
DMIT-HK
维护中
告警静音 2 小时
```

避免重复 Push。

---

### 1.5.15 维护模式

Web 或 App 可将服务器设置为：

```text
Maintenance
```

期间：

```text
继续采集数据
继续保存历史
不发送普通离线告警
```

维护结束后恢复正常。

---

### 1.5.16 App 端全局搜索

搜索：

```text
服务器名称
IP
供应商
地区
标签
备注
```

例如输入：

```text
香港
```

立即过滤所有香港节点。

---

### 1.5.17 App 端统一事件中心

不单独只做“告警列表”，而是：

```text
事件中心
```

包括：

```text
服务器离线
服务器恢复
指标告警
流量预警
Agent 升级
升级失败
升级回滚
证书到期
服务异常
```

时间线：

```text
16:31 Oracle-SG 离线
16:29 JP-01 Agent 升级成功
16:21 Zoro-JP 流量达到 90%
15:42 DMIT-HK CPU 告警恢复
```

---

### 1.5.18 App-first 首页导航

推荐底部导航：

```text
首页
服务器
事件
我的
```

其中：

```text
首页
  状态总览 + 异常 + 收藏

服务器
  全部节点 + 分组 + 搜索

事件
  告警 + 恢复 + 升级 + 系统事件

我的
  监控中心 + Push + 外观 + 安全
```

Web 才负责：

```text
用户
Agent Token
App AK
版本发布
升级任务
告警规则
系统配置
```

---



## 1.6 隐私、安全与可靠性原则

本项目将“用户数据由用户保存、开发者不保存”作为一级产品原则，而不是可选功能。

核心承诺：

```text
用户的服务器信息属于用户
用户的监控数据属于用户
用户的连接凭证属于用户
用户的 App 配置属于用户

开发者不建立集中式用户数据库
开发者不托管用户服务器清单
开发者不保存用户监控历史
开发者不保存用户 AK / Device Token
开发者不保存用户服务器 IP、域名、ASN、供应商、价格等信息
```

产品架构应做到：

```text
用户服务器
    │
    ▼
用户自建 monitor-server
    │
    ├── 用户自己的 Web
    └── 用户自己的 App
             │
             ▼
       本机安全存储

开发者业务后台
    ×
不进入日常监控数据链路
```

也就是说，App 与用户自己的监控服务直接通信：

```text
App  <──── HTTPS / WebSocket ────>  用户自建 monitor-server
```

而不是：

```text
App → 开发者云 → 用户服务器
```

---

### 1.6.1 App 信息只保存在用户设备

App 本地保存：

```text
监控中心地址
设备 ID
Device Access Token
Refresh Token
收藏
分组展示偏好
排序
主题
隐私模式
最近状态缓存
最近历史图表缓存
最近事件缓存
```

敏感信息：

```text
Access Token
Refresh Token
设备密钥
本地加密密钥
```

必须保存至：

```text
iOS Keychain
Android Keystore
```

普通配置才允许保存至：

```text
本地数据库
SharedPreferences
```

严禁：

```text
将 AK / Token 写入普通日志
将 AK / Token 上传到开发者后台
将 AK / Token 写入 Crash Report
将 AK / Token 长期放入剪贴板
将完整凭证写入明文 SQLite
```

---

### 1.6.2 monitor-server 数据只由用户保存

以下数据只保存在用户自己部署的服务器：

```text
VPS 清单
服务器 IP / IPv6
ASN
供应商
套餐
价格
续费日期
监控指标
历史曲线
流量统计
流量额度
告警规则
告警事件
Agent Token
App AK
App 设备授权
Agent 升级信息
备份
```

开发者不提供强制 SaaS 数据库。

用户可选择：

```text
完全离线内网部署
公网自托管
家庭服务器部署
VPS 部署
```

---

### 1.6.3 开发者不需要知道用户有多少台 VPS

默认情况下，开发者无法获知：

```text
用户有多少台服务器
服务器在哪个国家
服务器 IP
服务器供应商
服务器价格
服务器流量
服务器性能
服务器告警
服务器是否在线
```

App 不建立开发者侧用户画像。

默认不集成：

```text
广告 SDK
行为分析 SDK
第三方埋点 SDK
设备画像 SDK
```

如果未来引入崩溃分析：

```text
必须默认关闭或明确征得用户同意
必须进行敏感字段脱敏
不能上传服务器信息和认证凭证
```

---

### 1.6.4 AK 只用于配对

Web 生成的 AK：

```text
不是长期 API 密钥
```

它只用于：

```text
首次 App 配对
```

流程：

```text
Web 生成 AK
    ↓
App 扫码 / 输入
    ↓
monitor-server 验证
    ↓
生成 Device Token
    ↓
AK 失效或减少剩余配额
```

推荐默认：

```text
一次性 AK
最大设备数 = 1
短有效期
```

数据库中只保存：

```text
AK Hash
```

不保存可恢复的完整 AK。

---

### 1.6.5 设备级授权和撤销

每一台 iPhone / Android：

```text
独立 Device Token
独立 Refresh Token
独立设备记录
```

Web 可以单独吊销：

```text
Tom's iPhone
Pixel
iPad
```

吊销后立即失效：

```text
REST API
WebSocket
Token Refresh
Push
```

而不影响其他设备。

---

### 1.6.6 三套凭证完全隔离

安全边界：

```text
Web 管理员凭证
≠
App Device Token
≠
Agent Token
```

权限范围：

```text
Web Token
管理配置

App Token
查看授权范围内数据

Agent Token
仅上报自己节点数据
```

任何一种 Token 泄露，都不应自动获得其他角色的权限。

---

### 1.6.7 全链路加密

强制：

```text
HTTPS
WSS
TLS 1.2+
```

推荐：

```text
TLS 1.3
```

禁止生产环境：

```text
明文 HTTP
忽略证书错误
自动信任自签名证书
```

如用户使用自签名证书：

```text
App 必须显式提示
由用户主动信任
```

不能静默跳过证书校验。

---

### 1.6.8 Agent 安全边界

Agent 必须坚持：

```text
只采集
只上报
只接收受限配置
只安装签名版本
```

禁止设计：

```text
任意远程 Shell
任意命令执行
任意文件执行
Web 后台直接执行 Bash
```

远程升级只能执行：

```text
官方签名版本
SHA256 校验
Ed25519 签名校验（公钥内置于 Agent 与 updater）
OS / Arch 匹配
禁止降级
```

关键约束：**签名私钥由开发者离线保管，永远不进入用户的 monitor-server。**

```text
开发者离线签名机（或硬件密钥）
    │ 签发 release manifest
    ▼
官方发布地址（GitHub Releases 等）
    │
    ▼
monitor-server（只同步 / 缓存 / 分发，无签名能力）
    │
    ▼
Agent + updater（用内置公钥校验）
```

这样即使 monitor-server 整机被攻破，攻击者也只能：

```text
触发升级到已存在的官方版本
```

而不能：

```text
签发或分发任意二进制
降级到存在漏洞的旧版本
通过 Agent 在节点上执行代码
```

监控面板被攻破不会扩散为全部节点被控，这是本项目安全设计的底线。

---

### 1.6.9 Agent 最小权限运行

默认尽可能：

```text
非 root
```

基础指标：

```text
CPU
Memory
Load
Disk
Network
Uptime
```

尽量通过：

```text
/proc
/sys
标准系统调用
```

读取。

只有可选功能：

```text
SMART
Raw ICMP
部分硬件传感器
```

才按需增加权限。

使用：

```text
Linux Capability
```

而不是长期 root。

自我升级需要写入 `/usr/local/bin` 并重启服务，这与非 root 运行冲突。解决方式是特权分离：

```text
monitor-agent（用户 monitor-agent，非 root）
  只负责：检查更新、下载、首次校验、写入升级请求

monitor-agent-updater（root，由 systemd path unit 按需触发）
  只负责：独立复验签名、防降级检查、原子替换、重启、健康检查、回滚
```

updater 不信任 Agent 的校验结果，必须用自己内置的公钥重新校验。详见 29.13。

节点本地拥有最终控制权：

```yaml
upgrade:
  remote: true   # 设为 false 后，面板无法触发该节点升级
```

该开关只能在节点本地修改，不能由面板远程下发。

---

### 1.6.10 App 生物识别保护

App 可启用：

```text
Face ID
Touch ID
Android Biometrics
```

保护：

```text
打开 App
查看完整 IP
查看敏感资产信息
管理监控中心
```

用户可以开启：

```text
隐私模式
```

自动隐藏：

```text
IPv4
IPv6
域名
价格
供应商
备注
```

适合截图和公共场合使用。

---

### 1.6.11 本地缓存可控

App 提供：

```text
清除缓存
删除某个监控中心
删除所有本地数据
```

删除监控中心时：

```text
撤销设备授权
删除 Token
删除本地缓存
删除本地配置
```

不留下残留凭证。

---

### 1.6.12 Push 隐私：端到端加密

原生 Push 是 App 的核心卖点，因此**默认启用**，而不是藏在可选项里。

iOS 推送必须使用开发者的 APNs 凭证，所以官方 App 的原生 Push 无法绕开开发者运营的 Relay。本项目正面处理这一点：

```text
告警内容在用户的 monitor-server 上加密
Relay 只看到：密文 + 一次性投递目标
App 在本机解密并展示
```

Relay 保证：

```text
不落库
不记录请求 Body / Push Token / 来源地址
不需要用户账号
代码开源，可审计、可自建
```

完全不想经过开发者的用户，可以选择：

```text
Telegram / Webhook / ntfy / Email（monitor-server 直接发送）
Android：UnifiedPush / 自建 ntfy 作为 App 的推送通道（无需 FCM）
iOS：自建 Relay + 自签 App（需要自己的 Apple 开发者账号）
```

详见第 30 章。

---

### 1.6.13 更新机制必须安全可靠

Agent 更新必须：

```text
HTTPS 下载
SHA256
Ed25519 签名
版本白名单
平台匹配
健康检查
失败回滚
升级审计
```

升级失败：

```text
自动恢复旧版本
```

不能因为更新：

```text
导致 Agent 永久离线
```

---

### 1.6.14 数据可靠性

监控数据写入应支持：

```text
事务
批量写入
异常重试
时间戳校验
重复数据去重
```

Agent 网络中断：

```text
不崩溃
继续运行
恢复后继续上报
```

可选择保留少量内存 / 本地短缓存：

```text
最近若干分钟指标
```

避免短暂网络中断造成明显历史断层。

缓存必须有：

```text
大小上限
时间上限
```

不能无限增长。

---

### 1.6.15 monitor-server 故障恢复

服务端必须支持：

```text
配置备份
数据库备份
快速恢复
健康检查
数据库迁移
版本回滚
```

建议：

```text
/healthz
/readyz
```

备份由用户保存到：

```text
本地
NAS
用户自己的 S3
```

开发者不接收备份。

---

### 1.6.16 最小化日志

日志不得包含：

```text
完整 AK
完整 Agent Token
完整 Device Token
Refresh Token
用户密码
完整 Authorization Header
```

API 日志仅记录：

```text
请求时间
接口
状态码
耗时
匿名设备 ID
```

涉及敏感参数时：

```text
自动脱敏
```

---

### 1.6.17 默认安全配置

系统安装完成后应默认：

```text
HTTPS 推荐
强密码要求
一次性 AK
最小权限
Token 独立
无遥测
无广告 SDK
无第三方用户画像
Agent 无远程 Shell
升级签名验证
日志脱敏
```

而不是让用户自行逐项开启安全功能。

安全原则：

```text
Secure by Default
Privacy by Default
```

---

### 1.6.18 对外产品说明

产品介绍中建议明确写：

> **你的服务器数据，只属于你。**
>
> App 直接连接你自己部署的监控服务。服务器信息、监控指标、流量、IP、AK 和设备授权均由你自己保存，开发者不建立集中式监控数据平台。

并补充：

> **默认无遥测、无广告、无用户画像。**
>
> 监控数据、Web、App 与 Agent 之间的日常链路不经过开发者服务器。唯一的例外是原生推送：推送内容在你的服务器上端到端加密，经过开源的无状态中继转发，开发者看不到内容，也不保存任何记录。

以及：

> **安全可靠优先。**
>
> 监控面板被攻破，也不会让你的节点被控制：Agent 只安装由开发者离线签名的官方版本，面板本身没有签名能力；Agent 以非 root 运行，升级由独立的 updater 复验签名并支持自动回滚；Web、App、Agent 三类凭证相互隔离，可独立撤销。

对外文案禁止使用“完全不经过开发者”“零接触”等绝对表述，避免与原生 Push 的实际链路矛盾。

这一原则应成为官网、README、App 隐私说明和产品介绍中的核心信息。

---


## 1.7 不依赖开发者账号

App 默认不要求：

```text
注册开发者账号
手机号
邮箱
开发者云账号
开发者 OAuth
```

首次使用只需要：

```text
用户自己的监控服务地址
+
用户自己的 Web 生成的 AK
```

配对完成后：

```text
App ↔ 用户自己的 monitor-server
```

日常使用不经过开发者业务服务器（原生 Push 经 E2E 加密 Relay，见第 30 章）。

App 内购（见 1.12）使用 StoreKit / Google Play Billing 的本地凭证校验，同样不需要开发者账号。

---

## 1.8 禁止默认遥测

默认关闭：

```text
用户行为统计
服务器数量统计
IP 上传
设备指纹
使用时长统计
监控指标上传
崩溃日志自动上传
第三方广告 SDK
第三方用户画像 SDK
```

如未来需要 Crash Reporting，应采用：

```text
用户明确选择开启
+
提交前展示将发送的信息
+
不得包含服务器地址、AK、Token、IP、监控指标
```

第一版建议：

```text
完全不接入第三方分析 SDK
```

---

## 1.9 App 数据导出与删除

App 提供：

```text
清除本地缓存
清除当前监控中心
删除本地全部数据
```

删除某个监控中心：

```text
1. App 调用用户自己的 monitor-server 撤销当前设备
2. 删除本地 Access Token
3. 删除本地 Refresh Token
4. 删除本地缓存
5. 删除本地连接配置
```

用户也可仅执行：

```text
删除本机配置
```

不影响服务端其他设备。

---


## 1.10 借鉴 Beszel 的能力设计

Beszel 值得借鉴的不是“做得更重”，而是其轻量 Hub + Agent 思路。

本项目只吸收与 VPS 爱好者高度相关的能力：

```text
轻量 Agent
历史指标
Docker / Podman 基础监控
网络探测
告警
REST API
Agent 多种安装方式
服务健康检查
```

不作为主线功能：

```text
复杂多用户 / SSO
企业权限体系
GPU 深度监控
SMART 深度监控
ZFS / RAID 深度监控
复杂硬件传感器
企业备份体系
```

### 1.10.1 Docker / Podman

支持：

```text
容器名称
状态
CPU
内存
网络 RX / TX
Restart Count
```

不提供：

```text
远程 Shell
任意 Docker 命令
```

### 1.10.2 网络探测

支持：

```text
ICMP
TCP
HTTP / HTTPS
DNS
```

指标：

```text
延迟
丢包
连接时间
HTTP 状态码
响应时间
```

多个 VPS 可以测试同一个目标，用于对比线路质量。

### 1.10.3 Heartbeat

monitor-server 可向用户指定的外部服务发送简单 Heartbeat，用于发现“监控服务器自己挂了”的情况。

### 1.10.4 健康检查

Agent：

```bash
monitor-agent health
monitor-agent doctor
```

Server：

```text
/healthz
/readyz
```

### 1.10.5 与 Beszel / Komari 的差异

```text
Beszel / Komari
侧重 Web 监控平台

本项目
侧重官方 iOS / Android App
```

核心差异：

```text
App-first
AK 扫码配对
多监控中心
VPS 流量套餐
VPS 到期提醒
App Push
App Widget
用户数据自持
开发者不保存
```

---

## 1.11 不直接复制现有项目

可以参考成熟产品解决的问题和交互原则，但应保持独立实现。

不直接复制：

```text
UI 布局
图标体系
页面结构
组件代码
后端代码
数据库结构
接口定义
品牌视觉
文案
```

可以借鉴的通用思想：

```text
轻量 Agent
实时指标
历史数据
自托管
自动升级
分组
告警
插件/扩展思路
```

本项目的独立辨识度应来自：

```text
App-first
AK 扫码配对
多监控中心
移动 Push
Widget
流量套餐预测
VPS 资产管理
移动巡检体验
设备级授权
```

---

## 1.12 商业模式

无遥测、自托管、无账号的前提下，收入只能来自 App 本身。Relay 有持续运营成本，App 上架和多端维护也有成本，因此需要在立项时确定。

### 1.12.1 建议方案

```text
monitor-agent / monitor-server / Web / Push Relay
  开源、免费

App 免费版
  单个监控中心
  完整监控、历史、流量套餐、到期提醒
  官方原生 Push（E2E 加密，合理速率限制）

App Pro（买断制，可选年度更新包）
  多监控中心 + 聚合视图
  桌面 Widget
  高级排序 / 健康摘要
  自定义告警声音、主题
```

原则：

```text
安全能力不收费（E2E Push、生物识别锁、隐私模式、设备吊销均在免费版）
核心监控不收费
收费点集中在“多 VPS 重度用户”的效率功能
```

### 1.12.2 与隐私原则的一致性

```text
内购使用 StoreKit 2 / Google Play Billing 本地校验
不建立开发者账号体系
不需要将购买记录与服务器信息关联
Relay 的速率限制按匿名实例密钥计数（内存中），不按用户
```

### 1.12.3 备选

```text
Relay 订阅：免费版 Push 限额，订阅提高额度
捐赠 / 赞助：适合纯开源路线，但收入不稳定
```

不建议：

```text
SaaS 托管版（违背“开发者不保存数据”的核心定位）
广告
```

---

# 2. 总体架构

## 2.1 系统架构

```text
┌─────────────────────────────────────────────┐
│                被监控服务器                  │
│                                             │
│   monitor-agent                             │
│   ├─ CPU                                    │
│   ├─ Memory                                 │
│   ├─ Disk                                   │
│   ├─ Network                                │
│   ├─ Load                                   │
│   ├─ Uptime                                 │
│   └─ System Info                            │
└───────────────────┬─────────────────────────┘
                    │
                    │ HTTPS / JSON
                    │ 主动上报
                    ▼
┌─────────────────────────────────────────────┐
│              中央监控服务端                  │
│                                             │
│  monitor-server（Go 单二进制）               │
│  ├─ Agent API                               │
│  ├─ Web/App API                             │
│  ├─ Auth Service                            │
│  ├─ WebSocket                               │
│  ├─ Alert Engine                            │
│  ├─ Traffic Engine                          │
│  ├─ Aggregation Service（降采样）            │
│  ├─ Notification Service（含 Push 加密）     │
│  ├─ Release Mirror（只同步官方签名版本）      │
│  └─ 内嵌 Web 静态资源（go:embed）            │
│                                             │
│  SQLite（WAL，默认） / PostgreSQL（可选）     │
└───────────────┬─────────────────┬───────────┘
                │                 │
                │ REST/WebSocket  │ REST/WebSocket
                ▼                 ▼
┌──────────────────────┐   ┌──────────────────────┐
│      Web 管理端       │   │       Mobile App     │
│      Vue 3            │   │       Flutter        │
│                      │   │                      │
│ 登录/服务器管理       │   │ iOS                  │
│ 用户/Token/告警       │   │ Android              │
│ 历史/图表/资产        │   │ 状态/趋势/Push       │
└──────────────────────┘   └──────────▲───────────┘
                                      │ APNs / FCM
                                      │ （仅密文）
                           ┌──────────┴───────────┐
                           │  Push Relay（开源）   │
                           │  无状态、不落库       │
                           └──────────▲───────────┘
                                      │ 加密 Payload
                              monitor-server 发出
```

开发者侧只运营两样东西：

```text
官方发布地址（离线签名的 Agent 版本）
Push Relay（只转发密文）
```

两者都不接触明文监控数据。

## 2.2 核心设计原则

1. **Agent 主动上报**
   - 中央服务器不主动连接被监控服务器。
   - 支持 NAT、IPv6、动态公网 IP 等环境。

2. **Agent 轻量化**
   - 优先读取 Linux 内核已有计数器。
   - 不进行抓包。
   - 不在本地存储大量历史数据。

3. **Web 与 App 共用 API**
   - 避免重复实现业务逻辑。
   - 统一认证与权限。

4. **实时数据与历史数据分离**
   - WebSocket：用于实时状态。
   - REST API：用于历史趋势与管理操作。

5. **监控数据分层存储**
   - 高频数据保留短周期。
   - 长期数据自动降采样。

6. **安全优先**
   - HTTPS。
   - Agent 独立 Token。
   - Web 用户 Token、App Device Token、Agent Token 三类凭证相互隔离。
   - App AK 仅用于首次配对，不作为长期 API 访问凭证。
   - 支持 AK、Device Token、Agent Token 独立吊销。

---

# 3. 技术选型

## 3.1 Agent

推荐：

```text
Go
```

推荐依赖：

```text
github.com/shirou/gopsutil/v4
```

主要原因：

- 单文件部署方便。
- 支持 amd64 / arm64。
- 常驻内存低。
- 并发模型简单。
- Linux 适配较好。
- 无需安装 Python Runtime。

目标平台：

```text
linux/amd64
linux/arm64
```

后续可选：

```text
linux/386
linux/riscv64
freebsd/amd64
```

---

## 3.2 后端

推荐：

```text
Go
```

框架可选：

```text
Gin
Fiber
Echo
```

推荐第一版使用：

```text
Gin
```

主要组件：

```text
Gin
sqlx（统一 SQL 层，同时适配 SQLite / PostgreSQL）
modernc.org/sqlite（纯 Go，无 CGO，便于交叉编译）
pgx（可选 PostgreSQL 驱动）
JWT
WebSocket
go:embed（内嵌 Web 静态资源）
certmagic（可选，内置自动 HTTPS）
```

不依赖：

```text
Redis（实时状态保存在进程内存）
TimescaleDB
消息队列
```

服务端资源目标（50 台节点、10 秒上报）：

| 项目 | 目标 |
|---|---:|
| 常驻内存 | < 80 MB |
| 平均 CPU（1 vCPU） | < 3% |
| 数据库体积 | < 1 GB（按第 21 章保留策略） |
| 部署产物 | 1 个二进制 + 1 个数据目录 |

目标是能与几个小服务共存在一台 512MB～1GB 的 VPS 上。

---

## 3.3 Web 管理端

推荐：

```text
Vue 3
TypeScript
Vite
Pinia
Vue Router
Element Plus
ECharts
Axios
```

---

## 3.4 App

推荐：

```text
Flutter
```

支持：

```text
iOS
Android
```

推荐组件：

```text
dio
riverpod / bloc
go_router
fl_chart
shared_preferences
flutter_secure_storage
firebase_messaging
unifiedpush（Android 可选）
cryptography / libsodium 绑定（Push 解密）
```

iOS Push：

```text
APNs（mutable-content + Notification Service Extension 本地解密）
```

Android Push：

```text
Firebase Cloud Messaging（data message，App 本地解密后显示）
UnifiedPush / ntfy（无 Google 服务的设备，或希望完全自持的用户）
```

很多 VPS 用户的 Android 设备没有 Google 服务，UnifiedPush 支持应在第一阶段提供。

---

## 3.5 数据库

默认：

```text
SQLite（WAL 模式）
```

可选：

```text
PostgreSQL（不依赖 TimescaleDB 扩展）
```

选择 SQLite 作为默认的原因：

```text
目标用户的规模通常是 5～100 台节点
面板经常和其他服务共用一台小 VPS
零运维：无独立数据库进程、无额外容器
备份就是复制一个文件（VACUUM INTO / 在线备份 API）
```

容量估算（50 台节点、10 秒上报）：

```text
原始数据：50 × 8640 = 43.2 万行/天，保留 24 小时
1 分钟聚合：50 × 1440 × 7 天 ≈ 50 万行
5 分钟聚合：50 × 288 × 30 天 ≈ 43 万行
1 小时聚合：50 × 24 × 365 ≈ 44 万行
```

总量在百万行级别，SQLite 完全胜任。

写入策略：

```text
上报先写入内存缓冲，每 2～5 秒在一个事务中批量写入
实时状态（最新一次快照）保存在内存，App / Web 读取不打数据库
降采样与过期清理由进程内定时任务完成
单写连接 + 多读连接，避免 SQLITE_BUSY
PRAGMA journal_mode=WAL; synchronous=NORMAL; busy_timeout=5000
```

何时切换 PostgreSQL：

```text
节点数 > 200
需要将数据库与面板分离部署
需要外部 BI / SQL 工具长期接入
```

数据访问层通过统一接口抽象，两种后端使用同一套迁移脚本（方言差异单独处理），提供 `monitor-server migrate-db --to postgres` 迁移命令。

---

# 4. Agent 详细设计

## 4.1 Agent 职责

Agent 负责：

- 采集 CPU。
- 采集 Load。
- 采集内存。
- 采集 Swap。
- 采集磁盘容量。
- 采集磁盘 IO。
- 采集网卡累计流量。
- 计算实时上传/下载速率。
- 获取系统运行时间。
- 获取系统版本。
- 获取内核版本。
- 获取主机名。
- 获取公网/内网 IP。
- 获取 Agent 版本。
- 定时上报中央服务端。
- Agent 自身健康检查。
- 支持远程下发基础配置。

Agent 不负责：

- 大量历史数据存储。
- 抓包。
- 深度流量分析。
- 大量日志写盘。
- 告警策略判断。
- UI。

---

## 4.2 Agent 资源目标

建议第一版目标：

| 项目 | 目标 |
|---|---:|
| 常驻内存 | 10～30 MB |
| 平均 CPU | < 0.5% |
| 空闲 CPU | 接近 0 |
| 默认采集周期 | 10 秒 |
| 默认上报周期 | 10 秒 |
| 最低支持内存 | 256 MB |
| 推荐最低内存 | 512 MB |
| 本地磁盘写入 | 极少 |
| 进程数 | 1 |

---

## 4.3 Agent 配置

示例：

```yaml
server:
  id: hk-dmit-01
  name: DMIT-HK
  group: hongkong

api:
  endpoint: https://monitor.example.com
  token: AGENT_TOKEN

monitor:
  interval: 10s
  report_interval: 10s

  cpu: true
  memory: true
  disk: true
  disk_io: true
  network: true
  load: true
  uptime: true

network:
  # 留空时自动选择默认路由所在网卡
  interfaces:
    - eth0
  # 默认排除，避免与物理网卡重复计数
  exclude:
    - lo
    - docker*
    - veth*
    - br-*
    - virbr*
    - tun*
    - wg*

traffic:
  reset_day: 1

upgrade:
  remote: true        # 仅本地可改，false 时面板无法触发升级
  channel: stable

log:
  level: info
  file: /var/log/monitor-agent.log
  max_size_mb: 10
  max_backups: 3
```

---

## 4.4 CPU 指标

采集：

```text
cpu_usage_percent
cpu_cores
load_1
load_5
load_15
```

Linux 可从：

```text
/proc/stat
/proc/loadavg
```

获取。

---

## 4.5 内存指标

采集：

```text
memory_total
memory_used
memory_available
memory_usage_percent
swap_total
swap_used
swap_usage_percent
```

Linux 可从：

```text
/proc/meminfo
```

获取。

---

## 4.6 磁盘指标

每个挂载点：

```text
mount_point
filesystem
device
total_bytes
used_bytes
available_bytes
usage_percent
```

推荐默认监控：

```text
/
```

可选：

```text
/home
/data
/var/lib/docker
```

应忽略：

```text
tmpfs
devtmpfs
overlay
proc
sysfs
cgroup
```

---

## 4.7 磁盘 IO

采集：

```text
read_bytes
write_bytes
read_ops
write_ops
io_time
```

数据源：

```text
/proc/diskstats
```

速率计算：

```text
read_speed =
(current_read_bytes - previous_read_bytes) / interval

write_speed =
(current_write_bytes - previous_write_bytes) / interval
```

---

## 4.8 Agent 兼容性设计

Agent 重点服务常见 VPS 环境，不追求覆盖所有特殊 CPU 或企业专用平台。

正式支持：

```text
linux/amd64
linux/arm64
```

可选支持：

```text
linux/armv7
```

重点发行版：

```text
Ubuntu
Debian
Alpine Linux
Rocky Linux
AlmaLinux
CentOS Stream
Oracle Linux
Arch Linux
Fedora
```

设计原则：

```text
单文件
尽量静态编译
CGO_ENABLED=0
兼容 glibc / musl
不依赖 Python / Java / Node.js
不依赖 Docker
不依赖特定包管理器
```

基础采集优先读取：

```text
/proc
/sys
statfs/statvfs
```

支持服务管理：

```text
systemd
OpenRC
前台运行
```

Agent 启动时自动识别：

```text
OS
Kernel
Architecture
Hostname
Network Interface
```

如某项指标不可用：

```text
该指标标记 unavailable
```

不能导致整个 Agent 退出。

提供：

```bash
monitor-agent doctor
```

用于检查：

```text
系统
架构
网络
TLS
采集能力
后端连通性
升级能力
```

远程升级必须按：

```text
OS + Arch + Version
```

匹配正确安装包，禁止跨架构升级。

---

# 5. 网络流量设计

## 5.1 基础指标

每个网卡采集：

```text
rx_bytes
tx_bytes
rx_packets
tx_packets
rx_errors
tx_errors
rx_dropped
tx_dropped
```

Linux：

```text
/proc/net/dev
```

---

## 5.2 实时网速

Agent 记录两次累计值。

示例：

```text
T1:
RX = 1000000000

T2:
RX = 1020000000

间隔 = 10 秒
```

下载速度：

```text
(1020000000 - 1000000000) / 10

= 2000000 Byte/s
```

约：

```text
1.91 MB/s
15.26 Mbps
```

上传同理。

---

## 5.3 月流量

中央服务端根据 Agent 的累计计数器进行增量计算。

推荐服务端维护：

```text
previous_rx
previous_tx
current_rx
current_tx
delta_rx
delta_tx
```

公式：

```text
delta_rx = current_rx - previous_rx
delta_tx = current_tx - previous_tx
```

如果发现：

```text
current_rx < previous_rx
```

说明可能：

- 服务器重启。
- 网卡重建。
- 计数器归零。

此时不使用负值，而重新建立基线。

仅靠“当前值小于上次值”判断重启并不可靠（重启后流量很大时计数可能已超过旧值），因此改为以 `boot_id` 为准，见 5.5。

---

## 5.4 流量统计周期

每台服务器可单独配置：

```text
每月 1 日重置
每月 15 日重置
每月 20 日重置
自定义计费周期
```

服务器表：

```text
traffic_limit_bytes
traffic_reset_day
traffic_count_mode
```

计费方式支持：

```text
RX + TX
仅 RX
仅 TX
MAX(RX, TX)
```

第一版建议默认：

```text
RX + TX
```

---

## 5.5 重启识别与增量补偿

Agent 每次上报携带：

```text
boot_id      /proc/sys/kernel/random/boot_id
iface_index  网卡 ifindex（网卡重建时变化）
```

服务端规则：

```text
boot_id 或 ifindex 变化   → 新基线，本次 delta = 当前计数值（开机以来的流量全部计入）
boot_id 不变且计数递增    → delta = current - previous
boot_id 不变且计数回退    → 视为计数器溢出 / 驱动重置，按新基线处理并记录事件
```

注意“新基线时 delta = 当前计数值”，而不是 0：重启后到首次上报之间产生的流量也要计入。

无法避免的误差：从最后一次上报到关机之间的流量。缓解措施：

```text
Agent 收到 SIGTERM 时立即补报一次（超时 2 秒，尽力而为）
默认 10 秒上报周期，异常断电时误差上限为 10 秒流量
```

网络中断但未重启时不会丢流量：计数器是累计值，恢复上报后 delta 自然补齐。

---

## 5.6 网卡选择

VPS 上常见 Docker、WireGuard、隧道等虚拟网卡，全部累加会重复计数。

默认：

```text
只统计默认路由所在网卡（IPv4 与 IPv6 默认路由不同时，两者都计入并去重）
排除 lo / docker* / veth* / br-* / virbr* / tun* / wg*
```

Web 可按服务器调整统计网卡。

---

## 5.7 手动校准

Agent 的 `/proc/net/dev` 计数和服务商计费口径经常存在偏差（统计点不同、是否计入协议开销、计费时区不同）。如果“剩余流量”不可信，核心卖点反而会受损，因此必须支持校准。

Web / App 提供：

```text
[校准已用流量]
服务商面板显示已用：642.3 GB
```

服务端处理：

```text
adjustment = 用户填写值 - 当前统计值
展示值 = 统计值 + adjustment
```

规则：

```text
校准只影响当前计费周期，周期重置时清零
每次校准写入 traffic_adjustments（见 18.12），可查看历史
App 显示“已于 10-02 校准”
如果多次校准显示偏差稳定（例如统计值长期偏低 3%），可提示用户设置系数
```

可选：每台服务器设置统计系数（例如 1.03），用于长期修正固定比例偏差。

---

## 5.8 计量单位口径

服务商对“1 TB”的定义不一致。每台服务器可设置：

```text
十进制：1 GB = 10^9 Byte（多数服务商）
二进制：1 GiB = 2^30 Byte
```

套餐额度与剩余量都按该口径显示，默认十进制。

---

# 6. Agent 上报协议

## 6.1 Endpoint

```http
POST /api/v1/agent/report
```

Header：

```http
Authorization: Bearer AGENT_TOKEN
Content-Type: application/json
```

---

## 6.2 上报示例

```json
{
  "server_id": "hk-dmit-01",
  "timestamp": 1790928000,
  "agent_version": "1.0.0",

  "system": {
    "hostname": "dmit-hk",
    "os": "Debian",
    "os_version": "13",
    "kernel": "6.12.0",
    "uptime": 829233,
    "boot_id": "3f1c2a9e-8b7d-4e21-9c55-0a6f1d2b7e44"
  },

  "cpu": {
    "usage": 18.6,
    "cores": 2,
    "load1": 0.31,
    "load5": 0.28,
    "load15": 0.21
  },

  "memory": {
    "total": 2147483648,
    "used": 1073741824,
    "available": 1073741824,
    "usage": 50.0
  },

  "swap": {
    "total": 1073741824,
    "used": 134217728
  },

  "disk": [
    {
      "mount": "/",
      "total": 42949672960,
      "used": 18253611008,
      "usage": 42.5
    }
  ],

  "network": [
    {
      "interface": "eth0",
      "ifindex": 2,
      "rx_bytes": 833721124344,
      "tx_bytes": 139922812321,
      "rx_speed": 1258291,
      "tx_speed": 328122
    }
  ]
}
```

---

# 7. 中央服务端设计

## 7.1 服务模块

```text
monitor-server
├── auth
├── agent
├── server
├── metrics
├── traffic
├── alerts
├── users
├── notification
├── websocket
├── aggregation
└── admin
```

---

## 7.2 服务端主要职责

### Auth Service

负责：

- 登录。
- Token 签发。
- Refresh Token。
- Token 吊销。
- 用户权限。

### Agent Service

负责：

- Agent 鉴权。
- Agent 上报。
- Agent 在线状态。
- Agent 版本管理。

### Metrics Service

负责：

- 实时指标。
- 历史指标。
- 数据聚合。
- 查询。

### Traffic Service

负责：

- 网络增量计算。
- 今日流量。
- 月流量。
- 周期流量。
- 流量预测。

### Alert Service

负责：

- 告警规则。
- 告警判断。
- 告警恢复。
- 去重。
- 告警抑制。

### Notification Service

负责：

- App Push。
- Telegram。
- Webhook。
- 邮件扩展。

---

# 8. Web 管理端设计

## 8.1 登录页面

Web 必须提供独立登录页面。

URL：

```text
/login
```

页面结构：

```text
┌──────────────────────────────────────┐
│                                      │
│           Server Monitor             │
│                                      │
│       用户名 / 邮箱                  │
│       ┌──────────────────────┐       │
│       │                      │       │
│       └──────────────────────┘       │
│                                      │
│       密码                           │
│       ┌──────────────────────┐       │
│       │                      │       │
│       └──────────────────────┘       │
│                                      │
│       □ 记住登录                     │
│                                      │
│       [        登 录        ]        │
│                                      │
└──────────────────────────────────────┘
```

登录支持：

```text
用户名 + 密码
邮箱 + 密码
```

后续可选：

```text
TOTP 双因素认证
Passkey
OIDC
LDAP
```

---

## 8.2 登录流程

```text
用户输入账号密码
       │
       ▼
POST /api/v1/auth/login
       │
       ▼
后端校验密码
       │
       ▼
返回 Access Token
     + Refresh Token
       │
       ▼
进入 Dashboard
```

Access Token：

```text
15～30 分钟
```

Refresh Token：

```text
7～30 天
```

---

## 8.3 Web 菜单

建议：

```text
Dashboard
服务器
监控
流量
告警
分组
通知
Agent
App 接入
用户管理
系统设置
```

---

## 8.4 App 接入管理

Web 管理端负责创建和管理 App 接入凭证，不要求 App 使用 Web 用户名和密码登录。

菜单：

```text
App 接入
├── AK 管理
├── 已连接设备
└── 接入记录
```

### 8.4.1 创建 AK

管理员点击：

```text
[ + 创建 App AK ]
```

创建参数：

```text
名称：Tom iPhone
权限范围：全部服务器 / 指定分组 / 指定服务器
允许低风险操作：是 / 否（静音告警、维护模式）
有效期：一次性 / 30 天 / 90 天 / 自定义
最大配对设备数：默认 1
```

推荐默认：

```text
允许低风险操作：是
最大配对设备数：1
AK：一次性配对后失效
```

v1 不提供“管理”权限。App 设备凭证永远只能：

```text
查看授权范围内的数据
静音 / 取消静音告警
开启 / 结束维护模式
管理本设备的 Push 订阅
撤销本设备
```

不能：

```text
增删改服务器、告警规则、AK、用户
触发 Agent 升级
查看或生成 Agent Token
```

理由：手机比服务器更容易丢失或被借用，App 凭证泄露的最坏结果应当只是“被看到”和“告警被静音”。所有配置变更留在 Web。

创建成功后完整 AK 只显示一次：

```text
MNT-X7K9-3PH2-W8QF
```

页面同时提供：

```text
[复制 AK]
[显示二维码]
[吊销 AK]
```

数据库仅保存 AK 的安全摘要，不保存可恢复的明文 AK。

### 8.4.2 AK 列表

列表字段：

| 字段 | 说明 |
|---|---|
| 名称 | Tom iPhone |
| AK | MNT-X7K9-****-W8QF |
| 低风险操作 | 允许 |
| 范围 | 全部服务器 |
| 最大设备数 | 1 |
| 已配对设备数 | 1 |
| 有效期 | 一次性/到期时间 |
| 状态 | 可用/已使用/已吊销/已过期 |
| 创建时间 | 时间 |
| 最后使用 | 时间 |
| 操作 | 二维码/吊销 |

### 8.4.3 已连接设备

Web 应能够查看所有通过 AK 配对的移动设备：

```text
Tom's iPhone
平台：iOS
App：1.0.0
权限：只读
最后在线：1 分钟前
配对时间：2026-10-02 16:40
状态：正常

[吊销设备]
```

Android 同样管理。

设备吊销后：

```text
Device Token
Refresh Token
Push Token
```

均立即失效，App 下一次请求返回：

```http
401 Unauthorized
```

App 随即回到“添加监控平台”页面，并提示该设备授权已被撤销。

### 8.4.4 二维码配对

Web 生成二维码，二维码中只携带完成配对所需的信息：

```text
server
ak
```

逻辑负载示例：

```text
monitor://pair?server=https%3A%2F%2Fmonitor.example.com&ak=MNT-X7K9-3PH2-W8QF
```

二维码不得包含：

```text
Web 用户密码
Web Access Token
Agent Token
长期 Device Token
```

App 扫码后必须向服务端执行正式配对请求，由服务端验证 AK 后再签发设备凭证。

---

# 9. Dashboard

首页展示：

```text
服务器总数
在线
离线
告警
CPU 高负载
磁盘异常
流量异常
```

示例：

```text
┌──────────┐ ┌──────────┐ ┌──────────┐
│服务器 26 │ │在线 24   │ │离线 2    │
└──────────┘ └──────────┘ └──────────┘

┌──────────┐ ┌──────────┐
│告警 3    │ │流量预警 1│
└──────────┘ └──────────┘
```

下方：

```text
最近告警
服务器状态
地区分布
CPU Top 10
内存 Top 10
磁盘 Top 10
流量 Top 10
```

---

# 10. Web 服务器列表

URL：

```text
/servers
```

字段：

| 字段 | 说明 |
|---|---|
| 名称 | DMIT-HK |
| 状态 | 在线/离线 |
| 地区 | 香港 |
| IP | 公网 IP |
| CPU | 当前 CPU |
| 内存 | 当前内存 |
| 磁盘 | 根分区 |
| 下载 | 当前下载 |
| 上传 | 当前上传 |
| 月流量 | 已用/总额 |
| Uptime | 运行时间 |
| 最后上报 | 时间 |
| 操作 | 查看/编辑/删除 |

支持：

```text
搜索
排序
筛选
分组
标签
批量操作
```

---

# 11. Web 服务器详情页

URL：

```text
/servers/{id}
```

页面模块：

```text
概览
实时监控
历史趋势
网络流量
磁盘
系统信息
告警
配置
```

---

## 11.1 概览

展示：

```text
服务器名称
在线状态
IP
IPv6
供应商
地区
CPU 核数
内存
磁盘
系统版本
内核
Agent 版本
运行时间
```

---

## 11.2 图表

支持时间范围：

```text
1H
6H
24H
7D
30D
90D
```

图表：

```text
CPU
Load
内存
Swap
磁盘
上传
下载
磁盘 IO
```

---

# 12. App 设计

> App 是本项目的核心用户端。Web 主要承担配置、授权、版本与运维管理；日常服务器状态查看、告警接收、巡检和流量关注优先在 App 完成。


## 12.1 App 支持平台

必须支持：

```text
iOS
Android
```

推荐 Flutter 一套代码维护。

---

## 12.2 App 配对页

App 不使用 Web 用户名和密码登录。

首次启动流程：

```text
Splash
  │
  ├─ 已存在有效 Device Token → 首页
  │
  └─ 未配对 / Token 已失效
              │
              ▼
        添加监控平台
```

页面：

```text
Server Monitor

[ 扫描二维码 ]

        或

监控服务器地址
[ https://monitor.example.com ]

AK
[ MNT-____-____-____ ]

[ 连接监控平台 ]
```

用户只需要从 Web 管理端获取：

```text
服务器地址
AK
```

即可完成 App 接入。

---

## 12.3 App AK 配对流程

推荐采用：

```text
Web 管理端
   │
   │ 创建 AK
   ▼
AK + 二维码
   │
   ├──────────────► App 扫码
   │
   └──────────────► App 手工填写
                         │
                         ▼
                 POST /api/v1/app/pair
                         │
                         ▼
                    验证 AK
                         │
                         ▼
                  创建设备记录
                         │
                         ▼
                签发 Device Token
                    + Refresh Token
                         │
                         ▼
                App 安全保存凭证
                         │
                         ▼
                    进入首页
```

AK 的定位是：

```text
首次配对凭证
```

而不是：

```text
长期 API Bearer Token
```

默认建议：

```text
AK 一次性使用
最大配对设备数 = 1
```

如确有多设备需求，可在 Web 中明确设置：

```text
max_devices > 1
```

配对时同时建立 Push 加密密钥：

```text
App 本地生成 X25519 密钥对
  私钥 → Keychain / Keystore（iOS 放入与 Notification Service Extension 共享的 Keychain Access Group）
  公钥 → 随配对请求提交给 monitor-server，存入 push_devices.push_public_key
```

此后 monitor-server 发给该设备的每条 Push 都用该公钥加密（见 30.3）。私钥永不离开设备，更换设备或重新配对时重新生成。

---

## 12.4 二维码配对

App 点击：

```text
扫描二维码
```

扫码成功后解析：

```text
server=https://monitor.example.com
ak=MNT-X7K9-3PH2-W8QF
```

App 应先显示目标监控中心：

```text
即将连接：

https://monitor.example.com

[取消] [连接]
```

确认后才发送配对请求。

二维码逻辑格式：

```text
monitor://pair?server=https%3A%2F%2Fmonitor.example.com&ak=MNT-X7K9-3PH2-W8QF
```

为了防止误扫恶意二维码：

- 只接受 `https://` 服务地址；开发环境可单独允许 HTTP。
- 配对前展示目标域名。
- 不自动信任自签名证书。
- 不允许二维码直接携带长期 Device Token。
- AK 必须由服务端在线验证。

---

## 12.5 Device Token

配对成功后服务端返回：

```json
{
  "device_id": "dev_01JXYZ",
  "access_token": "dt_xxxxxxxxxxxxx",
  "refresh_token": "rt_xxxxxxxxxxxxx",
  "expires_in": 1800,
  "scope": {
    "role": "app_viewer",
    "servers": "all",
    "allow_low_risk_ops": true
  }
}
```

App 后续访问：

```http
Authorization: Bearer dt_xxxxxxxxxxxxx
```

不再重复提交 AK。

Access Token 建议有效期：

```text
15～30 分钟
```

Refresh Token 建议：

```text
30～90 天
```

Refresh Token 采用轮换机制：

```text
Refresh Token Rotation
```

---

## 12.6 App 凭证安全存储

Device Token 与 Refresh Token 必须保存在系统安全存储中：

```text
iOS Keychain
Android Keystore
```

Flutter：

```text
flutter_secure_storage
```

禁止保存在：

```text
普通 SharedPreferences
明文 SQLite
日志
剪贴板长期缓存
```

---

## 12.7 设备授权失效

出现以下情况时：

```text
管理员吊销设备
AK 所属授权被整体吊销
Refresh Token 失效
设备长时间未使用且策略要求失效
```

服务端返回：

```http
401 Unauthorized
```

App 应：

```text
清除本地 Device Token
清除 Refresh Token
保留非敏感 UI 偏好
跳转至“添加监控平台”
```

并显示：

```text
当前设备授权已失效，请重新通过 Web 管理端生成 AK 配对。
```

---

## 12.8 多监控中心

多监控中心是核心能力（见 1.5.2）。第一阶段支持添加多个中心并切换，聚合视图在第二阶段提供。

```text
我的监控

● VPS
  monitor.example.com
  26 台

● 家庭服务器
  monitor.home.example
  8 台
```

每个监控中心分别保存：

```text
server_url
device_id
access_token
refresh_token
push_private_key
```

凭证、Push 密钥之间完全隔离。Push 通知中携带加密的中心标识，App 解密后路由到对应中心。

---

# 13. App 首页

App 首页设计目标：

```text
快速看到异常
快速看到离线
快速看到服务器状态
```

顶部：

```text
我的服务器

在线 24
离线 1
告警 2
```

服务器卡片：

```text
🇭🇰 DMIT Hong Kong
● 在线

CPU      18%
MEM      42%
DISK     31%

↓ 12.6 Mbps
↑  1.8 Mbps

638 / 1000 GB
████████████░░░
```

支持分组：

```text
全部
香港
日本
美国
新加坡
大陆
```

支持标签：

```text
生产
测试
代理
NAS
AI
备用
```

---

# 14. App 服务器详情页

顶部：

```text
DMIT Hong Kong

● Online
```

状态卡：

```text
CPU
18%

Memory
42%

Disk
31%
```

网络：

```text
↓ 18.26 Mbps
↑ 2.81 Mbps
```

趋势：

```text
CPU
Memory
Network
Disk

1H
6H
24H
7D
30D
```

流量：

```text
638 GB / 1000 GB

已使用：63.8%
剩余：362 GB
距离重置：12 天
预计周期总量：891 GB
```

---

# 15. App Push 告警

必须支持 Push，且默认启用端到端加密（见第 30 章）。

以下示例均为 App 本地解密后显示的内容；Relay、APNs、FCM 只能看到密文。

## 15.1 离线告警

示例：

```text
🔴 DMIT-HK 已离线

超过 120 秒未收到 Agent 上报
时间：2026-10-02 16:31
```

点击通知：

```text
App
 ↓
服务器详情页
```

---

## 15.2 CPU 告警

```text
🟠 CPU 高负载

服务器：DMIT-HK
CPU：96.2%
持续时间：5 分钟
```

---

## 15.3 流量告警

```text
🟡 月流量预警

服务器：Zoro-JP
已用：923 GB / 1000 GB
使用率：92.3%
距离重置：8 天
```

---

# 16. 告警规则

推荐第一版支持：

```text
CPU
内存
磁盘
Swap
服务器离线
月流量
```

示例：

```text
CPU > 90%
持续 5 分钟
```

```text
内存 > 90%
持续 5 分钟
```

```text
磁盘 > 85%
```

```text
服务器 120 秒未上报
```

```text
流量 > 80%
流量 > 90%
流量 > 95%
```

---

## 16.1 告警状态

```text
OK
PENDING
FIRING
RECOVERED
```

流程：

```text
正常
 ↓
达到阈值
 ↓
Pending
 ↓
持续满足条件
 ↓
Firing
 ↓
发送通知
 ↓
指标恢复
 ↓
Recovered
 ↓
发送恢复通知
```

---

# 17. 用户及权限设计

第一版角色：

```text
admin
viewer
```

Admin：

```text
查看服务器
新增服务器
修改服务器
删除服务器
管理 Agent
管理告警
管理用户
系统设置
```

Viewer：

```text
查看服务器
查看监控
查看历史
查看告警
```

第一阶段只实现单管理员（admin），viewer 角色放在第二阶段。App 访问不依赖 Web 用户体系，而是通过 AK 配对的设备凭证（见 8.4）。

不做：

```text
RBAC
```

---

# 18. 数据库设计

> 本章节所述数据库均指**用户自行部署的 monitor-server 数据库**。这些数据不上传到开发者后台，开发者不集中存储任何用户的监控数据、服务器资料、AK 或 App 设备授权信息。


## 18.1 users

```sql
id
username
email
password_hash
role
status
created_at
updated_at
last_login_at
```

---

## 18.2 servers

```sql
id
server_key
name
hostname
group_id

provider
region
country

ipv4
ipv6

cpu_cores
memory_total
disk_total

os
os_version
kernel

agent_version

traffic_limit_bytes
traffic_reset_day
traffic_count_mode

status
last_seen_at

created_at
updated_at
```

---

## 18.3 agent_tokens

```sql
id
server_id
token_hash
status
created_at
last_used_at
revoked_at
```

---

## 18.4 metrics

高频时序表：

```sql
time
server_id

cpu_usage
load_1
load_5
load_15

memory_used
memory_usage

swap_used
swap_usage

disk_used
disk_usage

rx_speed
tx_speed

rx_bytes
tx_bytes
```

SQLite 下按粒度分表：

```text
metrics_raw   10 秒，保留 24 小时
metrics_1m    1 分钟，保留 7 天
metrics_5m    5 分钟，保留 30 天
metrics_1h    1 小时，保留 1 年
```

聚合表额外保存 `*_max`、`*_min` 字段。主键 `(server_id, time)`，使用 WITHOUT ROWID 表减少体积。PostgreSQL 后端使用相同表结构，可选按月分区。

---

## 18.5 traffic_daily

```sql
date
server_id

rx_bytes
tx_bytes
total_bytes
```

---

## 18.6 traffic_cycles

```sql
id
server_id

cycle_start
cycle_end

rx_bytes
tx_bytes
total_bytes

traffic_limit_bytes
```

---

## 18.7 alert_rules

```sql
id
server_id
metric
operator
threshold
duration
severity
enabled
created_at
updated_at
```

---

## 18.8 alert_events

```sql
id
server_id
rule_id

status
severity
message

triggered_at
recovered_at
```

---

## 18.9 app_access_keys

用于保存 Web 管理端创建的 App 配对 AK。该表仅存在于用户自建的 monitor-server 数据库中，开发者不持有此表的数据。

```sql
id
name

key_prefix
key_hash

scope_type
scope_data
allow_low_risk_ops

max_devices
paired_devices

one_time
expires_at

status

created_by
created_at
last_used_at
revoked_at
```

其中：

```text
key_prefix
```

只用于 Web 页面脱敏展示，例如：

```text
MNT-X7K9-****-W8QF
```

`key_hash` 保存 AK 的安全摘要，禁止保存可恢复的完整 AK。

`scope_type` 可选：

```text
ALL
GROUP
SERVER
```

`status`：

```text
ACTIVE
USED
EXPIRED
REVOKED
```

---

## 18.10 app_devices

每次 App 成功配对后在用户自建 monitor-server 中创建一条设备记录。开发者后台不维护设备列表。

```sql
id
access_key_id

device_name
platform
device_identifier_hash

app_version

scope_type
scope_data
allow_low_risk_ops

refresh_token_hash
refresh_token_expires_at

status

paired_at
last_seen_at
revoked_at
revoked_by
```

`platform`：

```text
ios
android
```

`status`：

```text
ACTIVE
REVOKED
EXPIRED
```

Access Token 可以采用短期 JWT，因此无需保存明文；Refresh Token 仅保存 Hash。

---

## 18.11 push_devices

Push Token 与已配对设备关联：

```sql
id
app_device_id

provider
push_token
push_public_key

app_version
created_at
updated_at
last_seen_at
```

`provider`：

```text
apns
fcm
unifiedpush
```

`push_public_key`：App 配对时生成的 X25519 公钥，用于加密 Push 内容。

设备被吊销时，其 Push Token 与公钥同时删除。

---

## 18.12 traffic_adjustments

流量手动校准记录（见 5.7）：

```sql
id
server_id
cycle_start

measured_bytes      -- 校准时系统统计值
reported_bytes      -- 用户填写的服务商数值
adjustment_bytes    -- reported - measured

note
created_by
created_at
```

servers 表同时增加：

```text
traffic_unit        -- decimal / binary
traffic_factor      -- 统计系数，默认 1.0
traffic_interfaces  -- 统计网卡，空为自动
expire_at           -- 到期日期
renew_price
renew_currency
renew_cycle
```

---

# 19. API 设计

API Prefix：

```text
/api/v1
```

---

## 19.1 Web 认证

Web 管理端继续使用用户名/密码认证：

```http
POST /api/v1/auth/login
POST /api/v1/auth/logout
POST /api/v1/auth/refresh
GET  /api/v1/auth/me
```

该认证只用于 Web 管理端，不直接提供给 App。

---

## 19.2 App AK 管理

以下接口要求 Web 管理员权限：

```http
GET    /api/v1/app-access-keys
POST   /api/v1/app-access-keys
GET    /api/v1/app-access-keys/{id}
DELETE /api/v1/app-access-keys/{id}
POST   /api/v1/app-access-keys/{id}/revoke
GET    /api/v1/app-access-keys/{id}/qrcode
```

创建 AK：

```http
POST /api/v1/app-access-keys
```

请求示例：

```json
{
  "name": "Tom iPhone",
  "scope_type": "all",
  "allow_low_risk_ops": true,
  "max_devices": 1,
  "one_time": true
}
```

成功响应中允许返回一次完整 AK：

```json
{
  "id": "ak_01JXYZ",
  "access_key": "MNT-X7K9-3PH2-W8QF",
  "qr_payload": "monitor://pair?server=https%3A%2F%2Fmonitor.example.com&ak=MNT-X7K9-3PH2-W8QF"
}
```

后续查询不得再次返回完整 AK。

---

## 19.3 App 配对认证

首次配对：

```http
POST /api/v1/app/pair
```

请求：

```json
{
  "access_key": "MNT-X7K9-3PH2-W8QF",
  "device": {
    "name": "Tom's iPhone",
    "platform": "ios",
    "app_version": "1.0.0"
  }
}
```

返回：

```json
{
  "device_id": "dev_01JXYZ",
  "access_token": "dt_xxxxxxxxx",
  "refresh_token": "rt_xxxxxxxxx",
  "expires_in": 1800
}
```

App Token 刷新：

```http
POST /api/v1/app/token/refresh
```

App 主动解除本机配对：

```http
POST /api/v1/app/unpair
```

---

## 19.4 App 设备管理与吊销

Web 管理员：

```http
GET  /api/v1/app-devices
GET  /api/v1/app-devices/{id}
POST /api/v1/app-devices/{id}/revoke
```

吊销后：

```text
该设备的 Refresh Token 立即失效
短期 Access Token 通过服务端吊销版本/设备状态校验失效
Push Token 停止发送
设备无法继续订阅 WebSocket
```

---

## 19.5 Server

```http
GET    /api/v1/servers
POST   /api/v1/servers
GET    /api/v1/servers/{id}
PUT    /api/v1/servers/{id}
DELETE /api/v1/servers/{id}
```

---

## 19.6 实时指标

```http
GET /api/v1/servers/{id}/metrics/current
```

---

## 19.7 历史指标

```http
GET /api/v1/servers/{id}/metrics/history
```

参数：

```text
range=1h
range=6h
range=24h
range=7d
range=30d
```

---

## 19.8 流量

```http
GET /api/v1/servers/{id}/traffic/current
GET /api/v1/servers/{id}/traffic/daily
GET /api/v1/servers/{id}/traffic/monthly
```

---

## 19.9 告警

```http
GET    /api/v1/alerts
GET    /api/v1/alert-rules
POST   /api/v1/alert-rules
PUT    /api/v1/alert-rules/{id}
DELETE /api/v1/alert-rules/{id}
```

---

## 19.10 Agent

```http
POST /api/v1/agent/report
GET  /api/v1/agent/config
```

---

# 20. WebSocket

Endpoint：

```text
wss://monitor.example.com/ws
```

消息类型：

```json
{
  "type": "server.metrics",
  "server_id": "hk-dmit-01",
  "data": {
    "cpu": 18.2,
    "memory": 43.1,
    "rx_speed": 8291821,
    "tx_speed": 1288121
  }
}
```

其他事件：

```text
server.online
server.offline
server.metrics
alert.triggered
alert.recovered
```

---

# 21. 数据保留与降采样

不建议永久保存 10 秒粒度。

推荐：

| 时间范围 | 粒度 |
|---|---|
| 最近 24 小时 | 10 秒 |
| 最近 7 天 | 1 分钟 |
| 最近 30 天 | 5 分钟 |
| 最近 1 年 | 1 小时 |

聚合：

```text
avg
max
min
p95
```

例如 CPU：

```text
10 秒原始数据
↓
1 分钟 AVG/MAX
↓
5 分钟 AVG/MAX
↓
1 小时 AVG/MAX
```

实现：monitor-server 内置定时任务，每分钟将上一分钟原始数据聚合写入 `metrics_1m`，以此类推逐级聚合；过期数据按批次删除（每批数千行，避免长事务），并定期执行 `PRAGMA incremental_vacuum`。p95 仅在 1 分钟级计算，更粗粒度使用 max 近似，以降低 SQLite 下的计算成本。

---

# 22. 在线状态判断

默认：

```text
Agent 每 10 秒上报
```

状态规则：

```text
0～30 秒未收到
Online

30～120 秒
Unknown

> 120 秒
Offline
```

可配置。

---

# 23. 安全设计

## 23.1 通信

所有通信必须：

```text
HTTPS
TLS 1.2+
```

禁止：

```text
明文 HTTP Agent 上报
```

---

## 23.2 Agent Token

每台服务器一个独立 Token：

```text
HK-DMIT  → Token A
JP-Zoro  → Token B
US-DMIT  → Token C
```

Token 泄露时只吊销对应服务器。

数据库中不保存明文 Token：

```text
SHA-256 / HMAC Hash
```

---

## 23.3 App AK 与 Device Token

App AK 与长期设备凭证必须分离：

```text
AK
↓
仅用于首次配对
↓
Device Access Token + Refresh Token
```

AK 安全要求：

```text
高熵随机生成
默认一次性
默认最多绑定 1 台设备
支持过期时间
支持手工吊销
数据库只保存安全摘要
完整 AK 只在创建时显示一次
```

建议 AK 随机强度至少：

```text
128 bit
```

展示时可编码为：

```text
MNT-X7K9-3PH2-W8QF-....
```

真实随机熵不能因易读格式而降低。

Device Token：

- Access Token 短期有效。
- Refresh Token 长期有效但必须轮换。
- Refresh Token 仅保存 Hash。
- 每个 App 设备独立凭证。
- Web 可单独吊销任意设备。
- App Device Token 在任何情况下都不能调用 Web 管理员接口（v1 不存在可授予的管理权限）。
- Web 管理员凭证、App Device Token、Agent Token 不能互换使用。

设备吊销应覆盖：

```text
REST API
WebSocket
Push
Token Refresh
```

---

## 23.4 用户密码

使用：

```text
Argon2id
```

或：

```text
bcrypt
```

推荐：

```text
Argon2id
```

禁止：

```text
MD5
SHA1
明文密码
```

---

## 23.5 API 防护

建议：

```text
Rate Limit
CSRF Protection
CORS 白名单
JWT
Refresh Token Rotation
登录失败限制
IP Rate Limit
```

---

# 24. 日志设计

## 24.1 Agent 日志

级别：

```text
DEBUG
INFO
WARN
ERROR
```

默认：

```text
INFO
```

日志轮转：

```text
10 MB
最多 3 个历史文件
```

---

## 24.2 服务端日志

记录：

```text
登录
Token 创建
Token 吊销
App AK 创建
App AK 使用
App AK 吊销
App 设备配对
App 设备吊销
服务器新增
服务器删除
配置修改
告警触发
告警恢复
API Error
数据库 Error
Push Error
```

不记录：

```text
用户密码
完整 JWT
完整 Agent Token
完整 App AK
完整 App Device Token
完整 Refresh Token
```

---

# 25. 服务部署

默认部署形态：**单二进制 + 一个数据目录**。

```bash
# 下载并校验（见第 27 章的校验方式）
monitor-server init --data /var/lib/monitor-server
monitor-server run  --data /var/lib/monitor-server --domain monitor.example.com
```

`--domain` 启用内置自动 HTTPS（ACME）。已有反向代理时省略该参数，监听本地端口即可。

数据目录：

```text
/var/lib/monitor-server/
├── monitor.db          SQLite 数据库
├── monitor.db-wal
├── config.yaml
├── releases/           官方 Agent 版本缓存（含签名 manifest）
├── certs/              ACME 证书（启用内置 HTTPS 时）
└── backups/
```

架构：

```text
Internet
   │
   ▼
monitor-server（单进程）
   ├── /        → 内嵌 Web
   ├── /api     → REST
   ├── /ws      → WebSocket
   └── SQLite
```

其他部署方式（可选，同等支持）：

```text
Docker：单容器，挂载数据目录
Docker Compose：monitor-server + PostgreSQL（大规模用户）
systemd：安装器自动生成 monitor-server.service，以非 root 用户运行
```

备份：

```bash
monitor-server backup --out /path/backup-20261002.db   # 在线备份，不停服务
monitor-server restore --from /path/backup-20261002.db
```

---

# 26. Nginx / Caddy

monitor-server 已内置自动 HTTPS，反向代理不是必需项。

如果同一台机器上已有其他站点，建议使用：

```text
Caddy
```

优点：

```text
自动 HTTPS
配置简单
自动证书续期
```

域名示例：

```text
monitor.example.com
```

---

# 27. Agent 安装

提供两种**同等主推**的安装方式，文档和 Web 页面中并列展示。

## 27.1 手动安装（带校验）

所有二进制均从官方发布地址获取，附带：

```text
monitor-agent-linux-amd64
monitor-agent-linux-arm64
SHA256SUMS
SHA256SUMS.sig        开发者离线私钥签名（Ed25519 / minisign 格式）
```

步骤：

```bash
VER=1.0.0; ARCH=amd64
BASE=https://github.com/<org>/monitor/releases/download/v$VER
curl -fLO $BASE/monitor-agent-linux-$ARCH
curl -fLO $BASE/SHA256SUMS
curl -fLO $BASE/SHA256SUMS.sig

minisign -Vm SHA256SUMS -P <官方公钥，同时公布在 README 与官网>
sha256sum --ignore-missing -c SHA256SUMS

install -m 0755 monitor-agent-linux-$ARCH /usr/local/bin/monitor-agent
monitor-agent install --server https://monitor.example.com --token-file ./agent.token
```

## 27.2 一键脚本

```bash
curl -fsSL https://get.<官方域名>/agent.sh | sh -s -- \
  --server https://monitor.example.com --token-file ./agent.token
```

约束：

```text
脚本只从官方发布地址获取，不由用户的 monitor-server 提供
  （否则面板被攻破即可向新节点下发恶意脚本）
脚本内置官方公钥，下载后自动执行与 27.1 相同的签名和 SHA256 校验
脚本内容短小、可读，提供“先下载再审阅再执行”的写法
```

## 27.3 Token 传递

Web 生成安装命令时：

```text
推荐 --token-file 或环境变量 MONITOR_AGENT_TOKEN
避免 Token 出现在命令行参数中（会进入 shell 历史和 ps 输出）
```

## 27.4 安装器行为

安装逻辑面向主流 VPS Linux，先检测：

```text
OS
uname -m
Kernel
libc
init system
```

然后：

```text
创建系统用户 monitor-agent（无登录 shell）
安装二进制：/usr/local/bin/monitor-agent（root:root 0755）
写入配置：/etc/monitor-agent/config.yaml（root:monitor-agent 0640）
创建状态目录：/var/lib/monitor-agent/（monitor-agent 所有）
安装服务单元（systemd 下同时安装 updater 单元，见 28.1）
```

---

# 28. 服务管理兼容

优先使用 systemd，但安装器必须支持 systemd、OpenRC、SysVinit、runit，并允许前台直接运行。

## 28.1 Systemd

Agent 主服务（非 root，加固）：

```ini
[Unit]
Description=Server Monitor Agent
After=network-online.target
Wants=network-online.target

[Service]
User=monitor-agent
Group=monitor-agent
ExecStart=/usr/local/bin/monitor-agent run
Restart=always
RestartSec=5

NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/monitor-agent
CapabilityBoundingSet=
# 启用 ICMP 探测时：AmbientCapabilities=CAP_NET_RAW，CapabilityBoundingSet=CAP_NET_RAW

[Install]
WantedBy=multi-user.target
```

升级助手（root，按需触发，见 29.13）：

```ini
# /etc/systemd/system/monitor-agent-updater.path
[Path]
PathChanged=/var/lib/monitor-agent/update/request.json

[Install]
WantedBy=multi-user.target
```

```ini
# /etc/systemd/system/monitor-agent-updater.service
[Service]
Type=oneshot
ExecStart=/usr/local/bin/monitor-agent-updater apply
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/usr/local/bin /var/lib/monitor-agent
```

非 systemd 系统（OpenRC、SysVinit、runit）：

```text
不安装特权 updater，远程升级不可用
Web 显示“该节点需手动升级”，并给出带校验的升级命令
本地可运行 monitor-agent upgrade（需 root），流程与 updater 相同
```

---

# 29. Agent 远程升级设计

Agent 支持由 Web 管理端发起的远程升级（第二阶段）。第一阶段只提供节点本地的 `monitor-agent upgrade` 命令，校验流程完全相同。

核心前提：**版本只能由开发者离线签名发布，monitor-server 只能“选择”官方版本，不能“制造”版本。**

升级设计采用：

```text
开发者离线签名 release manifest，发布到官方地址
      │
      ▼
monitor-server 同步 manifest 与二进制（只读缓存）
      │
      ▼
管理员在 Web 选择目标版本，创建升级任务
      │
      ▼
Agent 主动轮询 / 长连接收到升级通知
      │
      ▼
Agent 获取升级任务
      │
      ▼
下载对应平台二进制
      │
      ▼
Agent 校验 SHA256 + 签名，写入升级请求
      │
      ▼
systemd 触发特权 updater，updater 独立复验签名 + 防降级检查
      │
      ▼
本地创建升级事务
      │
      ▼
备份当前版本
      │
      ▼
替换 Agent
      │
      ▼
重启并执行健康检查
      │
      ├── 成功 → 提交升级
      │
      └── 失败 → 自动回滚旧版本
```

## 29.1 Web 管理端升级页面

菜单：

```text
Agent
 ├── Agent 列表
 ├── 版本管理
 ├── 升级任务
 └── 升级历史
```

版本管理页面示例：

```text
Agent 版本

v1.3.0
状态：稳定版
发布时间：2026-10-02

支持架构：
linux/amd64
linux/arm64

SHA256：
xxxxxxxxxxxxxxxx

[查看详情]
[创建升级任务]
```

Web 管理端**不提供上传二进制的功能**。版本来源只有一个：从官方发布地址同步签名 manifest 与对应平台二进制。

```text
[同步官方版本]   自动：每 6 小时检查一次（可关闭）
                手动：离线 / 内网环境可导入官方发布包（manifest + 二进制 + 签名），导入时同样校验签名
```

签名校验失败的版本不会出现在版本列表中。

每个版本记录（来自签名 manifest，面板不可修改）：

```text
version
os
architecture
download_url
sha256
signature
release_channel
release_notes
published_at
mandatory
```

其中 `architecture` 不仅支持 `amd64/arm64`，还应与 Agent 兼容矩阵一致，例如：

```text
amd64
arm64
armv7（可选）
```

---

## 29.2 升级策略

支持以下升级方式：

```text
单台升级
按分组升级
按标签升级
批量升级
全部升级
灰度升级
```

示例：

```text
先升级 2 台测试 VPS
      ↓
观察 30 分钟
      ↓
升级香港组
      ↓
升级其余服务器
```

支持升级通道：

```text
stable
beta
dev
```

服务器可以绑定：

```text
stable
```

只有显式切换到 beta 的服务器才接收 beta 版本。

---

## 29.3 升级模式

### 非强制升级

Web 下发升级任务后：

```text
Agent 在下一次检查时发现新版本
↓
后台下载
↓
完成校验
↓
执行升级
```

### 强制升级

用于：

```text
严重 Bug
安全漏洞
协议不兼容
```

服务器下发：

```text
mandatory = true
```

Agent 应优先执行。

---

## 29.4 Agent 升级检查

Agent 默认：

```text
每 5 分钟检查一次升级任务
```

也可以通过 WebSocket 通知：

```text
agent.upgrade.available
```

但 Agent 必须保留轮询机制，避免 WebSocket 中断导致无法升级。

Endpoint：

```http
GET /api/v1/agent/upgrade
```

Header：

```http
Authorization: Bearer AGENT_TOKEN
```

请求参数：

```text
current_version
os
arch
server_id
```

示例：

```http
GET /api/v1/agent/upgrade?current_version=1.2.0&os=linux&arch=amd64
```

---

## 29.5 升级任务返回

示例：

```json
{
  "upgrade": true,
  "task_id": "upgrade_20261002_001",
  "version": "1.3.0",
  "mandatory": false,
  "download_url": "https://monitor.example.com/api/v1/agent/releases/v1.3.0/linux/amd64",
  "manifest": "BASE64_MANIFEST",
  "manifest_signature": "BASE64_ED25519_SIGNATURE"
}
```

无升级任务：

```json
{
  "upgrade": false
}
```

---

## 29.6 下载安全

Agent 不直接执行网络上下载到的文件。

完整流程：

```text
下载到临时目录

/var/lib/monitor-agent/update/
```

例如：

```text
monitor-agent.new
```

首先校验：

```text
文件大小
SHA256
数字签名
平台
架构
版本号
```

全部通过后才能进入替换流程。

---

## 29.7 数字签名

### 29.7.1 密钥归属

```text
发布私钥：开发者离线保管（硬件密钥 / 离线签名机），不进入 CI，不进入任何 monitor-server
发布公钥：编译时内置于 monitor-agent 与 monitor-agent-updater
```

monitor-server **不持有任何签名私钥**。如果服务端持有私钥，面板被攻破就等于能向所有节点下发任意代码，整个安全设计失效。

### 29.7.2 签名对象

签名的是 release manifest，而不是单个字段：

```json
{
  "product": "monitor-agent",
  "version": "1.3.0",
  "channel": "stable",
  "released_at": "2026-10-02T08:00:00Z",
  "min_upgradable_from": "1.0.0",
  "artifacts": [
    {"os": "linux", "arch": "amd64", "size": 6291456, "sha256": "..."},
    {"os": "linux", "arch": "arm64", "size": 5980160, "sha256": "..."}
  ]
}
```

Agent / updater 校验：

```text
manifest 签名有效（Ed25519）
product = monitor-agent
version > 当前版本（防降级，见 29.7.4）
当前版本 ≥ min_upgradable_from
存在匹配本机 os/arch 的条目
下载文件 size 与 sha256 一致
```

### 29.7.3 密钥轮换

```text
Agent 内置两把公钥：current + next
需要轮换时，用 current 签发包含新公钥集合的版本
next 公钥平时离线保存，current 泄露时用 next 签发紧急版本
```

### 29.7.4 防降级

攻击者控制面板后，可能尝试把节点“升级”回存在已知漏洞的旧版本。因此：

```text
updater 拒绝安装版本号低于当前版本的 manifest
唯一例外：自动回滚到本机 backup 目录中的上一个版本（不经过网络）
需要主动降级时，只能在节点本地执行 monitor-agent upgrade --allow-downgrade
```

### 29.7.5 透明度

```text
每个版本的 SHA256SUMS 与签名同时公布在官方 Release 页面
构建过程开源，目标为可复现构建，用户可自行比对
```

---

## 29.8 本地升级目录

建议：

```text
/usr/local/bin/monitor-agent

/var/lib/monitor-agent/
├── update/
│   └── monitor-agent.new
├── backup/
│   └── monitor-agent-1.2.0
└── state/
    └── upgrade.json
```

---

## 29.9 原子替换

不能直接覆盖正在运行的可执行文件。

推荐：

```text
1. 下载 monitor-agent.new
2. 校验
3. chmod +x
4. 备份当前版本
5. rename 原子替换
6. systemctl restart monitor-agent
```

备份：

```text
/usr/local/bin/monitor-agent
        ↓
/var/lib/monitor-agent/backup/monitor-agent-1.2.0
```

新版本：

```text
monitor-agent.new
        ↓
/usr/local/bin/monitor-agent
```

---

## 29.10 升级状态机

升级状态：

```text
PENDING
DOWNLOADING
VERIFYING
INSTALLING
RESTARTING
HEALTH_CHECK
SUCCESS
FAILED
ROLLBACK
ROLLED_BACK
```

Web 可以实时查看：

```text
DMIT-HK
v1.2.0 → v1.3.0

状态：
HEALTH_CHECK
```

---

## 29.11 升级健康检查

新版本启动后不能立即判定升级成功。

必须至少检查：

```text
进程正常运行
Agent 能读取配置
Agent 能采集 CPU
Agent 能连接中央服务端
Agent 能成功上报一次数据
版本号正确
```

例如：

```text
健康检查超时：60 秒
```

如果 60 秒内没有完成健康检查：

```text
自动回滚
```

---

## 29.12 自动回滚

升级前：

```text
当前版本：1.2.0
```

升级：

```text
1.3.0
```

如果启动失败：

```text
systemd 检测异常
      │
      ▼
升级助手恢复：
monitor-agent-1.2.0
      │
      ▼
重新启动 Agent
```

回滚完成后向服务端报告：

```json
{
  "task_id": "upgrade_20261002_001",
  "status": "rolled_back",
  "from_version": "1.2.0",
  "target_version": "1.3.0",
  "reason": "health_check_timeout"
}
```

---

## 29.13 独立升级助手（特权分离）

Agent 以非 root 用户运行，没有权限替换 `/usr/local/bin/monitor-agent` 或重启自身服务。因此升级拆分为两个进程：

```text
monitor-agent（monitor-agent 用户）
  1. 从 monitor-server 获取升级任务
  2. 下载二进制到 /var/lib/monitor-agent/update/
  3. 首次校验（失败直接上报，不打扰 updater）
  4. 写入 /var/lib/monitor-agent/update/request.json
         │
         │ systemd path unit 检测到文件变化
         ▼
monitor-agent-updater（root，oneshot，执行完即退出）
  1. 读取 request.json（视为不可信输入）
  2. 用自身内置公钥重新校验 manifest 签名与文件 sha256
  3. 防降级检查
  4. 备份当前版本 → 原子替换 → 重启 monitor-agent
  5. 等待健康检查（见 29.11）
  6. 失败则从本机备份回滚
  7. 写入结果文件，由 Agent 上报
```

updater 设计约束：

```text
单一职责，代码量尽量小，便于审计
不联网，不解析来自 monitor-server 的任何指令
只替换 /usr/local/bin/monitor-agent 这一个路径
自身不通过该流程升级（随 Agent 安装包或系统包管理器更新）
config.yaml 中 upgrade.remote = false 时直接拒绝执行
```

即使 monitor-agent 进程被攻破，攻击者也只能让 updater 安装一个合法签名的更高版本。

---

## 29.14 Web 升级任务

升级任务数据：

```text
task_id
target_version
target_scope
upgrade_mode
status
created_by
created_at
started_at
completed_at
```

目标可以是：

```text
SERVER
GROUP
TAG
ALL
```

---

## 29.15 单机升级状态

例如：

```text
升级任务：v1.3.0

DMIT-HK       SUCCESS
Zoro-JP       SUCCESS
Oracle-SG     FAILED
US-DMIT       PENDING
```

点击失败项查看：

```text
当前版本：1.2.0
目标版本：1.3.0

失败阶段：
HEALTH_CHECK

失败原因：
Agent 启动后 60 秒内未成功上报

处理结果：
已自动回滚到 1.2.0
```

---

## 29.16 灰度升级（第二阶段）

Web 创建任务：

```text
目标版本：1.3.0

第一批：
2 台
观察时间：
30 分钟

第二批：
20%

第三批：
全部
```

灰度期间如果出现：

```text
升级失败率过高
Agent 离线
CPU 异常
大量上报异常
```

可自动：

```text
暂停后续升级
```

---

## 29.17 Agent 版本兼容

服务端维护：

```text
minimum_supported_version
recommended_version
latest_version
```

例如：

```text
minimum_supported_version = 1.1.0
recommended_version = 1.3.0
latest_version = 1.3.0
```

如果 Agent 版本：

```text
1.0.0
```

则 Web 显示：

```text
版本过旧
建议立即升级
```

---

## 29.18 升级权限

只有：

```text
admin
```

可以：

```text
同步 / 导入官方版本
创建升级任务
暂停任务
取消任务
执行回滚
```

Viewer 只能查看升级状态。

---

## 29.19 升级审计日志

记录：

```text
版本同步 / 导入记录（含签名校验结果）
谁创建升级任务
目标服务器
旧版本
新版本
升级时间
升级结果
失败原因
是否回滚
```

示例：

```text
2026-10-02 16:40
admin
DMIT-HK
1.2.0 → 1.3.0
SUCCESS
```

---

## 29.20 升级 API

版本管理：

```http
GET  /api/v1/agent-releases
POST /api/v1/agent-releases/sync      从官方地址同步
POST /api/v1/agent-releases/import    导入官方发布包（离线环境，仍需签名校验）
GET  /api/v1/agent-releases/{version}
```

升级任务：

```http
GET  /api/v1/upgrade-tasks
POST /api/v1/upgrade-tasks
GET  /api/v1/upgrade-tasks/{id}
POST /api/v1/upgrade-tasks/{id}/pause
POST /api/v1/upgrade-tasks/{id}/resume
POST /api/v1/upgrade-tasks/{id}/cancel
```

Agent：

```http
GET  /api/v1/agent/upgrade
POST /api/v1/agent/upgrade/status
```

---

## 29.21 安全限制

远程升级功能只允许：

```text
下载并安装受信任的 monitor-agent 版本
```

不设计为：

```text
远程执行任意 Shell
远程执行任意命令
远程上传并执行任意二进制
```

这样可以降低后台账号或 Token 泄露后的风险。

必须满足：

```text
HTTPS
签名私钥由开发者离线保管，monitor-server 无签名能力
monitor-server 不提供二进制上传
签名 manifest + SHA256
Agent 与 updater 双重校验，公钥内置
防降级
特权分离：Agent 非 root，updater 按需触发
节点本地可关闭远程升级
独立 Agent Token
完整审计日志
失败自动回滚
```

威胁模型小结：

| 被攻破的部分 | 攻击者能做的 | 不能做的 |
|---|---|---|
| App 设备凭证 | 查看数据、静音告警 | 修改配置、触发升级 |
| Agent Token | 伪造该节点的上报数据 | 读取其他节点、执行代码 |
| monitor-agent 进程 | 读取本机指标、伪造上报 | 获取 root、安装未签名二进制 |
| monitor-server 整机 | 读取 / 篡改监控数据，升级到官方新版本 | 在节点上执行任意代码、降级 |
| Push Relay | 丢弃或延迟推送 | 读取告警内容 |
| 开发者签名私钥 | 签发恶意版本（最高风险，因此离线保管） | — |

---


# 30. 原生 Push 的隐私设计

App 的核心原则是：

```text
监控数据不经过开发者后台
```

但 iOS 原生 Push 必须使用开发者的 APNs Provider 凭证发送，Android FCM 同样需要应用方凭证。也就是说，**官方 App 的原生后台推送无法绕开开发者运营的服务**。

本项目不回避这一点，而是保证：经过开发者的只有密文。

## 30.1 推送通道总览

| 通道 | 是否经过开发者 | 默认 | 说明 |
|---|---|---|---|
| 官方原生 Push（APNs / FCM） | 经过 Relay，仅密文 | **默认启用** | App 的核心体验 |
| UnifiedPush / 自建 ntfy（Android） | 不经过 | 可选 | 无 Google 服务的设备首选 |
| 自建 Relay + 自签 App（iOS） | 不经过 | 可选 | 需要自己的 Apple 开发者账号 |
| Telegram / Webhook / Email / ntfy | 不经过 | 可选 | monitor-server 直接发送 |

---

## 30.2 官方原生 Push：无状态 Relay

链路：

```text
用户的 monitor-server
   │ 1. 用设备公钥加密告警内容
   │ 2. HTTPS 发送 { push_token, ciphertext }
   ▼
官方 Push Relay（开源）
   │ 3. 内存中转发，立即丢弃
   ├── APNs
   └── FCM
   ▼
用户手机
   4. App / Notification Service Extension 用本地私钥解密并展示
```

Relay 只负责：

```text
收到请求
校验实例签名与速率限制
在内存中转发
立即丢弃
```

开发者不保存：

```text
服务器信息
监控指标
告警内容
用户账号
AK / Device Token / Refresh Token
APNs / FCM Device Token
monitor-server 地址
```

Push Token 的持久化位置是用户自己的 monitor-server，每次发送时临时携带。

### 30.2.1 防滥用

Relay 无账号体系，但需要防止被当作免费推送通道滥用：

```text
monitor-server 首次启用 Push 时生成匿名实例密钥对（Ed25519）
每个请求携带实例公钥与请求签名
Relay 按实例公钥哈希在内存中计数限流（例如每实例每分钟 30 条、每天 2000 条）
计数器只在内存中，进程重启即清零，不落库
```

实例密钥不包含任何用户或服务器信息，也不与 App 内购关联。

---

## 30.3 端到端加密

### 30.3.1 密钥

```text
App 配对时生成 X25519 密钥对（见 12.3）
私钥：iOS Keychain（与 Notification Service Extension 共享的 Access Group）/ Android Keystore 包裹存储
公钥：存入 monitor-server 的 push_devices.push_public_key
```

### 30.3.2 加密

每条消息：

```text
monitor-server 生成临时 X25519 密钥对
共享密钥 = X25519(临时私钥, 设备公钥)
用 HKDF 派生对称密钥
ChaCha20-Poly1305 加密 payload
发送：临时公钥 + nonce + 密文
```

即 HPKE（RFC 9180）的 Base 模式，推荐直接使用成熟实现，不自行拼装。

明文 payload：

```json
{
  "center_id": "c_9f2a",
  "event_id": "evt_01JXYZ",
  "server_name": "DMIT-HK",
  "title": "已离线",
  "body": "超过 120 秒未收到 Agent 上报",
  "severity": "critical",
  "ts": 1790928000
}
```

### 30.3.3 平台实现

```text
iOS
  APNs alert + mutable-content: 1
  外层 alert 文本固定为“服务器告警”（解密失败时的兜底显示）
  Notification Service Extension 解密后替换标题与正文

Android
  FCM data message（无 notification 字段）
  App 后台处理器解密后创建本地通知
  UnifiedPush 通道使用相同的密文格式
```

### 30.3.4 Relay 与 APNs / FCM 能看到的

```text
目标 Push Token
密文
发送时间
消息大小（payload 补齐到固定长度档位，减少长度泄露）
```

看不到：

```text
服务器名称、IP、域名
CPU、内存、流量
告警原因
monitor-server 地址
```

---

## 30.4 开源与自建

```text
Relay 代码与 monitor-server 同仓库开源
官方 Relay 的部署配置公开
用户可以自建 Relay，monitor-server 中修改 Relay 地址即可
```

需要说明的限制：

```text
iOS：APNs 凭证绑定 App 的 Bundle ID，自建 Relay 只能配合自签名构建的 App 使用
Android：自建 Relay 同样受 FCM 项目绑定限制，推荐直接使用 UnifiedPush / ntfy
```

---

## 30.5 无日志要求

官方 Push Relay：

```text
不记录请求 Body
不记录 Push Token
不记录实例公钥
不记录来源 IP（反向代理层同样关闭访问日志）
```

仅允许保留聚合运行指标：

```text
总体请求数
总体成功率
总体错误率
```

且不能关联到具体实例、用户或服务器。

---

## 30.6 Relay 不可用时

```text
monitor-server 发送失败时重试 3 次（指数退避）
仍失败则在 Web / App 事件中心记录“Push 投递失败”
若用户同时配置了 Telegram 等渠道，照常发送（建议至少配置一个备用渠道）
App 打开时始终从 monitor-server 拉取完整事件列表，不依赖 Push 是否送达
```

---

# 31. 通知渠道

第一阶段：

```text
App Push（E2E 加密，官方 Relay）
UnifiedPush / ntfy（Android 完全自持）
Telegram
Webhook
```

第二阶段：

```text
Email
Discord
企业微信
钉钉
飞书
Bark
```

所有渠道均由用户的 monitor-server 直接发送；只有官方 App Push 经过 Relay，且仅密文。

---

# 32. 流量预测

计算：

```text
当前周期已使用流量
÷
已过去天数
=
日均流量
```

预测：

```text
日均流量 × 周期总天数
```

示例：

```text
已用：638 GB
周期已过去：20 天
周期总长度：30 天
```

日均：

```text
31.9 GB/day
```

预测：

```text
957 GB
```

状态：

```text
预计不会超限
```

如果：

```text
预测 > 套餐流量
```

则提示：

```text
预计超出套餐流量
```

补充规则：

```text
计算基于校准后的已用量（见 5.7）
周期开始不足 3 天时不显示预测，避免样本过少导致误报
优先使用最近 7 天日均，周期日均作为兜底，更贴近近期用量变化
预测超限时提前推送一次，而不是每次刷新都推送
```

---

# 33. 后续扩展

## 33.1 网络质量

增加：

```text
ICMP Ping
Latency
Packet Loss
TCP Connect
HTTP / HTTPS
DNS Resolve
IPv4 Connectivity
IPv6 Connectivity
多地区 Agent 线路对比
```

---

## 33.2 服务监控

支持：

```text
HTTP
HTTPS
TCP Port
DNS
ICMP
```

例如：

```text
443
22
80
3306
5432
```

---

## 33.3 SSL

监控：

```text
SSL 到期时间
证书签发者
域名
剩余天数
```

告警：

```text
30 天
14 天
7 天
3 天
```

---

## 33.4 Docker

采集：

```text
容器数量
运行中
停止
CPU
Memory
Restart Count
```

---

## 33.5 进程

可配置：

```text
nginx
docker
sshd
mysql
postgres
```

检测：

```text
Running
Stopped
```

---

## 33.6 VPS 资产管理

每台服务器增加：

```text
供应商
机房
地区
套餐
CPU
内存
SSD
带宽
流量
购买价格
续费价格
购买日期
到期日期
支付周期
```

形成：

```text
VPS 资产管理
+
服务器监控
```

---

# 34. 项目目录建议

## 34.1 后端

```text
monitor-server/
├── cmd/
│   └── server/
├── internal/
│   ├── auth/
│   ├── agent/
│   ├── server/
│   ├── metric/
│   ├── traffic/
│   ├── alert/
│   ├── notification/
│   ├── pushcrypto/
│   ├── release/        官方版本同步与签名校验（无签名能力）
│   ├── store/          sqlite / postgres 实现
│   └── websocket/
├── web/                构建产物，go:embed 内嵌
├── pkg/
├── migrations/
│   ├── sqlite/
│   └── postgres/
├── configs/
└── Dockerfile
```

Push Relay：

```text
push-relay/
├── cmd/relay/
├── internal/
│   ├── apns/
│   ├── fcm/
│   └── ratelimit/      仅内存
└── deploy/             官方部署配置（公开）
```

---

## 34.2 Agent

```text
monitor-agent/
├── cmd/
│   └── agent/
├── internal/
│   ├── collector/
│   │   ├── cpu.go
│   │   ├── memory.go
│   │   ├── disk.go
│   │   └── network.go
│   ├── reporter/
│   ├── upgrade/        下载与首次校验
│   └── config/
├── cmd/updater/        monitor-agent-updater（特权、极小）
├── internal/verify/    签名校验（Agent 与 updater 共用，内置公钥）
├── scripts/
│   └── agent.sh        一键安装脚本（发布到官方地址）
└── Dockerfile
```

---

## 34.3 Web

```text
monitor-web/
├── src/
│   ├── api/
│   ├── assets/
│   ├── components/
│   ├── layouts/
│   ├── router/
│   ├── stores/
│   └── views/
│       ├── login/
│       ├── dashboard/
│       ├── servers/
│       ├── alerts/
│       └── settings/
└── package.json
```

---

## 34.4 App

```text
monitor-app/
├── lib/
│   ├── api/
│   ├── models/
│   ├── providers/
│   ├── routes/
│   ├── services/
│   ├── widgets/
│   └── pages/
│       ├── pairing/
│       ├── home/
│       ├── server/
│       ├── alerts/
│       └── settings/
└── pubspec.yaml
```

---

# 35. 第一阶段开发范围（MVP）

目标：一个人或小团队在合理时间内交付，并且 v1 就能完整体现三个卖点：**App 好用、安全可信、部署轻**。

主线：

```text
Agent 基础指标 + 流量
  → 单二进制 Server（SQLite）
  → 最小 Web（服务器、Agent Token、AK、告警规则、流量套餐）
  → App（配对、列表、详情、流量套餐、E2E Push）
```

## Agent

```text
CPU / Load / 内存 / Swap / 磁盘 / 网速 / 累计流量 / Uptime / 系统信息
boot_id 与 ifindex 上报，SIGTERM 补报
网卡自动选择与虚拟网卡排除
HTTPS 上报，断网短缓存
非 root 运行 + systemd 加固
amd64 / arm64
monitor-agent doctor
monitor-agent upgrade（本地触发，签名校验 + 防降级 + 回滚）
```

## Server

```text
单二进制，内嵌 Web
SQLite（WAL），批量写入，内存实时状态
降采样与过期清理
内置自动 HTTPS（可选）
Web 登录（Argon2id）
Agent Token
App AK / 配对 / Device Token / 设备吊销
在线状态
告警（离线、CPU、内存、磁盘、流量阈值、到期）
流量统计周期、计费模式、手动校准、单位口径
VPS 到期日期与提醒
E2E Push 加密 + 官方 Relay 对接
Telegram / Webhook / ntfy
在线备份 / 恢复命令
```

## Web

```text
登录
服务器列表与详情（含历史曲线）
添加服务器 / 生成安装命令（手动校验与一键脚本并列）
流量套餐配置与校准
到期信息
告警规则
App 接入：AK、二维码、已连接设备、吊销
系统设置（通知渠道、Relay 地址）
```

## App

```text
iOS / Android
扫码配对 / 手工填写
多监控中心添加与切换
首页：状态总览 + 异常优先
服务器列表（搜索、收藏、智能排序）
服务器详情：实时模式、历史曲线、流量套餐卡片
事件中心
E2E Push（APNs / FCM / UnifiedPush）
静音 / 维护模式
生物识别锁、隐私模式
离线缓存
```

## Push Relay

```text
无状态转发 APNs / FCM
实例签名校验 + 内存限流
开源，无日志部署
```

## 第一阶段明确不做

```text
面板发起的远程升级与灰度升级
Widget
多监控中心聚合视图
网络探测（Ping / TCP / HTTP）
Docker / 进程监控
PostgreSQL 后端
多用户 / viewer 角色（只保留单管理员）
```

---

# 36. 第二阶段开发范围

```text
面板发起的远程升级（特权 updater）+ 灰度升级 + 升级审计
iOS / Android Widget
多监控中心聚合视图
ICMP / TCP / HTTP / DNS 网络探测，延迟与丢包
Agent → Agent 线路对比
SSL 到期
Docker / Podman 基础监控
进程 / 服务基础监控
Heartbeat（监控面板自身存活）
费用统计（按币种汇总月度 / 年度开销）
PostgreSQL 后端与迁移命令
viewer 角色
Email / Discord / Bark 等更多通知渠道
2FA（TOTP / Passkey）
App Pro 内购
```

---

# 37. 推荐最终技术栈

```text
Agent
  Go（单文件，无 CGO）
  monitor-agent-updater（Go，极小）

Backend
  Go + Gin，单二进制，go:embed 内嵌 Web

Database
  SQLite WAL（默认，modernc.org/sqlite）
  PostgreSQL（可选）

Web
  Vue 3
  TypeScript
  Element Plus
  ECharts

App
  Flutter

Push
  APNs / FCM，经开源无状态 Relay，HPKE 端到端加密
  UnifiedPush / ntfy（Android 可选）

HTTPS
  内置 ACME（certmagic），或外部 Caddy

Release
  开发者离线 Ed25519 签名（minisign 格式）
  GitHub Releases 分发

Deployment
  单二进制 + systemd（默认）
  Docker（可选）
```

---

# 38. 最终产品结构

```text
monitor-agent
服务器监控采集程序

monitor-server
中央 API / 数据 / 告警服务

monitor-web
Web 管理后台

monitor-app
iOS / Android App

push-relay
开源无状态推送中继（仅转发密文）

monitor-agent-updater
特权升级助手（复验签名、防降级、回滚）

认证关系：
Web = 用户名/密码 + Web JWT
App = AK 首次配对 + Device Token（只读 + 低风险操作）
Agent = 独立 Agent Token
Release = 开发者离线签名，monitor-server 只能选择不能签发
```

最终系统定位：

```text
多服务器统一监控
+
网络流量管理
+
VPS 资产管理
+
移动告警
+
App-first
+
Self-hosted / Local-first
```

数据边界：

```text
开发者提供软件与离线签名的版本
开发者运营 Push Relay，但只能看到密文
用户保存自己的数据
用户控制自己的 monitor-server
App 直接连接用户自己的服务
开发者不建立用户监控数据中心
```

适合作为个人或小团队长期运行的轻量服务器监控平台。
