// 接口客户端（设计 19.0.1、43.6）。类型由 api/openapi.yaml 生成（api.gen.ts，make api-types），不手写：
// 每个函数通过 request(方法, 契约中的路径, 参数) 调用，路径参数、查询参数、请求体与响应都按契约检查。
import type { Paths } from './api.gen'
import type * as G from './api.gen'

export type {
  AgentRelease, AlertEvent, HealthSummary, SSLMonitor, APIKey, APIKeyInput, AppAccessKey, AppAccessKeyInput, AppDevice, CloudAccount, CloudAccountInput, CloudCost, CloudInstance, AlertRule, Delivery, EnrollCodeView, InstallCommand, Me, MetricPoint, NotifyChannel,
  NotifyChannelInput, QuietHours, QuietHoursView, Report, ServerView, Silence, TrafficView, UpgradeRollout, UpgradeTask,
} from './api.gen'

// ---- 由契约派生的类型 ----

type Method = 'get' | 'post' | 'put' | 'patch' | 'delete'
type Op<P extends keyof Paths, M extends keyof Paths[P]> = Paths[P][M]
/** 某个接口的成功响应 */
export type Res<P extends keyof Paths, M extends keyof Paths[P]> = Op<P, M> extends { response: infer R } ? R : never
/** 某个接口的请求体 */
export type Body<P extends keyof Paths, M extends keyof Paths[P]> = Op<P, M> extends { body?: infer B } ? B : never
/** 某个接口的查询参数 */
export type Query<P extends keyof Paths, M extends keyof Paths[P]> = Op<P, M> extends { query?: infer Q } ? NonNullable<Q> : never

/** 一份上报中的各部分（设计 4） */
export type NetIface = G.Report['network'][number]
export type DiskInfo = G.Report['disk'][number]
export type DiskIO = NonNullable<G.Report['disk_io']>[number]
export type ListenPort = NonNullable<G.Report['ports']>[number]
export type CPUBreakdown = NonNullable<G.Report['cpu']['breakdown']>

/** 节点当前的一条活动告警 */
export type AlertBrief = G.ServerView['alerts'][number]
export type AlertSeverity = G.AlertRule['severity']
/** decimal：1 GB = 10⁹ 字节；binary：1 GiB = 2³⁰ 字节（设计 5.8） */
export type TrafficUnit = G.TrafficView['unit']
/** 新建 / 修改节点的表单（设计 27.2）；空字符串字段表示未填写 */
export type CreateServerInput = G.CreateServerRequest
export type UpgradeStatus = G.UpgradeTask['status']
export type ChannelType = G.NotifyChannel['type']

/** 一天的流量；used 按计费模式取值 × 系数，不含校准 */
export type TrafficDay = Res<'/servers/{id}/traffic/daily', 'get'>['items'][number]
/** 一个计费周期的流量（设计 19.8）；used 含系数与该周期的校准 */
export type TrafficCycle = Res<'/servers/{id}/traffic/monthly', 'get'>['items'][number]
/** 一条手动校准记录（设计 5.7、18.12） */
export type TrafficAdjustment = Res<'/servers/{id}/traffic/adjustments', 'get'>['items'][number]
/** 历史指标：粒度随范围变化（10 秒～1 小时，设计 21） */
export type HistoryView = Res<'/servers/{id}/metrics/history', 'get'>
export type HistoryRange = HistoryView['range']
/** 一条审计记录（设计 24.8）；details 已脱敏 */
export type AuditLog = Res<'/audit-logs', 'get'>['items'][number]
export type AuditQuery = Query<'/audit-logs', 'get'>
/** 一个登录会话（设计 24.8） */
export type LoginSession = Res<'/auth/sessions', 'get'>['items'][number]
/** 登录滑动验证码（设计 17.4）：背景（带缺口）与拼图块图片，正确位置只在服务端 */
export type Captcha = Res<'/auth/captcha', 'get'>
/** 规则中可修改的字段 */
export type AlertRuleInput = Body<'/alert-rules/{id}', 'put'>
export type AlertPreview = Res<'/alert-rules/preview', 'post'>

