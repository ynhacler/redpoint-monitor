// 轻量接口客户端，类型与 internal/protocol、internal/server 的 JSON 保持一致。
// TODO(B): 类型改为由 api/openapi.yaml 生成，不再手写（设计 19.0.1）。

/** 一块网卡的数据：rx/tx_bytes 为内核累计字节数，rx/tx_speed 为 Agent 计算的字节/秒。 */
export interface NetIface {
  interface: string
  rx_bytes: number
  tx_bytes: number
  rx_speed: number
  tx_speed: number
}

/** 一个挂载点的容量（设计 4.6）。字节；usage 为 0～100，口径与 df 的 Use% 一致。 */
export interface DiskInfo {
  mount: string
  total: number
  used: number
  usage: number
  fstype?: string
  device?: string
  available?: number
}

/** 一份 Agent 上报（internal/protocol.Report）。字节类字段为原始字节数，usage 为 0～100 的百分比。 */
export interface Report {
  timestamp: number
  agent_version: string
  /** uptime 为秒；cpu_model 为可选（旧版 Agent 不带） */
  system: { hostname: string; os: string; os_version: string; kernel: string; arch: string; uptime: number; cpu_model?: string }
  cpu: {
    usage: number
    cores: number
    load1: number
    load5?: number
    load15?: number
    /** 各类 CPU 时间占比（设计 4.4）；以下均为可选，旧版 Agent 不带 */
    breakdown?: CPUBreakdown
    /** 每核使用率；节点列表接口省略，只在详情中返回 */
    per_core?: number[]
    /** CPU 温度 ℃；读不到时没有 */
    temp_c?: number
  }
  /** free / buffers / cached 为可选：buffers 与 cached 可回收，不计入 used（设计 4.5） */
  memory: { total: number; used: number; usage: number; available?: number; free?: number; buffers?: number; cached?: number }
  swap: { total: number; used: number }
  /** 每个挂载点的容量（设计 4.6） */
  disk: DiskInfo[]
  network: NetIface[]
  /** 各磁盘的 IO（设计 4.7）；旧版 Agent 不上报 */
  disk_io?: DiskIO[]
  /** 进程数、套接字数（设计 4.9）；旧版 Agent 不上报 */
  processes?: { total: number; running: number }
  conns?: { tcp: number; udp: number; time_wait: number }
}

/** 两次采样之间各类 CPU 时间的占比，0～100 */
export interface CPUBreakdown {
  user: number
  nice: number
  system: number
  iowait: number
  irq: number
  softirq: number
  steal: number
  idle: number
}

/** 一块磁盘的 IO：累计字节与本轮速率；iops / await / util 为可选（设计 4.7） */
export interface DiskIO {
  device: string
  read_bytes?: number
  write_bytes?: number
  read_speed: number
  write_speed: number
  read_iops?: number
  write_iops?: number
  /** 平均每次 IO 耗时，毫秒 */
  await_ms?: number
  /** 设备忙碌占比 0～100 */
  util?: number
}

/**
 * GET /api/v1/servers 的一行。
 * status 由面板根据 last_seen_at 判定（≤30 秒在线，≤120 秒未知，否则离线；设计 22），
 * 保证 Web 与 App 的口径一致。
 */
export interface ServerView {
  id: number
  name: string
  /** pending 为待安装：已新建、尚未注册（设计 27.7） */
  status: 'online' | 'unknown' | 'offline' | 'pending'
  enroll_state: 'pending' | 'enrolled'
  expected_hostname: string
  expected_ipv4: string
  expected_ipv6: string
  verify_mode: 'warn' | 'strict'
  /** 注册时间，Unix 秒；0 表示未注册 */
  enrolled_at: number
  created_at: number
  /** 注册时的实际值（设计 18.2） */
  hostname: string
  ipv4: string
  ipv6: string
  group: string
  note: string
  provider: string
  plan: string
  region: string
  /** 国家 / 地区，ISO 3166-1 两位代码（大写）；空表示未填 */
  country: string
  /** 服务商标称带宽（端口速率），Mbps；0 表示未填。Agent 采集不到，只能手动填写 */
  bandwidth_mbps: number
  /** 续费价格 × 100 */
  price_cents: number
  currency: string
  billing_period: string
  /** YYYY-MM-DD，空表示未填 */
  expire_date: string
  traffic_limit_bytes: number
  traffic_reset_day: number
  traffic_count_mode: 'sum' | 'rx' | 'tx' | 'max'
  /** 计量单位口径（设计 5.8） */
  traffic_unit: TrafficUnit
  /** 统计系数，默认 1（设计 5.7） */
  traffic_factor: number
  /** 最后一次上报时间，Unix 秒；0 表示从未上报 */
  last_seen_at: number
  /** 收到首次上报之前不存在 */
  latest?: Report
  /** 活动告警（由面板的告警引擎判定，严重在前；节点离线时只有离线告警，设计 16） */
  alerts: AlertBrief[]
  traffic: TrafficView
}

