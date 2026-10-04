// 由 scripts/gen-api-types.rb 根据 api/openapi.yaml 生成，不要手改（设计 19.0.1）。
// 修改接口时先改 api/openapi.yaml，再运行 make api-types。
/* eslint-disable */

/** 稳定的错误码，一经发布不再改名（设计 43.4） */
export type ErrorCode = 'validation_failed' | 'bad_request' | 'unauthorized' | 'token_revoked' | 'forbidden' | 'not_found' | 'conflict' | 'payload_too_large' | 'enroll_code_invalid' | 'rate_limited' | 'quota_exceeded' | 'unavailable' | 'internal' | 'password_change_required' | 'reauth_required' | 'captcha_failed' | 'unsupported_encoding'

export interface ErrorBody {
  error: {
    code: ErrorCode
    /** 中文提示，可直接展示给用户 */
    message: string
    request_id: string
    /** 字段级错误，仅 validation_failed 使用 */
    details?: {
      field: string
      message: string
    }[]
  }
}

export interface Me {
  username: string
  /** 使用初始 / 重置密码登录，需先修改密码 */
  must_change_password: boolean
  /** 修改类请求放在 X-CSRF-Token 头中 */
  csrf_token: string
}

/** 注册码有效期（设计 27.2） */
export type EnrollTTL = '1h' | '24h' | '7d'

/** 未知字段会被忽略，保证新版 Agent 与旧面板兼容 */
export interface EnrollRequest {
  /** 大小写不敏感 */
  enroll_code: string
  hostname?: string
  /** sha256(/etc/machine-id)，用于重试幂等与识别更换主机 */
  machine_id_hash?: string
  os?: string
  os_version?: string
  arch?: string
  agent_version?: string
  /** 可选：更换 Token（rotate-token）时本机当前所属的节点；注册码属于其他节点时返回 403 且不做任何修改（设计 17.2） */
  server_id?: number
}

export interface EnrollResponse {
  server_id: number
  server_name: string
  /** agt_…，写入 /etc/vpsmon-agent/token 后不再传输 */
  agent_token: string
  /** 主机名 / IP 与填写值不一致时的提示（设计 27.6.3） */
  warnings: string[]
}

export interface CreateServerRequest {
  name: string
  /** 注册时核对 */
  expected_hostname?: string
  expected_ipv4?: string
  expected_ipv6?: string
  group?: string
  note?: string
  provider?: string
  plan?: string
  region?: string
  /** ISO 3166-1 两位代码（大小写不敏感，保存为大写）；用于显示国旗 */
  country?: string
  /** 服务商标称带宽（端口速率），Mbps；0 或省略表示未填（设计 27.2） */
  bandwidth_mbps?: number
  /** 采样间隔秒数；0 或省略表示默认 10 秒（设计 4.2、6.1） */
  report_interval_s?: 0 | 5 | 10 | 15 | 30 | 60
  /** 计费时区（IANA 名称）；空或省略表示面板时区。只影响之后写入的流量（设计 5.4） */
  traffic_timezone?: string
  /** 按 traffic_unit 口径的 GB / GiB（设计 5.8），0 或省略表示不限 */
  traffic_limit_gb?: number
  /** decimal：1 GB = 10^9 字节；binary：1 GiB = 2^30 字节（设计 5.8） */
  traffic_unit?: 'decimal' | 'binary'
  /** 统计系数，修正固定比例偏差（设计 5.7） */
  traffic_factor?: number
  traffic_reset_day?: number
  /** 设计 1.2.4 */
  traffic_count_mode?: 'sum' | 'rx' | 'tx' | 'max'
  /** 续费价格，保留两位小数 */
  price?: number
  /** ISO 4217；填写价格时必填 */
  currency?: string
  billing_period?: '' | 'monthly' | 'quarterly' | 'semiannually' | 'annually' | 'biennially' | 'triennially' | 'one_time'
  /** YYYY-MM-DD */
  expire_date?: string
  enroll_ttl?: EnrollTTL
  /** 核对严格程度（设计 27.6.3） */
  verify_mode?: 'warn' | 'strict'
}

export interface InstallCommand {
  /** 面板尚未同步并验签任何官方版本时只有 manual（设计 27.3.1） */
  mode: 'default' | 'manual'
  /** default：下载按版本固定的脚本 → 按已验签清单中的 SHA256 校验 → 执行；不使用管道（设计 27.3.1） */
  command: string
  /** 程序已在主机上时的注册命令（设计 27.3.3），两种模式都提供 */
  manual_command: string
  /** 主机上已安装 Agent（如 Token 已吊销）时，凭新注册码就地更换 Token（设计 17.2） */
  rotate_command: string
  /** 写进命令的面板对外地址 */
  server: string
  /** 生成默认命令所用的已验签官方正式版 */
  release: AgentRelease | null
}

/** 已同步并验签的官方 Agent 版本（设计 29.1）；每次读取都重新验签清单原文 */
export interface AgentRelease {
  version: string
  channel: 'stable' | 'beta'
  /** 签名所用官方公钥的 ID */
  key_id: string
  installer_file: string
  installer_sha256: string
  released_at: string
  synced_at: number
  notes?: string
  artifacts?: {
    os?: string
    arch?: string
    file: string
    size: number
    sha256: string
  }[]
  /** 全部文件已校验并保存在本面板（设计 27.5.3） */
  mirrored: boolean
}