/** 滑块结果：拼图块左边缘位置（图片像素）与拖动用时 */
export interface CaptchaAnswer {
  id: string
  x: number
  ms: number
}

// ---- 请求与错误（设计 43.4、43.6） ----

// 【安全】登录状态由 HttpOnly 会话 Cookie 维持，页面脚本读不到它（设计 17.4）。
// 修改类请求需要的 CSRF 值从登录 / /auth/me 响应中取得，只保存在内存里。
let csrfToken = ''

/** 设置 CSRF 值（登录、刷新登录状态后调用） */
export function setCsrf(t: string) {
  csrfToken = t
}

/** 表单字段级错误（设计 43.4 details）。 */
export type FieldError = NonNullable<G.ErrorBody['error']['details']>[number]

/**
 * 面板返回的统一错误（设计 43.4）。界面展示用 errorText(e)（设计 43.6、43.9），不直接展示 message 以外的内容；
 * requestId 供用户复制，对照服务端日志。
 */
export class ApiError extends Error {
  constructor(
    /** HTTP 状态码；网络错误时为 0 */
    public status: number,
    /** 稳定的错误码（G.ErrorCode）；网络错误为 network，无法解析的响应为 unknown */
    public code: G.ErrorCode | 'network' | 'unknown',
    message: string,
    public requestId = '',
    public details: FieldError[] = [],
    /** 429 时建议等待的秒数（Retry-After），没有时为 0 */
    public retryAfter = 0,
  ) {
    super(message)
  }

  /** 面板不可达（网络错误、502～504）：保留已显示的数据并自动重试（设计 43.6） */
  get unreachable(): boolean {
    return this.status === 0 || this.status === 502 || this.status === 503 || this.status === 504
  }

  /** 某个字段的错误（422 validation_failed 的 details） */
  field(name: string): string {
    return this.details.find((d) => d.field === name)?.message ?? ''
  }
}

/** 收到 401 时抛出：会话失效。统一由 onUnauthorized 处理（回到登录页），调用方只需停止后续操作。 */
export class UnauthorizedError extends ApiError {}

let unauthorizedHandler: (() => void) | null = null

/** 注册 401 的统一处理（设计 43.6：跳转登录页，登录后回到原页面）；/auth/me、/auth/login 的 401 不触发 */
export function onUnauthorized(fn: () => void) {
  unauthorizedHandler = fn
}

/**
 * 把错误转换为给用户看的一句话（设计 43.6、43.9）：不展示原始错误与堆栈；
 * 500 附错误编号；网络错误与 503 提示正在重试；401 返回空字符串（统一处理已在跳转登录页）。
 */
export function errorText(e: unknown, fallback = '操作失败，请稍后重试'): string {
  if (e instanceof UnauthorizedError) return '' // 已由 onUnauthorized 统一回到登录页
  if (!(e instanceof ApiError)) return fallback
  if (e.unreachable) return '无法连接面板，正在重试'
  if (e.status >= 500) return `服务器内部错误，请稍后重试${e.requestId ? `（编号 ${e.requestId}）` : ''}`
  if (e.status === 429 && e.retryAfter > 0) return `${e.message}（${e.retryAfter} 秒后可重试）`
  if (e.code === 'validation_failed' && e.details.length) return e.details[0].message
  return e.message || fallback
}

type Opts<P extends keyof Paths, M extends keyof Paths[P]> = (Op<P, M> extends { params: infer A } ? { params: A } : { params?: never }) &
  (Op<P, M> extends { query?: infer Q } ? { query?: Q } : { query?: never }) &
  (Op<P, M> extends { body: infer B } ? { body: B } : Op<P, M> extends { body?: infer B } ? { body?: B } : { body?: never })

type Optional<T> = Record<string, never> extends T ? [opts?: T] : [opts: T]