export type AlertSeverity = 'info' | 'warning' | 'critical'

/** 节点当前的一条活动告警 */
export interface AlertBrief {
  event_id: number
  rule_key: string
  type: string
  severity: AlertSeverity
  message: string
  /** 当前值：百分比、离线秒数或负载倍数 */
  value: number
  fired_at: number
}

/** 一条告警事件（设计 18.8） */
export interface AlertEvent {
  id: number
  rule_key: string
  server_id: number
  server_name: string
  type: string
  severity: AlertSeverity
  state: 'firing' | 'resolved'
  value: number
  threshold: number
  message: string
  started_at: number
  fired_at: number
  resolved_at?: number
  resolved_value?: number
}

/** decimal：1 GB = 10⁹ 字节；binary：1 GiB = 2³⁰ 字节（设计 5.8） */
export type TrafficUnit = 'decimal' | 'binary'

/** 本计费周期流量（设计 5.7、32）。字节数均为整数。 */
export interface TrafficView {
  /** YYYY-MM-DD */
  cycle_start: string
  /** 下一周期开始日（不含） */
  cycle_end: string
  rx: number
  tx: number
  /** 统计值：按计费模式取值 × 系数，不含校准 */
  measured: number
  /** 本周期最近一次校准的偏差，可为负 */
  adjustment: number
  /** 最近一次校准时间，Unix 秒；未校准时没有 */
  calibrated_at?: number
  /** 展示值 = 统计值 + 校准偏差 */
  used: number
  /** 字节，0 表示不限 */
  limit: number
  unit: TrafficUnit
  factor: number
  /** 周期开始不足 3 天时没有（设计 32） */
  forecast?: { daily: number; total: number; over: boolean }
}

/** 一天的流量；used 按计费模式取值 × 系数，不含校准 */
export interface TrafficDay {
  day: string
  rx: number
  tx: number
  used: number
}

// 【安全】登录状态由 HttpOnly 会话 Cookie 维持，页面脚本读不到它（设计 17.4）。
// 修改类请求需要的 CSRF 值从登录 / /auth/me 响应中取得，只保存在内存里。
let csrfToken = ''

/** 设置 CSRF 值（登录、刷新登录状态后调用） */
export function setCsrf(t: string) {
  csrfToken = t
}

/** 表单字段级错误（设计 43.4 details）。 */
export interface FieldError {
  field: string
  message: string
}

/**
 * 面板返回的统一错误（设计 43.4）。message 是中文提示，可直接展示；
 * requestId 供用户复制，对照服务端日志（设计 43.6）。
 */
export class ApiError extends Error {
  constructor(
    /** HTTP 状态码；网络错误时为 0 */
    public status: number,
    /** 稳定的错误码，如 validation_failed */
    public code: string,
    message: string,
    public requestId = '',
    public details: FieldError[] = [],
  ) {
    super(message)
  }
}

/** 收到 401 时抛出，让界面区分“未登录或会话失效”（回到登录页）与“面板不可达”（继续轮询并提示）。 */
export class UnauthorizedError extends ApiError {}

/**
 * 统一请求封装（设计 43.6）：同源请求自动带上会话 Cookie；修改类请求附带 X-CSRF-Token；
 * 把错误响应转换为 ApiError / UnauthorizedError。
 * 使用相对路径：开发时由 Vite 代理到 :8080；生产环境页面由面板内嵌提供，始终同源。
 */
async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch('/api/v1' + path, {
      method,
      headers: {
        ...(method === 'GET' ? {} : { 'X-CSRF-Token': csrfToken }),
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
      },
      credentials: 'same-origin',
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError(0, 'network', '无法连接面板，请检查网络')
  }
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => null)
  if (res.ok) return data as T
  const e = data?.error ?? {}
  const msg: string = e.message || `请求失败（HTTP ${res.status}）`
  const args = [res.status, e.code ?? 'unknown', msg, e.request_id ?? '', e.details ?? []] as const
  if (res.status === 401) throw new UnauthorizedError(...args)
  throw new ApiError(...args)
}

/**
 * 获取节点列表。
 * @throws UnauthorizedError Token 无效（401）
 */
export function listServers(): Promise<ServerView[]> {
  return request('GET', '/servers')
}