export interface QuietHours {
  enabled: boolean
  /** HH:MM；晚于 end 表示跨午夜 */
  start: string
  end: string
  /** IANA 时区名；空为面板本地时区 */
  timezone: string
  critical: 'notify' | 'summary'
}

export type QuietHoursView = QuietHours & {
  /** 当前是否处于免打扰 */
  active: boolean
  effective_timezone: string
  /** 已暂存、等待汇总的通知数 */
  held: number
}

/** 通知渠道。凭证脱敏：bot_token 只保留 ID 与末 4 位，Webhook 地址只保留协议与主机，签名密钥只返回是否已设置 */
export interface NotifyChannel {
  id: number
  type: 'telegram' | 'webhook'
  name: string
  enabled: boolean
  /** 达到该级别才发送 */
  min_severity: 'critical' | 'warning' | 'info'
  notify_resolved: boolean
  config: {
    bot_token?: string
    chat_id?: string
    url?: string
    has_secret?: boolean
  }
  created_at: number
  updated_at: number
}

export interface NotifyChannelInput {
  /** 创建时必填，不可修改 */
  type?: 'telegram' | 'webhook'
  name: string
  enabled?: boolean
  min_severity?: 'critical' | 'warning' | 'info'
  notify_resolved?: boolean
  /** 修改时凭证字段留空表示保持原值 */
  config: {
    /** Telegram Bot Token（@BotFather） */
    bot_token?: string
    /** 数字 ID（群组为负数）或 @频道名 */
    chat_id?: string
    /** Webhook 地址；只允许 HTTPS，回环地址除外；不跟随重定向 */
    url?: string
    /** 可选，请求头 X-Vpsmon-Signature 为 sha256=HMAC-SHA256(secret */
    secret?: string
    clear_secret?: boolean
  }
}

export interface Delivery {
  id: number
  channel_id: number
  channel_name: string
  channel_type: 'telegram' | 'webhook'
  /** 测试通知为 0 */
  event_id: number
  server_name: string
  /** quiet_summary：免打扰结束后的汇总；flapping：状态频繁变化（之后暂停该告警的通知）；still_firing：抖动结束时仍在告警；panel_down / panel_up：面板自检（设计 16.4） */
  kind: 'firing' | 'resolved' | 'repeat' | 'test' | 'flapping' | 'still_firing' | 'panel_down' | 'panel_up' | 'quiet_summary'
  title: string
  status: 'sent' | 'failed' | 'retrying'
  attempts: number
  /** 不含请求地址与凭证 */
  last_error: string
  created_at: number
  sent_at: number
}

/** 远程升级任务（设计 29.10、29.13） */
export interface UpgradeTask {
  id: number
  server_id: number
  server_name: string
  target_version: string
  /** 创建任务时节点上报的版本 */
  from_version: string
  status: 'pending' | 'delivered' | 'staged' | 'success' | 'failed' | 'rolled_back' | 'cancelled'
  /** 失败或回滚原因 */
  reason: string
  created_by: string
  created_at: number
  updated_at: number
}

export interface EnrollCodeView {
  server_id: number
  server_name: string
  enroll_state: 'pending' | 'enrolled'
  /** 完整注册码，只在新建与重新生成时返回 */
  enroll_code?: string
  enroll_code_hint: string
  enroll_status: 'ACTIVE' | 'USED' | 'REVOKED' | 'EXPIRED' | 'NONE'
  /** Unix 秒 */
  enroll_expires_at: number
  install: InstallCommand
}

export interface ServerView {
  id: number
  name: string
  /** ≤30 秒在线，≤120 秒未知（设计 22）；pending 为待安装（设计 27.7） */
  status: 'online' | 'unknown' | 'offline' | 'pending'
  last_seen_at: number
  enroll_state: 'pending' | 'enrolled'
  expected_hostname: string
  expected_ipv4: string
  expected_ipv6: string
  verify_mode: 'warn' | 'strict'
  hostname: string
  ipv4: string
  ipv6: string
  enrolled_at: number
  group: string
  note: string
  provider: string
  plan: string
  region: string
  /** ISO 3166-1 两位代码（大写），空表示未填 */
  country: string
  /** 标称带宽 Mbps，0 表示未填 */
  bandwidth_mbps: number
  /** 采样间隔秒数，0 表示默认 10 秒 */
  report_interval_s: number
  /** 计费时区（IANA），空表示面板时区 */
  traffic_timezone: string
  /** 续费价格 × 100 */
  price_cents: number
  currency: string
  billing_period: string
  expire_date: string
  traffic_limit_bytes: number
  traffic_reset_day: number
  traffic_count_mode: 'sum' | 'rx' | 'tx' | 'max'
  created_at: number
  latest?: Report
  traffic_unit: 'decimal' | 'binary'
  traffic_factor: number
  traffic: TrafficView
  /** 活动告警（firing），严重在前；同一类型只保留最严重的一条；节点离线时只有离线告警（设计 16.4） */
  alerts: ({
    event_id: number
    rule_key: string
    type: string
    severity: 'info' | 'warning' | 'critical'
    message: string
    /** 当前值：百分比、离线秒数或负载倍数 */
    value: number
    fired_at: number
    /** 已静音：照常记录，不通知，不计入需要关注 */
    silenced: boolean
  })[]
  maintenance?: Silence
  muted?: Silence
}