/** 拼出查询字符串；跳过 undefined 与空字符串 */
export function queryString(q: object | undefined): string {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(q ?? {})) if (v !== undefined && v !== '') qs.set(k, String(v))
  const s = qs.toString()
  return s ? '?' + s : ''
}

/**
 * 统一请求封装（设计 43.6）：同源请求自动带上会话 Cookie；修改类请求附带 X-CSRF-Token；
 * 把错误响应转换为 ApiError / UnauthorizedError。
 * 使用相对路径：开发时由 Vite 代理到 :8080；生产环境页面由面板内嵌提供，始终同源。
 */
export async function request<P extends keyof Paths, M extends Method & keyof Paths[P]>(
  method: M, path: P, ...[opts]: Optional<Opts<P, M>>
): Promise<Res<P, M>> {
  const o = (opts ?? {}) as { params?: Record<string, string | number>; query?: object; body?: unknown }
  const url = (path as string).replace(/\{(\w+)\}/g, (_, k: string) => encodeURIComponent(String(o.params?.[k] ?? ''))) + queryString(o.query)
  const m = method.toUpperCase()
  let res: Response
  try {
    res = await fetch('/api/v1' + url, {
      method: m,
      headers: {
        ...(m === 'GET' ? {} : { 'X-CSRF-Token': csrfToken }),
        ...(o.body === undefined ? {} : { 'Content-Type': 'application/json' }),
      },
      credentials: 'same-origin',
      body: o.body === undefined ? undefined : JSON.stringify(o.body),
    })
  } catch {
    throw new ApiError(0, 'network', '无法连接面板，请检查网络')
  }
  if (res.status === 204) return undefined as Res<P, M>
  const data = await res.json().catch(() => null)
  if (res.ok) return data as Res<P, M>
  const e = data?.error ?? {}
  const args = [
    res.status, e.code ?? 'unknown', e.message || `请求失败（HTTP ${res.status}）`, e.request_id ?? res.headers.get('X-Request-ID') ?? '',
    e.details ?? [], Number(res.headers.get('Retry-After')) || 0,
  ] as const
  if (res.status === 401) {
    // 登录接口与读取登录状态的 401 是正常结果（密码错误 / 尚未登录），由调用方处理
    if (path !== '/auth/login' && path !== '/auth/me') unauthorizedHandler?.()
    throw new UnauthorizedError(...args)
  }
  throw new ApiError(...args)
}

// ---- 节点 ----

/** 节点列表一次返回全部（总览的统计与筛选需要全量），next_cursor 恒为空 */
export async function listServers() {
  return (await request('get', '/servers')).items
}

/** 新建节点（状态：待安装），返回注册码与安装命令。 */
export function createServer(input: CreateServerInput) {
  return request('post', '/servers', { body: input })
}

/** 批量新建（设计 27.9）：全部校验通过才创建；返回每个节点的注册码与三种导出（只返回这一次） */
export function createServersBatch(items: CreateServerInput[], enrollTTL: '1h' | '24h' | '7d') {
  return request('post', '/servers/batch', { body: { items, enroll_ttl: enrollTTL } })
}

/** live 为真时请求按需实时模式：节点随后 30 秒内以 2 秒采样（设计 46.2）；只在详情页可见时使用 */
export function getServer(id: number, live = false) {
  return request('get', '/servers/{id}', { params: { id }, query: live ? { live: 1 } : undefined })
}

/** 修改节点信息（整体替换，未提供的可选字段视为清空），返回最新节点。 */
export function updateServer(id: number, input: CreateServerInput) {
  return request('put', '/servers/{id}', { params: { id }, body: input })
}

/** 删除节点及其全部历史数据，不可恢复。 */
export function deleteServer(id: number) {
  return request('delete', '/servers/{id}', { params: { id } })
}

/** 查看安装命令；不含完整注册码。 */
export function getInstallCommand(id: number) {
  return request('get', '/servers/{id}/install-command', { params: { id } })
}

