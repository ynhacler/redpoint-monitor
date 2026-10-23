// 接口客户端（设计 19.0.1、43.6）。类型由 api/openapi.yaml 生成（api.gen.ts，make api-types），不手写：
// 每个函数通过 request(方法, 契约中的路径, 参数) 调用，路径参数、查询参数、请求体与响应都按契约检查。
import type { Paths } from './api.gen'
import type * as G from './api.gen'

export type {
  AgentRelease, AlertEvent, HealthSummary, IPInfo, ListenSetting, ListenSettingInput, SSLMonitor, UnlockView, Subscription, SubscriptionInput, APIKey, APIKeyInput, AppAccessKey, AppAccessKeyInput, AppDevice, CloudAccount, CloudAccountInput, CloudCost, CloudInstance, CloudTrafficPool, AlertRule, Delivery, EnrollCodeView, InstallCommand, Me, MetricPoint, NotifyChannel,
  LatencyView, NodeProbe, NotifyChannelInput, ProbeSettings, ProbeTarget, ProbeTargetInput, QuietHours, QuietHoursView, Report, ServerView, Silence, ServiceMonitor, ServiceMonitorInput, TrafficView, UpgradeRollout, UpgradeTask,
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
  // 二进制请求体（如上传的图标 PNG）原样发送，其余按 JSON 发送
  const raw = o.body instanceof Blob
  let res: Response
  try {
    res = await fetch('/api/v1' + url, {
      method: m,
      headers: {
        ...(m === 'GET' ? {} : { 'X-CSRF-Token': csrfToken }),
        ...(o.body === undefined ? {} : { 'Content-Type': raw ? (o.body as Blob).type || 'application/octet-stream' : 'application/json' }),
      },
      credentials: 'same-origin',
      body: o.body === undefined ? undefined : raw ? (o.body as Blob) : JSON.stringify(o.body),
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
    // 登录接口（含两步验证的第二步）与读取登录状态的 401 是正常结果（密码或验证码错误 / 尚未登录），由调用方处理
    if (path !== '/auth/login' && path !== '/auth/login/totp' && path !== '/auth/me') unauthorizedHandler?.()
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

/** 保存节点的自定义顺序（修订第 150 条）：ids 为完整顺序，不在其中的节点排在后面。 */
export function putServerOrder(ids: number[]) {
  return request('put', '/servers/order', { body: { ids } })
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

/** 撤销节点尚未使用的注册码 */
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

/** 节点的历史曲线（1h / 6h / 24h / 7d / 30d，设计 19.7） */
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

/** Agent 升级任务；传 serverId 时只看该节点 */
export function listUpgradeTasks(serverId?: number) {
  return request('get', '/upgrade-tasks', { query: { server_id: serverId } })
}

/** 为选中的节点创建升级到 version 的任务 */
export function createUpgradeTasks(serverIds: number[], version: string) {
  return request('post', '/upgrade-tasks', { body: { server_ids: serverIds, version } })
}

/** 取消尚未开始的升级任务 */
export function cancelUpgradeTask(id: number) {
  return request('post', '/upgrade-tasks/{id}/cancel', { params: { id } })
}

// 灰度升级（设计 29.16）
export function listRollouts() {
  return request('get', '/upgrade-rollouts')
}

/** 创建灰度升级 */
export function createRollout(body: Body<'/upgrade-rollouts', 'post'>) {
  return request('post', '/upgrade-rollouts', { body })
}

/** 暂停、继续或取消灰度升级 */
export function rolloutAction(id: number, action: 'pause' | 'resume' | 'cancel') {
  return request('post', '/upgrade-rollouts/{id}/{action}', { params: { id, action } })
}

/** 面板版本（git describe，设计 40.3.2），显示在页脚 */
export async function getPanelVersion() {
  return (await request('get', '/version')).version
}

// ---- 告警（设计 16、19.9） ----

/** 告警事件，按时间倒序；state 默认 active（正在告警） */
export function listAlerts(q: Query<'/alerts', 'get'>) {
  return request('get', '/alerts', { query: q })
}

/** 设置节点的容器名称（容器 ID 前 12 位 → 名称；整体替换，名称为空表示删除，修订第 164 条） */
export function putContainerNames(id: number, names: Record<string, string>) {
  return request('put', '/servers/{id}/container-names', { params: { id }, body: { names } })
}

/** 不再提醒已停止的容器：以当前运行的为准；省略 label 表示全部（修订第 166 条） */
export function forgetContainers(id: number, label?: string) {
  return request('post', '/servers/{id}/containers/forget', { params: { id }, body: label ? { label } : {} })
}

/** 不再提醒没有运行的代理；省略 name 表示全部（修订第 169 条） */
export function forgetProxies(id: number, name?: string) {
  return request('post', '/servers/{id}/proxies/forget', { params: { id }, body: name ? { name } : {} })
}

/** 设置端口基线（允许对公网开放的监听端口，如 22/tcp）；空数组表示取消（修订第 170 条） */
export function putPortBaseline(id: number, ports: string[]) {
  return request('put', '/servers/{id}/port-baseline', { params: { id }, body: { ports } })
}

/** 从面板检测公网可达：TCP 连接节点公网地址上它报告的监听端口（修订第 171 条） */
export function checkExposure(id: number) {
  return request('post', '/servers/{id}/exposure-check', { params: { id } })
}

/** 系统事件（修订第 163 条） */
export type SystemEvent = G.SystemEvent
/** 系统事件：最新在前，cursor 翻页；按调用方的授权范围过滤 */
export function listSystemEvents(cursor?: string, limit = 50) {
  return request('get', '/system-events', { query: { cursor, limit } })
}

/** 全部告警规则（全局、分组、节点三层） */
export async function listAlertRules() {
  return (await request('get', '/alert-rules')).items
}

/** 修改告警规则 */
export function updateAlertRule(id: number, v: AlertRuleInput) {
  return request('put', '/alert-rules/{id}', { params: { id }, body: v })
}

/** 为分组或节点新增覆盖，以同 rule_key 的全局规则为基础 */
export function createAlertRule(v: Body<'/alert-rules', 'post'>) {
  return request('post', '/alert-rules', { body: v })
}

/** 删除分组或节点的告警规则（全局规则只能停用） */
export function deleteAlertRule(id: number) {
  return request('delete', '/alert-rules/{id}', { params: { id } })
}

/** 预览：按当前数据，此规则会对几台节点触发（只比较阈值，不考虑持续时间） */
export function previewAlertRule(v: AlertRuleInput & { id?: number; rule_key?: string; scope_type?: string; scope_id?: string }) {
  return request('post', '/alert-rules/preview', { body: v })
}

/** 生效中的静音与维护 */
export async function listSilences() {
  return (await request('get', '/silences')).items
}

/** duration 为 1h / 8h / 24h，留空表示直到手动结束；维护只能针对单个节点 */
export function createSilence(v: Body<'/silences', 'post'>) {
  return request('post', '/silences', { body: v })
}

/** 立即结束静音或维护 */
export function endSilence(id: number) {
  return request('delete', '/silences/{id}', { params: { id } })
}

// ---- 告警通知（设计 16.5、18.15） ----

/** 全部通知渠道 */
export function listChannels() {
  return request('get', '/notification-channels')
}
/** 添加通知渠道 */
export function createChannel(v: G.NotifyChannelInput) {
  return request('post', '/notification-channels', { body: v })
}
/** 修改通知渠道 */
export function updateChannel(id: number, v: G.NotifyChannelInput) {
  return request('put', '/notification-channels/{id}', { params: { id }, body: v })
}
/** 删除通知渠道 */
export function deleteChannel(id: number) {
  return request('delete', '/notification-channels/{id}', { params: { id } })
}
/** 向通知渠道发送一条测试消息 */
export function testChannel(id: number) {
  return request('post', '/notification-channels/{id}/test', { params: { id } })
}
/** 最近的通知发送记录 */
export function listDeliveries(limit = 30) {
  return request('get', '/notification-deliveries', { query: { limit } })
}

/** 免打扰时段 */
export function getQuietHours() {
  return request('get', '/settings/quiet-hours')
}
/** 保存免打扰时段 */
export function saveQuietHours(v: G.QuietHours) {
  return request('put', '/settings/quiet-hours', { body: v })
}

/** 面板心跳（设计 1.10.3，修订第 159 条） */
export type HeartbeatView = G.HeartbeatView
/** 面板心跳的设置与最近一次结果 */
export function getHeartbeat() {
  return request('get', '/settings/heartbeat')
}
/** 保存面板心跳；url 留空保留原地址 */
export function saveHeartbeat(v: G.HeartbeatInput) {
  return request('put', '/settings/heartbeat', { body: v })
}
/** 立即发送一次心跳 */
export function testHeartbeat() {
  return request('post', '/settings/heartbeat/test')
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

/** 全部云账户（不含凭证，只有末 4 位） */
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

/** 云账户的按月费用 */
export async function listCloudCosts(id: number) {
  return (await request('get', '/cloud-accounts/{id}/costs', { params: { id } })).items
}

/** 云实例；可按账户或关联的节点筛选 */
export async function listCloudInstances(q: { account_id?: number; server_id?: number } = {}) {
  return (await request('get', '/cloud-instances', { query: q })).items
}

/** 关联或取消关联（serverId 为 null）节点（设计 44.5） */
/** 打开或关闭自动流量校准（设计 44.5）：每天用云厂商口径的本周期流量校准关联节点 */
export function setCloudAutoCalibrate(id: number, enabled: boolean) {
  return request('put', '/cloud-instances/{id}/auto-calibrate', { params: { id }, body: { enabled } })
}

/** 把云实例关联到节点（serverId 为 null 时取消关联） */
export function linkCloudInstance(id: number, serverId: number | null) {
  return request('put', '/cloud-instances/{id}/server', { params: { id }, body: { server_id: serverId } })
}

/** 把关联实例的到期时间写入节点的到期日 */
export function applyCloudExpire(id: number) {
  return request('post', '/cloud-instances/{id}/apply-expire', { params: { id } })
}

// ---- 只读 API Key（设计 45.2） ----

/** 全部只读 API 密钥（不含密钥本身） */
export async function listAPIKeys() {
  return (await request('get', '/api-keys')).items
}

/** 创建只读 API Key；需先重新验证密码。返回的 key 只出现这一次 */
export function createAPIKey(v: G.APIKeyInput) {
  return request('post', '/api-keys', { body: v })
}

/** 吊销 API 密钥 */
export function revokeAPIKey(id: number) {
  return request('delete', '/api-keys/{id}', { params: { id } })
}

// ---- App 接入（设计 8.4、19.2、19.4） ----

/** 全部 App 接入码（AK） */
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

/** 已配对的 App 设备 */
export async function listAppDevices() {
  return (await request('get', '/app-devices')).items
}

/** 吊销 App 设备，设备立即失去访问权限 */
export function revokeAppDevice(id: number) {
  return request('post', '/app-devices/{id}/revoke', { params: { id } })
}

// ---- 订阅（设计 47） ----

/** 订阅列表（下次扣费日在前） */
export async function listSubscriptions() {
  return (await request('get', '/subscriptions')).items
}

/** 添加订阅 */
export function createSubscription(body: G.SubscriptionInput) {
  return request('post', '/subscriptions', { body })
}

/** 修改订阅（整体替换） */
export function updateSubscription(id: number, body: G.SubscriptionInput) {
  return request('put', '/subscriptions/{id}', { params: { id }, body })
}

/** 上传订阅图标（PNG；调用方先在浏览器中裁剪缩放） */
export function uploadSubscriptionLogo(id: number, png: Blob) {
  // 契约中请求体为 image/png，生成的类型只描述 JSON 请求体，这里按原样传入 Blob
  return request('put', '/subscriptions/{id}/logo', { params: { id }, body: png } as never)
}

/** 删除上传的图标 */
export function deleteSubscriptionLogo(id: number) {
  return request('delete', '/subscriptions/{id}/logo', { params: { id } })
}

/** 上传图标的地址（带版本号，可长期缓存） */
export const subscriptionLogoURL = (s: { id: number; logo_version: number }) => `/api/v1/subscriptions/${s.id}/logo?v=${s.logo_version}`

/** 删除订阅 */
export function deleteSubscription(id: number) {
  return request('delete', '/subscriptions/{id}', { params: { id } })
}

// ---- SSL 证书到期监控（设计 33.3） ----

/** 全部证书监控 */
export async function listSSLMonitors() {
  return (await request('get', '/ssl-monitors')).items
}

/** 添加证书监控 */
export function createSSLMonitor(host: string, note: string) {
  return request('post', '/ssl-monitors', { body: { host, note } })
}

/** 立即检查一次证书 */
export function checkSSLMonitor(id: number) {
  return request('post', '/ssl-monitors/{id}/check', { params: { id } })
}

/** 删除证书监控 */
export function deleteSSLMonitor(id: number) {
  return request('delete', '/ssl-monitors/{id}', { params: { id } })
}

// 服务监控（设计 33.2）
export async function listServiceMonitors() {
  return (await request('get', '/service-monitors')).items
}

/** 添加服务监控 */
export function createServiceMonitor(body: G.ServiceMonitorInput) {
  return request('post', '/service-monitors', { body })
}

/** 修改服务监控 */
export function updateServiceMonitor(id: number, body: G.ServiceMonitorInput) {
  return request('put', '/service-monitors/{id}', { params: { id }, body })
}

/** 删除服务监控 */
export function deleteServiceMonitor(id: number) {
  return request('delete', '/service-monitors/{id}', { params: { id } })
}

/** 立即检查一次服务 */
export function checkServiceMonitor(id: number) {
  return request('post', '/service-monitors/{id}/check', { params: { id } })
}

/** 服务监控的检查记录（24 小时或 7 天） */
export async function serviceChecks(id: number, range: '24h' | '7d') {
  return (await request('get', '/service-monitors/{id}/checks', { params: { id }, query: { range } })).items
}

// 测速（设计 42.3.2）
export type LatencyTarget = G.LatencyView['targets'][number]

/** 节点的测速：各目标最近 1 小时汇总与曲线（设计 42.3.2） */
export function getLatency(id: number, range: '24h' | '7d') {
  return request('get', '/servers/{id}/latency', { params: { id }, query: { range } })
}

/** 测速对比：全部节点到各目标最近 1 小时的延迟与丢包（修订第 137 条） */
export function getLatencyOverview() {
  return request('get', '/latency-overview')
}
export type LatencyOverview = Res<'/latency-overview', 'get'>

// 国内探针与被墙检测（设计 42.2，修订第 154 条）
export type ReachProbe = G.ReachProbe
export type ServerReach = Res<'/servers/{id}/reach', 'get'>

/** 国内探针设置（管理员） */
export function getReachProbes() {
  return request('get', '/reach-probes')
}
export type ReachProbes = Res<'/reach-probes', 'get'>

/** 整体替换国内探针，随探针的下一份上报下发 */
export function putReachProbes(probes: ReachProbe[]) {
  return request('put', '/reach-probes', { body: { probes } })
}

/** 节点的国内可达性：各探针最近结果与判定 */
export function getServerReach(id: number) {
  return request('get', '/servers/{id}/reach', { params: { id } })
}

/** 节点自己的测速设置（跟随全局 / 自定义 / 关闭） */
export function getNodeProbe(id: number) {
  return request('get', '/servers/{id}/probe', { params: { id } })
}

/** 保存节点自己的测速设置，随下一份上报下发 */
export function putNodeProbe(id: number, body: G.NodeProbe) {
  return request('put', '/servers/{id}/probe', { params: { id }, body })
}

// ---- 监听端口（设计 51） ----

/** Web 端口、Agent 专用端口与 Agent 地址 */
export function getListenSetting() {
  return request('get', '/settings/listen')
}

/** 修改监听端口，保存后立即生效；修改 Web 端口时旧端口保留到 closeOldListen */
export function putListenSetting(body: G.ListenSettingInput) {
  return request('put', '/settings/listen', { body })
}

/** 关闭修改前的旧 Web 端口 */
export function closeOldListen() {
  return request('post', '/settings/listen/close-old')
}

// ---- 节点 IP 信息（设计 49） ----

/** IP 信息查询开关 */
export function getIPInfoSetting() {
  return request('get', '/settings/ipinfo')
}

/** 打开或关闭 ipinfo.io 查询 */
export function putIPInfoSetting(enabled: boolean) {
  return request('put', '/settings/ipinfo', { body: { enabled } })
}

/** 立即查询一个节点的 IP 信息 */
export function refreshIPInfo(id: number) {
  return request('post', '/servers/{id}/ipinfo', { params: { id } })
}

/** 请求节点的 Agent 检测一次流媒体与网站解锁（设计 50） */
export function requestUnlockCheck(id: number) {
  return request('post', '/servers/{id}/unlock-check', { params: { id } })
}

/** 测速的全局设置：频率、超时与样本间隔 */
export function getProbeSettings() {
  return request('get', '/settings/probe')
}

/** 保存测速的全局设置 */
export function putProbeSettings(body: G.ProbeSettings) {
  return request('put', '/settings/probe', { body })
}

/** 探测目标：默认只列共用目标；传 serverId 时再加上这台节点自己的目标 */
export async function listProbeTargets(serverId?: number) {
  return (await request('get', '/probe-targets', { query: serverId ? { server_id: serverId } : {} })).items
}

/** 添加测速目标 */
export function createProbeTarget(body: G.ProbeTargetInput) {
  return request('post', '/probe-targets', { body })
}

/** 修改测速目标（省略的字段不变） */
export function updateProbeTarget(id: number, body: G.ProbeTargetInput) {
  return request('put', '/probe-targets/{id}', { params: { id }, body })
}

/** 删除测速目标及其结果 */
export function deleteProbeTarget(id: number) {
  return request('delete', '/probe-targets/{id}', { params: { id } })
}

// ---- 登录（设计 8.2、17.4、19.1） ----

/** 获取一次登录验证码；每次登录尝试后都需重新获取（一次性）。 */
export function getCaptcha() {
  return request('get', '/auth/captcha')
}

/** 登录。失败时抛出 ApiError（401 用户名或密码错误、400 验证码未通过、429 失败过多）。
 * 账号开启了两步验证时返回 challenge，再用 loginTotp 提交验证码（修订第 160 条）。 */
export async function login(username: string, password: string, remember: boolean, captcha?: CaptchaAnswer) {
  const res = await request('post', '/auth/login', {
    body: {
      username, password, remember,
      captcha_id: captcha?.id ?? '', captcha_x: captcha?.x ?? 0, captcha_ms: captcha?.ms ?? 0,
    },
  })
  if ('challenge' in res) return res
  setCsrf(res.csrf_token)
  return res
}

/** 两步验证的第二步：认证器的 6 位验证码或恢复码。401 为验证码不正确或登录已过期。 */
export async function loginTotp(challenge: string, code: string) {
  const me = await request('post', '/auth/login/totp', { body: { challenge, code } })
  setCsrf(me.csrf_token)
  return me
}

/** Web 账号（管理员与查看者，修订第 161 条） */
export type UserView = G.UserView
/** 账号列表，管理员在前 */
export async function listUsers() {
  return (await request('get', '/users')).items
}
/** 新建查看者（需先重新验证密码）；返回随机初始密码，只显示这一次 */
export function createViewer(username: string) {
  return request('post', '/users', { body: { username } })
}
/** 删除查看者（需先重新验证密码） */
export function deleteViewer(id: number) {
  return request('delete', '/users/{id}', { params: { id } })
}
/** 重置查看者密码（需先重新验证密码）；返回新的随机密码 */
export function resetViewerPassword(id: number) {
  return request('post', '/users/{id}/password', { params: { id } })
}

/** 开始开启两步验证：生成新密钥（需先重新验证密码） */
export function totpSetup() {
  return request('post', '/auth/totp/setup')
}
/** 用认证器的验证码确认开启，返回恢复码（只显示这一次） */
export function totpEnable(code: string) {
  return request('post', '/auth/totp/enable', { body: { code } })
}
/** 关闭两步验证（需先重新验证密码） */
export function totpDisable() {
  return request('post', '/auth/totp/disable')
}

/** 读取当前登录状态；未登录时抛出 UnauthorizedError。 */
export async function getMe() {
  const me = await request('get', '/auth/me')
  setCsrf(me.csrf_token)
  return me
}

/** 退出登录：吊销当前会话 */
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