export interface Silence {
  id: number
  scope_type: 'server' | 'group' | 'rule' | 'global'
  scope_id: string
  kind: 'mute' | 'maintenance'
  reason: string
  starts_at: number
  /** null 表示直到手动结束 */
  ends_at: number | null
  created_by: string
}

export interface AlertRule {
  id: number
  /** 同一规则在三层中的标识，下层按它覆盖上层 */
  rule_key: string
  scope_type: 'global' | 'group' | 'server'
  scope_id: string
  type: 'offline' | 'cpu' | 'memory' | 'disk' | 'swap' | 'load' | 'traffic' | 'traffic_forecast' | 'agent_clock'
  operator: '>' | '>='
  threshold: number
  /** 低于此值恢复（回差） */
  recover_threshold: number
  duration_s: number
  recover_duration_s: number
  severity: 'info' | 'warning' | 'critical'
  repeat_interval_s: number
  enabled: boolean
}

export interface AlertEvent {
  id: number
  rule_id?: number
  rule_key: string
  server_id: number
  server_name: string
  type: string
  severity: 'info' | 'warning' | 'critical'
  state: 'firing' | 'resolved'
  /** 触发时的值 */
  value: number
  threshold: number
  message: string
  /** 开始满足条件（pending）的时间 */
  started_at: number
  fired_at: number
  resolved_at?: number
  resolved_value?: number
}

/** 本计费周期流量（设计 5.7、5.8、32）。字节数均为整数，GB 换算由客户端按 unit 完成。 */
export interface TrafficView {
  cycle_start: string
  /** 下一周期开始日（不含） */
  cycle_end: string
  rx: number
  tx: number
  /** 统计值：按计费模式取值 × 系数，不含校准 */
  measured: number
  /** 本周期最近一次校准在当前系数与计费模式下的偏差，可为负（设计 5.7） */
  adjustment: number
  /** 最近一次校准时间，Unix 秒；未校准时省略 */
  calibrated_at?: number
  /** 展示值 = 统计值 + 校准偏差 */
  used: number
  /** 字节，0 表示不限 */
  limit: number
  unit: 'decimal' | 'binary'
  factor: number
  /** 周期结束时的预计用量；周期开始不足 3 天时省略（设计 32） */
  forecast?: {
    /** 采用的日均 */
    daily: number
    total: number
    /** 预计超过额度 */
    over: boolean
  }
  /** 多次校准显示稳定的比例偏差时建议设置的统计系数；没有建议时省略（设计 5.7） */
  factor_suggestion?: number
}