/** 重新生成注册码，旧码立即失效（设计 27.4）。 */
export function regenerateEnrollCode(id: number, ttl: G.EnrollTTL = '24h') {
  return request('post', '/servers/{id}/enroll-code', { params: { id }, body: { enroll_ttl: ttl } })
}

export function revokeEnrollCode(id: number) {
  return request('delete', '/servers/{id}/enroll-code', { params: { id } })
}

/** 立即吊销节点的全部 Agent Token（设计 17.2）；需先重新验证密码 */
export function revokeAgentToken(id: number) {
  return request('post', '/servers/{id}/revoke-agent-token', { params: { id } })
}

/** 健康摘要（设计 1.5.7）：面板归纳的结论，Web 与 App 相同 */
export function getHealth(id: number) {
  return request('get', '/servers/{id}/health', { params: { id } })
}

export function getHistory(id: number, range: HistoryRange) {
  return request('get', '/servers/{id}/metrics/history', { params: { id }, query: { range } })
}

// ---- 流量（设计 5、19.8） ----

/** 最近 cycles 个计费周期的流量，最新在前 */
export async function getTrafficMonthly(id: number, cycles = 12) {
  return (await request('get', '/servers/{id}/traffic/monthly', { params: { id }, query: { cycles } })).items
}

/** 校准历史（最近 50 条，最新在前） */
export async function getTrafficAdjustments(id: number) {
  return (await request('get', '/servers/{id}/traffic/adjustments', { params: { id } })).items
}

/** 最近 days 天的每日流量，按日期升序，无数据的日期为 0 */
export async function getTrafficDaily(id: number, days = 30) {
  return (await request('get', '/servers/{id}/traffic/daily', { params: { id }, query: { days } })).items
}

/** 手动校准本周期已用流量（设计 5.7）：usedGB 按节点的单位口径，返回校准后的本周期流量 */
export function calibrateTraffic(id: number, usedGB: number, note = '') {
  return request('post', '/servers/{id}/traffic/calibrate', { params: { id }, body: { used_gb: usedGB, note } })
}

// ---- Agent 版本与升级（设计 29） ----

/** 立即从官方地址同步并验签最新版本 */
export function syncReleases() {
  return request('post', '/agent-releases/sync')
}

/** 已同步并验签的官方版本，最新在前 */
export function listReleases() {
  return request('get', '/agent-releases')
}

export function listUpgradeTasks(serverId?: number) {
  return request('get', '/upgrade-tasks', { query: { server_id: serverId } })
}

export function createUpgradeTasks(serverIds: number[], version: string) {
  return request('post', '/upgrade-tasks', { body: { server_ids: serverIds, version } })
}

export function cancelUpgradeTask(id: number) {
  return request('post', '/upgrade-tasks/{id}/cancel', { params: { id } })
}

// 灰度升级（设计 29.16）
export function listRollouts() {
  return request('get', '/upgrade-rollouts')
}

export function createRollout(body: Body<'/upgrade-rollouts', 'post'>) {
  return request('post', '/upgrade-rollouts', { body })
}

export function rolloutAction(id: number, action: 'pause' | 'resume' | 'cancel') {
  return request('post', '/upgrade-rollouts/{id}/{action}', { params: { id, action } })
}

// ---- 告警（设计 16、19.9） ----

/** 告警事件，按时间倒序；state 默认 active（正在告警） */
export function listAlerts(q: Query<'/alerts', 'get'>) {
  return request('get', '/alerts', { query: q })
}

export async function listAlertRules() {
  return (await request('get', '/alert-rules')).items
}

export function updateAlertRule(id: number, v: AlertRuleInput) {
  return request('put', '/alert-rules/{id}', { params: { id }, body: v })
}

/** 为分组或节点新增覆盖，以同 rule_key 的全局规则为基础 */
export function createAlertRule(v: Body<'/alert-rules', 'post'>) {
  return request('post', '/alert-rules', { body: v })
}