/** 新建节点的表单（设计 27.2）；空字符串字段表示未填写。 */
export interface CreateServerInput {
  name: string
  expected_hostname?: string
  expected_ipv4?: string
  expected_ipv6?: string
  group?: string
  note?: string
  provider?: string
  plan?: string
  region?: string
  /** ISO 3166-1 两位代码 */
  country?: string
  /** 标称带宽，Mbps */
  bandwidth_mbps?: number
  /** 月流量额度，按 traffic_unit 口径的 GB / GiB（设计 5.8）；0 或不填表示不限 */
  traffic_limit_gb?: number
  traffic_unit?: TrafficUnit
  /** 统计系数 0.5～2 */
  traffic_factor?: number
  /** 流量重置日 1～31 */
  traffic_reset_day?: number
  traffic_count_mode?: 'sum' | 'rx' | 'tx' | 'max'
  /** 续费价格 */
  price?: number
  /** ISO 4217 币种代码，如 USD */
  currency?: string
  billing_period?: string
  /** 到期日 YYYY-MM-DD */
  expire_date?: string
  enroll_ttl?: '1h' | '24h' | '7d'
  verify_mode?: 'warn' | 'strict'
}

/** 安装命令（设计 27.3）。没有已验签的官方版本时 mode 为 manual（设计 27.3.1）。 */
export interface InstallCommand {
  mode: 'default' | 'manual'
  command: string
  server: string
  release: unknown | null
}

/** 注册码与安装命令。enroll_code 只在新建与重新生成时返回一次（设计 19.11）。 */
export interface EnrollCodeView {
  server_id: number
  server_name: string
  enroll_state: 'pending' | 'enrolled'
  enroll_code?: string
  enroll_code_hint: string
  enroll_status: 'ACTIVE' | 'USED' | 'REVOKED' | 'EXPIRED' | 'NONE'
  /** Unix 秒 */
  enroll_expires_at: number
  install: InstallCommand
}

/** 新建节点（状态：待安装），返回注册码与安装命令。 */
export function createServer(input: CreateServerInput): Promise<EnrollCodeView> {
  return request('POST', '/servers', input)
}

/** 获取单个节点。 */
export function getServer(id: number): Promise<ServerView> {
  return request('GET', `/servers/${id}`)
}

/** 修改节点信息（整体替换，未提供的可选字段视为清空），返回最新节点。 */
export function updateServer(id: number, input: CreateServerInput): Promise<ServerView> {
  return request('PUT', `/servers/${id}`, input)
}

/** 删除节点及其全部历史数据，不可恢复。 */
export function deleteServer(id: number): Promise<void> {
  return request('DELETE', `/servers/${id}`)
}

/** 历史曲线上的一个点（设计 19.7）。平均值与 *_max；磁盘 IO 在旧版 Agent 时段为 null。 */
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
  disk_read: number | null
  disk_read_max: number | null
  disk_write: number | null
  disk_write_max: number | null
}

/** 可选的历史时间范围（设计 19.7、41.5） */
export type HistoryRange = '1h' | '6h' | '24h' | '7d' | '30d'

/** 历史指标：粒度随范围变化（10 秒～1 小时，设计 21） */
export interface HistoryView {
  range: HistoryRange
  /** 点的间隔，秒 */
  resolution: number
  items: MetricPoint[]
}

/** 获取节点历史指标。 */
export function getHistory(id: number, range: HistoryRange): Promise<HistoryView> {
  return request('GET', `/servers/${id}/metrics/history?range=${range}`)
}

/** 最近 days 天的每日流量，按日期升序，无数据的日期为 0（设计 19.8） */
export async function getTrafficDaily(id: number, days = 30): Promise<TrafficDay[]> {
  return (await request<{ items: TrafficDay[] }>('GET', `/servers/${id}/traffic/daily?days=${days}`)).items
}

/** 手动校准本周期已用流量（设计 5.7）：usedGB 按节点的单位口径，返回校准后的本周期流量 */
export function calibrateTraffic(id: number, usedGB: number, note = ''): Promise<TrafficView> {
  return request('POST', `/servers/${id}/traffic/calibrate`, { used_gb: usedGB, note })
}

/** 告警事件，按时间倒序（设计 19.9）；state 默认 active（正在告警） */
export function listAlerts(q: { state?: 'active' | 'resolved' | 'all'; server_id?: number; cursor?: string; limit?: number }): Promise<{ items: AlertEvent[]; next_cursor: string }> {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) if (v !== undefined && v !== '') qs.set(k, String(v))
  return request('GET', `/alerts?${qs}`)
}

/** 告警规则（设计 16.2）：全局 / 分组 / 节点三层，下层按 rule_key 覆盖上层 */
export interface AlertRule {
  id: number
  rule_key: string
  scope_type: 'global' | 'group' | 'server'
  /** 分组名或节点 ID；全局为空 */
  scope_id: string
  type: string
  operator: '>' | '>='
  threshold: number
  /** 低于此值恢复（回差） */
  recover_threshold: number
  duration_s: number
  recover_duration_s: number
  severity: AlertSeverity
  repeat_interval_s: number
  enabled: boolean
}