/** Agent 上报，定义见 internal/protocol（设计 6.2）；只做向后兼容的新增 */
export interface Report {
  timestamp: number
  agent_version: string
  final?: boolean
  /** 可选：发送时刻的 Agent 时钟（Unix 秒），面板据此计算时钟偏差（设计 43.5） */
  sent_at?: number
  system: {
    hostname: string
    os: string
    os_version: string
    kernel: string
    arch: string
    /** 秒 */
    uptime: number
    boot_id?: string
    /** 可选（设计 4.4） */
    cpu_model?: string
    /** 可选：内核网卡计数器位数；32 位时面板按回绕补算流量（设计 5.5） */
    counter_bits?: 32 | 64
    /** 可选：本机是否启用了远程升级（设计 29.13）；省略表示旧版 Agent、未知 */
    remote_upgrade?: boolean
  }
  cpu: {
    usage: number
    cores: number
    load1: number
    load5?: number
    load15?: number
    /** 可选：两次采样之间各类时间占比 0～100（设计 4.4） */
    breakdown?: {
      user: number
      nice: number
      system: number
      iowait: number
      irq: number
      softirq: number
      steal: number
      idle: number
    }
    /** 可选；GET /servers 列表中省略 */
    per_core?: number[]
    /** 可选，CPU 温度 ℃ */
    temp_c?: number
  }
  memory: {
    total: number
    /** MemTotal − MemAvailable */
    used: number
    available?: number
    usage: number
    /** 可选 */
    free?: number
    /** 可选 */
    buffers?: number
    /** 可选：Cached + SReclaimable */
    cached?: number
  }
  swap: {
    total: number
    used: number
  }
  /** 可选（设计 4.9） */
  processes?: {
    total: number
    running: number
  }
  /** 可选（设计 4.9）：sockstat 的 inuse，IPv4 + IPv6 */
  conns?: {
    tcp: number
    udp: number
    time_wait: number
  }
  /** 可选（设计 4.9.1）：本机监听端口，按协议与端口合并；不含连接明细与进程信息。节点列表接口省略此字段 */
  ports?: ({
    proto: 'tcp' | 'udp'
    port: number
    /** 监听地址，如 0.0.0.0、::、127.0.0.1 */
    addrs: string[]
  })[]
  /** 每个挂载点（设计 4.6）；只采集本地块设备文件系统，同一设备只报一次 */
  disk: {
    mount: string
    total: number
    used: number
    /** 与 df 的 Use% 一致 */
    usage: number
    fstype?: string
    device?: string
    available?: number
  }[]
  /** 每块网卡；rx/tx_bytes 为内核累计字节数，rx/tx_speed 为 Agent 计算的字节/秒 */
  network: {
    interface: string
    ifindex?: number
    rx_bytes: number
    tx_bytes: number
    rx_speed: number
    tx_speed: number
    /** 可选：包速率（设计 4.10） */
    rx_pps?: number
    tx_pps?: number
    /** 可选：开机以来的错误、丢包累计 */
    rx_errors?: number
    tx_errors?: number
    rx_dropped?: number
    tx_dropped?: number
  }[]
  /** 可选，整块磁盘的 IO（设计 4.7） */
  disk_io?: {
    device: string
    read_bytes?: number
    write_bytes?: number
    read_ops?: number
    write_ops?: number
    io_time_ms?: number
    read_speed: number
    write_speed: number
    /** 可选 */
    read_iops?: number
    /** 可选 */
    write_iops?: number
    /** 可选：每次 IO 平均耗时 */
    await_ms?: number
    /** 可选：设备忙碌占比 0～100 */
    util?: number
    /** 可选：采样时正在处理的 IO 请求数（设计 4.10） */
    in_flight?: number
  }[]
  /** 可选：本轮失败的采集项及原因，对应字段留空，其余照常（设计 43.5） */
  collect_errors?: ({
    item: 'system' | 'cpu' | 'memory' | 'disk' | 'disk_io' | 'network' | 'processes' | 'conns' | 'ports' | 'extra'
    message: string
  })[]
  /** 可选：扩展指标（设计 4.10），字段定义见 internal/protocol.Extra。面板保存在实时状态中随接口返回，暂不入库也不展示。 读不到或内核不提供的项省略；速率为与上一次采样之间的平均值。 */
  extra?: {
    /** /proc/stat：ctx_switches、interrupts、forks（每秒）、procs_blocked */
    activity?: Record<string, unknown>
    /** /proc/vmstat：major_faults（每秒）、swap_in / swap_out（字节/秒）、oom_kills（开机以来，内核 4.13+） */
    vm?: Record<string, unknown>
    /** PSI（内核 4.20+）：cpu_some_avg10/60、memory_some_avg10、memory_full_avg10、io_some_avg10、io_full_avg10 */
    pressure?: Record<string, unknown>
    /** /proc/meminfo：shmem、slab_unreclaim、dirty、writeback、committed、commit_limit（字节） */
    memory_more?: Record<string, unknown>
    /** /proc/net/snmp：tcp_established、tcp_retrans_rate（%）、tcp_active_opens / tcp_passive_opens（每秒）、错误累计 */
    net?: Record<string, unknown>
    /** /proc/sys/fs/file-nr：allocated、max */
    file_handles?: Record<string, unknown>
    /** nf_conntrack：count、max；未加载时省略 */
    conntrack?: Record<string, unknown>
    /** adjtimex：synced、max_error_us */
    clock?: Record<string, unknown>
    /** 运行环境：virt（kvm / xen / vmware / hyperv / openvz / lxc / docker / podman / vm / none）、dmi_vendor、dmi_product */
    env?: Record<string, unknown>
  }
}

/** 添加时 provider、name、credential 必填；修改时 provider 不可更改，credential 留空表示不变 */
export interface CloudAccountInput {
  /** aliyun_cn / aliyun_intl：阿里云国内站（aliyun.com）/ 国际站（alibabacloud.com）；tencent_cn / tencent_intl：腾讯云国内站（cloud.tencent.com）/ 国际站（tencentcloud.com）。Oracle Cloud 随后续步骤加入（设计 44.10） */
  provider?: 'aws' | 'aliyun_cn' | 'aliyun_intl' | 'tencent_cn' | 'tencent_intl'
  name: string
  /** 只同步这些区域；空表示全部已启用的区域 */
  regions?: string[]
  /** 只读凭证（设计 44.3 列出最小权限）。只写不读：响应中只有 credential_hint */
  credential?: {
    /** AWS：IAM 用户的 Access Key ID（AKIA…）；阿里云：RAM 用户的 AccessKey ID（LTAI…） */
    access_key_id?: string
    /** AWS */
    secret_access_key?: string
    /** 阿里云 */
    access_key_secret?: string
    /** 腾讯云 CAM 子用户的 SecretId（AKID…） */
    secret_id?: string
    /** 腾讯云 */
    secret_key?: string
  }
  /** 月度预算（账户币种：AWS 为 USD，阿里云国内站 CNY、国际站多为 USD）；0 表示不设 */
  budget?: number
  /** 费用同步间隔，小时；AWS 每次同步约 0.02 美元 */
  cost_interval_h?: 6 | 12 | 24
  enabled?: boolean
  sync_cost?: boolean
  sync_traffic?: boolean
}

export interface CloudAccount {
  id: number
  provider: 'aws' | 'aliyun_cn' | 'aliyun_intl' | 'tencent_cn' | 'tencent_intl'
  name: string
  regions: string[]
  /** 如 AKIA…WXYZ；完整凭证不返回 */
  credential_hint: string
  budget_cents: number
  cost_interval_h: number
  enabled: boolean
  sync_cost: boolean
  sync_traffic: boolean
  /** 上次成功同步费用的时间，Unix 秒；0 表示从未 */
  cost_synced_at: number
  instances_synced_at: number
  traffic_synced_at: number
  /** 最近一次同步失败的原因（云厂商的错误码与说明）；空表示正常 */
  last_error: string
  /** 连续失败的开始时间，Unix 秒；0 表示正常 */
  error_since: number
  /** 凭证失效或权限不足，已停止自动同步，需更新凭证 */
  auth_failed: boolean
  /** 失败后下次自动重试的时间 */
  next_try_at: number
  /** 正在同步 */
  syncing?: boolean
  instance_count: number
  /** 本月费用；尚未同步时为 null */
  current_cost: CloudCost | null
  created_at: number
  updated_at: number
}