export function deleteAlertRule(id: number) {
  return request('delete', '/alert-rules/{id}', { params: { id } })
}

/** 预览：按当前数据，此规则会对几台节点触发（只比较阈值，不考虑持续时间） */
export function previewAlertRule(v: AlertRuleInput & { id?: number; rule_key?: string; scope_type?: string; scope_id?: string }) {
  return request('post', '/alert-rules/preview', { body: v })
}

export async function listSilences() {
  return (await request('get', '/silences')).items
}

/** duration 为 1h / 8h / 24h，留空表示直到手动结束；维护只能针对单个节点 */
export function createSilence(v: Body<'/silences', 'post'>) {
  return request('post', '/silences', { body: v })
}

export function endSilence(id: number) {
  return request('delete', '/silences/{id}', { params: { id } })
}

// ---- 告警通知（设计 16.5、18.15） ----

export function listChannels() {
  return request('get', '/notification-channels')
}
export function createChannel(v: G.NotifyChannelInput) {
  return request('post', '/notification-channels', { body: v })
}
export function updateChannel(id: number, v: G.NotifyChannelInput) {
  return request('put', '/notification-channels/{id}', { params: { id }, body: v })
}
export function deleteChannel(id: number) {
  return request('delete', '/notification-channels/{id}', { params: { id } })
}
export function testChannel(id: number) {
  return request('post', '/notification-channels/{id}/test', { params: { id } })
}
export function listDeliveries(limit = 30) {
  return request('get', '/notification-deliveries', { query: { limit } })
}

export function getQuietHours() {
  return request('get', '/settings/quiet-hours')
}
export function saveQuietHours(v: G.QuietHours) {
  return request('put', '/settings/quiet-hours', { body: v })
}

// ---- 日志（设计 24.8） ----

/** 审计日志，按时间倒序；next_cursor 为空表示没有更多 */
export function listAuditLogs(q: AuditQuery) {
  return request('get', '/audit-logs', { query: q })
}

/** CSV 导出地址（同源 GET，凭会话 Cookie 下载）；筛选条件同列表，最多 10000 条 */
export function auditExportURL(q: AuditQuery): string {
  return '/api/v1/audit-logs/export' + queryString({ ...q, cursor: undefined, limit: undefined })
}

/** 当前账号的登录会话，最近活动在前 */
export async function listSessions() {
  return (await request('get', '/auth/sessions')).items
}

/** 踢出一个会话（该浏览器需要重新登录） */
export function revokeSession(id: number) {
  return request('delete', '/auth/sessions/{id}', { params: { id } })
}

// ---- 云厂商账户（设计 44） ----

export async function listCloudAccounts() {
  return (await request('get', '/cloud-accounts')).items
}

/** 添加云账户；需先在 10 分钟内重新验证过密码（reauth） */
export function createCloudAccount(v: G.CloudAccountInput) {
  return request('post', '/cloud-accounts', { body: v })
}

/** 修改；credential 留空表示不变（提供时需先重新验证密码） */
export function updateCloudAccount(id: number, v: G.CloudAccountInput) {
  return request('put', '/cloud-accounts/{id}', { params: { id }, body: v })
}

/** 删除账户及同步的数据；需先重新验证密码 */
export function deleteCloudAccount(id: number) {
  return request('delete', '/cloud-accounts/{id}', { params: { id } })
}

/** 立即在后台同步（同一账户 1 分钟一次） */
export function syncCloudAccount(id: number) {
  return request('post', '/cloud-accounts/{id}/sync', { params: { id } })
}

export async function listCloudCosts(id: number) {
  return (await request('get', '/cloud-accounts/{id}/costs', { params: { id } })).items
}

export async function listCloudInstances(q: { account_id?: number; server_id?: number } = {}) {
  return (await request('get', '/cloud-instances', { query: q })).items
}