/** 规则中可修改的字段 */
export type AlertRuleInput = Partial<Pick<AlertRule, 'threshold' | 'recover_threshold' | 'duration_s' | 'recover_duration_s' | 'severity' | 'repeat_interval_s' | 'enabled'>>

export async function listAlertRules(): Promise<AlertRule[]> {
  return (await request<{ items: AlertRule[] }>('GET', '/alert-rules')).items
}

export function updateAlertRule(id: number, v: AlertRuleInput): Promise<AlertRule> {
  return request('PUT', `/alert-rules/${id}`, v)
}

/** 为分组或节点新增覆盖，以同 rule_key 的全局规则为基础 */
export function createAlertRule(v: AlertRuleInput & { rule_key: string; scope_type: 'group' | 'server'; scope_id: string }): Promise<AlertRule> {
  return request('POST', '/alert-rules', v)
}

export function deleteAlertRule(id: number): Promise<void> {
  return request('DELETE', `/alert-rules/${id}`)
}

/** 预览：按当前数据，此规则会对几台节点触发（只比较阈值，不考虑持续时间） */
export function previewAlertRule(v: AlertRuleInput & { id?: number; rule_key?: string; scope_type?: string; scope_id?: string }): Promise<{ matching: number; total: number; items: { server_id: number; name: string; value: number; detail?: string }[] }> {
  return request('POST', '/alert-rules/preview', v)
}

/** 一条审计记录（设计 24.8）；details 已脱敏 */
export interface AuditLog {
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
  details: Record<string, unknown>
}

export interface AuditQuery {
  category?: 'login' | 'operation'
  result?: 'success' | 'failure'
  cursor?: string
  limit?: number
}

/** 审计日志，按时间倒序；next_cursor 为空表示没有更多 */
export function listAuditLogs(q: AuditQuery): Promise<{ items: AuditLog[]; next_cursor: string }> {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) if (v !== undefined && v !== '') qs.set(k, String(v))
  return request('GET', `/audit-logs?${qs}`)
}

/** 查看安装命令；不含完整注册码。 */
export function getInstallCommand(id: number): Promise<EnrollCodeView> {
  return request('GET', `/servers/${id}/install-command`)
}

/** 重新生成注册码，旧码立即失效（设计 27.4）。 */
export function regenerateEnrollCode(id: number, ttl: '1h' | '24h' | '7d' = '24h'): Promise<EnrollCodeView> {
  return request('POST', `/servers/${id}/enroll-code`, { enroll_ttl: ttl })
}

/** 撤销注册码。 */
export function revokeEnrollCode(id: number): Promise<void> {
  return request('DELETE', `/servers/${id}/enroll-code`)
}


/** 当前登录信息（设计 19.1） */
export interface Me {
  username: string
  /** 使用初始 / 重置密码登录，必须先修改密码（设计 17.4） */
  must_change_password: boolean
  csrf_token: string
}

/** 登录滑动验证码（设计 17.4）：背景（带缺口）与拼图块图片，正确位置只在服务端 */
export interface Captcha {
  id: string
  background: string
  piece: string
  piece_y: number
  width: number
  height: number
  piece_size: number
}

/** 滑块结果：拼图块左边缘位置（图片像素）与拖动用时 */
export interface CaptchaAnswer {
  id: string
  x: number
  ms: number
}

/** 获取一次登录验证码；每次登录尝试后都需重新获取（一次性）。 */
export function getCaptcha(): Promise<Captcha> {
  return request('GET', '/auth/captcha')
}

/** 登录。失败时抛出 ApiError（401 用户名或密码错误、400 验证码未通过、429 失败过多）。 */
export async function login(username: string, password: string, remember: boolean, captcha?: CaptchaAnswer): Promise<Me> {
  const me = await request<Me>('POST', '/auth/login', {
    username, password, remember,
    captcha_id: captcha?.id ?? '', captcha_x: captcha?.x ?? 0, captcha_ms: captcha?.ms ?? 0,
  })
  setCsrf(me.csrf_token)
  return me
}

/** 读取当前登录状态；未登录时抛出 UnauthorizedError。 */
export async function getMe(): Promise<Me> {
  const me = await request<Me>('GET', '/auth/me')
  setCsrf(me.csrf_token)
  return me
}

/** 退出登录。 */
export function logoutSession(): Promise<void> {
  return request('POST', '/auth/logout')
}

/** 修改密码；成功后其他会话全部失效（设计 17.4）。 */
export function changePassword(current: string, next: string): Promise<void> {
  return request('POST', '/auth/password', { current_password: current, new_password: next })
}

/** 敏感操作前重新输入密码，10 分钟内有效（设计 17.4）。 */
export function reauth(password: string): Promise<void> {
  return request('POST', '/auth/reauth', { password })
}