export interface CloudCost {
  /** YYYY-MM */
  period: string
  /** 本月已产生 */
  amount_cents: number
  /** 本月预估；数据不足时为 null */
  forecast_cents: number | null
  /** 账户余额（预付费账户）；没有时为 null */
  balance_cents: number | null
  currency: string
  updated_at: number
}

export interface CloudInstance {
  id: number
  account_id: number
  account_name: string
  provider: string
  instance_id: string
  name: string
  region: string
  /** ec2 / lightsail / ecs / swas（阿里云轻量）/ cvm / lighthouse（腾讯云轻量） */
  kind: string
  /** 云厂商原样，如 running / stopped */
  state: string
  public_ipv4: string
  public_ipv6: string
  /** 规格（EC2 实例类型、Lightsail 套餐） */
  plan: string
  /** 到期时间，Unix 秒；按需付费为 0 */
  expire_at: number
  renew_price_cents: number
  /** 流量包额度，字节；0 表示没有流量包 */
  traffic_limit_bytes: number
  /** 本周期已用（云厂商口径，有数小时延迟） */
  traffic_used_bytes: number
  /** YYYY-MM-DD */
  traffic_period_start: string
  /** 关联的节点（设计 44.5，随第五步） */
  server_id: number | null
  updated_at: number
}

/** WebSocket 推送的事件（设计 20）；未来新增类型，页面应忽略不认识的 type */
export interface WsEvent {
  /** server.enrolled：主机已用注册码注册（设计 19.11） */
  type: 'server.enrolled'
  server_id?: number
  /** 事件时间，Unix 秒 */
  ts: number
  /** server.enrolled：{server_name, warnings}；不含任何凭证 */
  data?: {
    server_name?: string
    warnings?: string[]
  }
}

export interface MetricPoint {
  /** 桶起点，Unix 秒 */
  ts: number
  cpu: number
  cpu_max: number
  load1: number
  mem_used: number
  mem_total: number
  swap_used: number
  disk_used: number
  disk_total: number
  rx_speed: number
  rx_speed_max: number
  tx_speed: number
  tx_speed_max: number
  /** 所有磁盘读速率之和，字节/秒；旧版 Agent 时段为 null（设计 4.7） */
  disk_read: number | null
  disk_read_max: number | null
  disk_write: number | null
  disk_write_max: number | null
  /** CPU steal 占比 0～100；旧版 Agent 时段为 null（设计 4.4） */
  steal: number | null
  steal_max: number | null
  /** CPU iowait 占比 0～100；旧版 Agent 时段为 null（设计 4.4） */
  iowait: number | null
  iowait_max: number | null
  /** TCP 连接数（不含 TIME_WAIT）；旧版 Agent 时段为 null（设计 4.9） */
  tcp: number | null
  tcp_max: number | null
}