/** 关联或取消关联（serverId 为 null）节点（设计 44.5） */
/** 打开或关闭自动流量校准（设计 44.5）：每天用云厂商口径的本周期流量校准关联节点 */
export function setCloudAutoCalibrate(id: number, enabled: boolean) {
  return request('put', '/cloud-instances/{id}/auto-calibrate', { params: { id }, body: { enabled } })
}

export function linkCloudInstance(id: number, serverId: number | null) {
  return request('put', '/cloud-instances/{id}/server', { params: { id }, body: { server_id: serverId } })
}

/** 把关联实例的到期时间写入节点的到期日 */
export function applyCloudExpire(id: number) {
  return request('post', '/cloud-instances/{id}/apply-expire', { params: { id } })
}

// ---- 只读 API Key（设计 45.2） ----

export async function listAPIKeys() {
  return (await request('get', '/api-keys')).items
}

/** 创建只读 API Key；需先重新验证密码。返回的 key 只出现这一次 */
export function createAPIKey(v: G.APIKeyInput) {
  return request('post', '/api-keys', { body: v })
}

export function revokeAPIKey(id: number) {
  return request('delete', '/api-keys/{id}', { params: { id } })
}

// ---- App 接入（设计 8.4、19.2、19.4） ----

export async function listAppKeys() {
  return (await request('get', '/app-access-keys')).items
}

/** 创建 App 配对 AK；需先重新验证密码。返回的 AK 与配对链接只出现这一次 */
export function createAppKey(v: G.AppAccessKeyInput) {
  return request('post', '/app-access-keys', { body: v })
}

/** 吊销 AK；revokeDevices 时同时吊销用它配对的设备 */
export function revokeAppKey(id: number, revokeDevices: boolean) {
  return request('post', '/app-access-keys/{id}/revoke', { params: { id }, body: { revoke_devices: revokeDevices } })
}

export async function listAppDevices() {
  return (await request('get', '/app-devices')).items
}

export function revokeAppDevice(id: number) {
  return request('post', '/app-devices/{id}/revoke', { params: { id } })
}

// ---- SSL 证书到期监控（设计 33.3） ----

export async function listSSLMonitors() {
  return (await request('get', '/ssl-monitors')).items
}

export function createSSLMonitor(host: string, note: string) {
  return request('post', '/ssl-monitors', { body: { host, note } })
}

export function checkSSLMonitor(id: number) {
  return request('post', '/ssl-monitors/{id}/check', { params: { id } })
}

export function deleteSSLMonitor(id: number) {
  return request('delete', '/ssl-monitors/{id}', { params: { id } })
}

// ---- 登录（设计 8.2、17.4、19.1） ----

/** 获取一次登录验证码；每次登录尝试后都需重新获取（一次性）。 */
export function getCaptcha() {
  return request('get', '/auth/captcha')
}

/** 登录。失败时抛出 ApiError（401 用户名或密码错误、400 验证码未通过、429 失败过多）。 */
export async function login(username: string, password: string, remember: boolean, captcha?: CaptchaAnswer) {
  const me = await request('post', '/auth/login', {
    body: {
      username, password, remember,
      captcha_id: captcha?.id ?? '', captcha_x: captcha?.x ?? 0, captcha_ms: captcha?.ms ?? 0,
    },
  })
  setCsrf(me.csrf_token)
  return me
}

/** 读取当前登录状态；未登录时抛出 UnauthorizedError。 */
export async function getMe() {
  const me = await request('get', '/auth/me')
  setCsrf(me.csrf_token)
  return me
}

export function logoutSession() {
  return request('post', '/auth/logout')
}

/** 修改密码；成功后其他会话全部失效（设计 17.4）。 */
export function changePassword(current: string, next: string) {
  return request('post', '/auth/password', { body: { current_password: current, new_password: next } })
}

/** 敏感操作前重新输入密码，10 分钟内有效（设计 17.4）。 */
export function reauth(password: string) {
  return request('post', '/auth/reauth', { body: { password } })
}