/** 每个接口的路径参数、查询参数、请求体与成功响应 */
export interface Paths {
  '/auth/captcha': {
    /** 获取登录滑动验证码（设计 17.4） */
    get: {
      response: {
        id: string
        /** data:image/png;base64,…（带缺口） */
        background: string
        /** 拼图块 PNG */
        piece: string
        piece_y: number
        width: number
        height: number
        piece_size: number
      }
    }
  }
  '/auth/login': {
    /** 用户名 + 密码登录，写入会话 Cookie */
    post: {
      body: {
        username: string
        password: string
        /** 为 true 时 Cookie 保存 7 天，否则为会话 Cookie */
        remember?: boolean
        captcha_id?: string
        /** 拼图块左边缘位置，像素 */
        captcha_x?: number
        /** 拖动用时，毫秒 */
        captcha_ms?: number
      }
      response: Me
    }
  }
  '/auth/me': {
    /** 当前账号与 CSRF Token（页面刷新后恢复登录状态） */
    get: {
      response: Me
    }
  }
  '/auth/logout': {
    /** 退出登录（删除当前会话） */
    post: {
      response: void
    }
  }
  '/auth/password': {
    /** 修改密码；成功后该账号其他会话全部失效（设计 17.4） */
    post: {
      body: {
        current_password: string
        new_password: string
      }
      response: void
    }
  }
  '/auth/reauth': {
    /** 敏感操作前重新输入密码，10 分钟内有效（设计 17.4） */
    post: {
      body: {
        password: string
      }
      response: void
    }
  }
  '/auth/sessions': {
    /** 当前账号的登录会话（设计 24.8、17.4），最近活动在前 */
    get: {
      response: {
        /** 空表示没有更多（此列表不分页，恒为空） */
        next_cursor: string
        items: {
          id: number
          created_at: number
          last_seen_at: number
          expires_at: number
          client_ip: string
          user_agent: string
          /** 是否为发出本次请求的会话 */
          current: boolean
        }[]
      }
    }
  }
  '/auth/sessions/{id}': {
    /** 踢出一个会话（该浏览器需要重新登录）；只能操作当前账号的会话，记入审计日志 */
    delete: {
      params: {
        id: number
      }
      response: void
    }
  }
  '/ws': {
    /** 实时事件（WebSocket，设计 20）；面板只推送，不接收指令 */
    get: {
      response: void
    }
  }
  '/agent/enroll': {
    /** 用一次性注册码换取 Agent Token（设计 27.6.2） */
    post: {
      body: EnrollRequest
      response: EnrollResponse
    }
  }
  '/agent/report': {
    /** Agent 定时上报（设计 6） */
    post: {
      body: Report
      response: void
    }
  }
  '/releases/{version}/{file}': {
    /** 面板镜像：下载官方发布文件（设计 27.5.3），无需凭证 */
    get: {
      params: {
        version: string
        file: string
      }
      response: unknown
    }
  }
  '/agent/upgrade': {
    /** Agent 查询本节点的升级任务（设计 29.4、29.13） */
    get: {
      response: {
        upgrade: boolean
        task_id?: number
        version?: string
        /** 签名清单原文（base64） */
        manifest?: string
        /** manifest.json.minisig 原文 */
        manifest_signature?: string
        /** 可选：目标版本已镜像时，Agent 从“自己配置的面板地址 + 此路径”下载构建（设计 27.5.3） */
        mirror_path?: string
      }
    }
  }
  '/agent/upgrade/status': {
    /** Agent 上报升级进度与结果（设计 29.12） */
    post: {
      body: {
        task_id: number
        status: 'staged' | 'success' | 'failed' | 'rolled_back'
        reason?: string
      }
      response: void
    }
  }
  '/agent/unregister': {
    /** Agent 本地卸载时通知面板（设计 27.11） */
    post: {
      response: void
    }
  }
  '/alerts': {
    /** 告警事件，按时间倒序（设计 19.9） */
    get: {
      query?: {
        /** active 为正在告警（firing） */
        state?: 'active' | 'resolved' | 'all'
        server_id?: number
        cursor?: string
        limit?: number
      }
      response: {
        next_cursor: string
        items: AlertEvent[]
      }
    }
  }
  '/alert-rules': {
    /** 全部告警规则（全局 / 分组 / 节点三层，设计 16.2） */
    get: {
      response: {
        /** 空表示没有更多（此列表不分页，恒为空） */
        next_cursor: string
        items: AlertRule[]
      }
    }
    /** 为分组或节点新增覆盖：以同 rule_key 的全局规则为基础，应用请求中的字段 */
    post: {
      body: {
        rule_key: string
        scope_type: 'group' | 'server'
        /** 分组名或节点 ID */
        scope_id: string
        threshold?: number
        /** 不高于触发阈值 */
        recover_threshold?: number
        duration_s?: number
        recover_duration_s?: number
        severity?: 'info' | 'warning' | 'critical'
        repeat_interval_s?: number
        enabled?: boolean
      }
      response: AlertRule
    }
  }
  '/alert-rules/{id}': {
    /** 修改规则；省略的字段保持不变；类型、rule_key、层级不可改 */
    put: {
      params: {
        id: number
      }
      body: {
        threshold?: number
        /** 不高于触发阈值 */
        recover_threshold?: number
        duration_s?: number
        recover_duration_s?: number
        severity?: 'info' | 'warning' | 'critical'
        repeat_interval_s?: number
        enabled?: boolean
      }
      response: AlertRule
    }
    /** 删除分组 / 节点覆盖（回到上一层的设置）；默认规则不能删除（422），可以关闭 */
    delete: {
      params: {
        id: number
      }
      response: void
    }
  }
  '/alert-rules/preview': {
    /** 预览“按当前数据，此规则会对几台节点触发”（设计 16.2）；只比较阈值，不含持续时间 */
    post: {
      body: Record<string, unknown>
      response: {
        matching: number
        /** 由此规则决定的节点数 */
        total: number
        items: {
          server_id: number
          name: string
          value: number
          detail?: string
        }[]
      }
    }
  }
  '/agent-releases': {
    /** 已同步并验签的官方 Agent 版本，最新在前（设计 29.1、29.20） */
    get: {
      response: {
        /** 空表示没有更多 */
        next_cursor: string
        items: AgentRelease[]
        auto_sync: boolean
        source: string
        /** 是否开启 --release-mirror */
        mirror: boolean
      }
    }
  }
  '/agent-releases/sync': {
    /** 立即从官方发布地址同步最新版本并验签；验签失败的版本不会被记录 */
    post: {
      response: {
        version: string
        channel: string
      }
    }
  }
  '/upgrade-tasks': {
    /** 远程升级任务，最新在前（最多 100 个，设计 29.14） */
    get: {
      query?: {
        server_id?: number
      }
      response: {
        /** 空表示没有更多 */
        next_cursor: string
        items: UpgradeTask[]
      }
    }
    /** 为节点创建升级任务（设计 29.14） */
    post: {
      body: {
        server_ids: number[]
        version: string
      }
      response: {
        created: UpgradeTask[]
        skipped: {
          server_id: number
          reason: string
        }[]
      }
    }
  }
  '/upgrade-tasks/{id}/cancel': {
    /** 取消尚未交给 updater 的升级任务（pending / delivered） */
    post: {
      params: {
        id: number
      }
      response: void
    }
  }
  '/settings/quiet-hours': {
    /** 免打扰时段（设计 16.5） */
    get: {
      response: QuietHoursView
    }
    /** 修改免打扰时段（记入操作日志 setting.update） */
    put: {
      body: QuietHours
      response: QuietHoursView
    }
  }
  '/notification-channels': {
    /** 通知渠道（设计 16.5、31）；凭证已脱敏 */
    get: {
      response: {
        /** 空表示没有更多 */
        next_cursor: string
        items: NotifyChannel[]
      }
    }
    /** 添加通知渠道（记入操作日志） */
    post: {
      body: NotifyChannelInput
      response: NotifyChannel
    }
  }
  '/notification-channels/{id}': {
    /** 修改通知渠道；类型不可修改，凭证字段留空保持原值 */
    put: {
      params: {
        id: number
      }
      body: NotifyChannelInput
      response: NotifyChannel
    }
    /** 删除通知渠道（投递记录保留） */
    delete: {
      params: {
        id: number
      }
      response: void
    }
  }
  '/notification-channels/{id}/test': {
    /** 发送测试通知（同步发送一次，不重试；停用的渠道也可测试） */
    post: {
      params: {
        id: number
      }
      response: {
        ok: boolean
        error?: string
      }
    }
  }
  '/notification-deliveries': {
    /** 通知投递记录，最新在前（保留 30 天，设计 18.15） */
    get: {
      query?: {
        event_id?: number
        limit?: number
      }
      response: {
        /** 空表示没有更多 */
        next_cursor: string
        items: Delivery[]
      }
    }
  }
  '/silences': {
    /** 生效中的静音与维护（设计 16.6），最新在前 */
    get: {
      response: {
        /** 空表示没有更多 */
        next_cursor: string
        items: Silence[]
      }
    }
    /** 新增静音或维护；同一对象同一类型已有生效记录时以新设置为准 */
    post: {
      body: {
        /** 维护只能针对单个节点 */
        kind: 'mute' | 'maintenance'
        scope_type: 'server' | 'group' | 'rule' | 'global'
        /** 节点 ID、分组名或 rule_key；global 时省略 */
        scope_id?: string
        /** 空表示直到手动结束 */
        duration?: '' | '1h' | '8h' | '24h'
        reason?: string
      }
      response: Silence
    }
  }
  '/silences/{id}': {
    /** 立即结束静音或维护（保留记录） */
    delete: {
      params: {
        id: number
      }
      response: void
    }
  }
  '/audit-logs': {
    /** 审计日志：登录日志与操作日志（设计 24.8），按时间倒序；只读，不提供修改或删除 */
    get: {
      query?: {
        /** login：登录、退出、二次验证；operation：其他操作；省略为全部 */
        category?: 'login' | 'operation'
        result?: 'success' | 'failure'
        /** 主体类型 */
        actor?: 'admin' | 'agent' | 'cli' | 'system'
        /** 操作：精确匹配；以 “.” 结尾时按前缀匹配（如 server.） */
        action?: string
        /** 起始时间（含），Unix 秒 */
        from?: number
        /** 结束时间（不含），Unix 秒 */
        to?: number
        /** 上一页返回的 next_cursor */
        cursor?: string
        limit?: number
      }
      response: {
        /** 空表示没有更多 */
        next_cursor: string
        items: ({
          id: number
          ts: number
          actor_type: 'admin' | 'agent' | 'cli' | 'system'
          /** 管理员用户名（登录失败时为填写的用户名）或节点 ID */
          actor_id: string
          action: string
          target_type: string
          target_id: string
          /** 对象为节点且仍存在时的名称 */
          target_name: string
          result: 'success' | 'failure'
          client_ip: string
          user_agent: string
          /** 已脱敏（设计 24.7） */
          details: Record<string, unknown>
        })[]
      }
    }
  }
  '/audit-logs/export': {
    /** 导出审计日志为 CSV（设计 24.8），筛选参数同 /audit-logs，最多 10000 条，按时间倒序 */
    get: {
      query?: {
        category?: 'login' | 'operation'
        result?: 'success' | 'failure'
        actor?: 'admin' | 'agent' | 'cli' | 'system'
        action?: string
        from?: number
        to?: number
      }
      response: unknown
    }
  }
  '/cloud-accounts': {
    /** 云账户列表（不含凭证，只有末 4 位提示） */
    get: {
      response: {
        /** 空表示没有更多 */
        next_cursor: string
        items: CloudAccount[]
      }
    }
    /** 添加云账户（需在 10 分钟内重新验证过密码，设计 17.4、44.2）；添加后立即在后台同步一次 */
    post: {
      body: CloudAccountInput
      response: CloudAccount
    }
  }
  '/cloud-accounts/{id}': {
    /** 修改云账户；credential 留空表示不变，提供时需重新验证密码，并清除“凭证失效”状态 */
    put: {
      params: {
        id: number
      }
      body: CloudAccountInput
      response: CloudAccount
    }
    /** 删除云账户及同步的费用与实例（需重新验证密码） */
    delete: {
      params: {
        id: number
      }
      response: void
    }
  }
  '/cloud-accounts/{id}/sync': {
    /** 立即在后台同步一次全部数据（同一账户 1 分钟内只允许一次）；结果见账户的 *_synced_at 与 last_error */
    post: {
      params: {
        id: number
      }
      response: void
    }
  }
  '/cloud-accounts/{id}/costs': {
    /** 按月费用，最新在前（最多 24 个月） */
    get: {
      params: {
        id: number
      }
      response: {
        /** 空表示没有更多 */
        next_cursor: string
        items: CloudCost[]
      }
    }
  }
  '/cloud-instances': {
    /** 云厂商的实例列表 */
    get: {
      query?: {
        /** 只看某个账户 */
        account_id?: number
      }
      response: {
        /** 空表示没有更多 */
        next_cursor: string
        items: CloudInstance[]
      }
    }
  }
  '/servers': {
    /** 节点列表（实时状态取自内存） */
    get: {
      response: {
        items: ServerView[]
        /** 空表示没有更多 */
        next_cursor: string
      }
    }
    /** 新建节点（状态：待安装），同时生成注册码（设计 19.11、27.2） */
    post: {
      body: CreateServerRequest
      response: EnrollCodeView
    }
  }
  '/servers/{id}': {
    /** 单个节点（格式同列表中的一项） */
    get: {
      params: {
        id: number
      }
      response: ServerView
    }
    /** 修改节点信息（设计 19.5） */
    put: {
      params: {
        id: number
      }
      body: CreateServerRequest
      response: ServerView
    }
    /** 删除节点及其全部历史数据，不可恢复 */
    delete: {
      params: {
        id: number
      }
      response: void
    }
  }
  '/servers/{id}/install-command': {
    /** 查看安装命令与注册码状态 */
    get: {
      params: {
        id: number
      }
      response: EnrollCodeView
    }
  }
  '/servers/{id}/revoke-agent-token': {
    /** 立即吊销节点的全部 Agent Token（设计 17.2、23.2） */
    post: {
      params: {
        id: number
      }
      response: void
    }
  }
  '/servers/{id}/enroll-code': {
    /** 重新生成注册码，旧码立即失效（设计 27.4）；对已注册节点即重新安装 / 更换主机（设计 27.8） */
    post: {
      params: {
        id: number
      }
      body?: {
        enroll_ttl?: EnrollTTL
      }
      response: EnrollCodeView
    }
    /** 撤销注册码 */
    delete: {
      params: {
        id: number
      }
      response: void
    }
  }
  '/servers/{id}/metrics/history': {
    /** 节点历史指标（设计 19.7） */
    get: {
      params: {
        id: number
      }
      query?: {
        range?: '1h' | '6h' | '24h' | '7d' | '30d'
      }
      response: {
        /** 按时间范围一次返回，恒为空（设计 19.0.2） */
        next_cursor: string
        range: '1h' | '6h' | '24h' | '7d' | '30d'
        /** 点的间隔，秒 */
        resolution: number
        items: MetricPoint[]
      }
    }
  }
  '/servers/{id}/traffic/current': {
    /** 本计费周期流量、校准与预测（设计 19.8、5.7、32）；与节点中的 traffic 字段相同 */
    get: {
      params: {
        id: number
      }
      response: TrafficView
    }
  }
  '/servers/{id}/traffic/daily': {
    /** 最近 N 天每日流量，按日期升序，无数据的日期补 0 */
    get: {
      params: {
        id: number
      }
      query?: {
        days?: number
      }
      response: {
        /** 空表示没有更多（此列表不分页，恒为空） */
        next_cursor: string
        items: {
          day: string
          rx: number
          tx: number
          /** 按计费模式取值并乘以系数；不含校准 */
          used: number
        }[]
      }
    }
  }
  '/servers/{id}/traffic/monthly': {
    /** 最近 N 个计费周期（含当前），最新在前；历史周期按当前的计费模式、系数与额度计算 */
    get: {
      params: {
        id: number
      }
      query?: {
        cycles?: number
      }
      response: {
        /** 空表示没有更多（此列表不分页，恒为空） */
        next_cursor: string
        items: {
          cycle_start: string
          /** 下一周期开始日（不含） */
          cycle_end: string
          rx: number
          tx: number
          adjustment: number
          used: number
          limit: number
        }[]
      }
    }
  }
  '/servers/{id}/traffic/calibrate': {
    /** 手动校准本周期已用流量（设计 5.7） */
    post: {
      params: {
        id: number
      }
      body: {
        /** 服务商面板显示的已用量，按节点的 traffic_unit 口径 */
        used_gb: number
        note?: string
      }
      response: TrafficView
    }
  }
  '/servers/{id}/traffic/adjustments': {
    /** 校准历史（最近 50 条，最新在前） */
    get: {
      params: {
        id: number
      }
      response: {
        /** 空表示没有更多（此列表不分页，恒为空） */
        next_cursor: string
        items: {
          id: number
          cycle_start: string
          /** 校准时的统计值（已乘系数） */
          measured_bytes: number
          /** 用户填写值 */
          reported_bytes: number
          adjustment_bytes: number
          /** 校准时本周期的原始接收字节（未乘系数）；旧记录省略 */
          raw_rx?: number
          /** 校准时本周期的原始发送字节（未乘系数）；旧记录省略 */
          raw_tx?: number
          note: string
          created_at: number
        }[]
      }
    }
  }
}
