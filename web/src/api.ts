// 轻量接口客户端，类型与 internal/protocol、internal/server 的 JSON 保持一致。
// TODO(B): 类型改为由 api/openapi.yaml 生成，不再手写（设计 19.0.1）。
// TODO(A2): 用 Web 会话登录替换开发用 admin token（设计 8.2）。

/** 一块网卡的数据：rx/tx_bytes 为内核累计字节数，rx/tx_speed 为 Agent 计算的字节/秒。 */
export interface NetIface {
  interface: string
  rx_bytes: number
  tx_bytes: number
  rx_speed: number
  tx_speed: number
}

/** 一份 Agent 上报（internal/protocol.Report）。字节类字段为原始字节数，usage 为 0～100 的百分比。 */
export interface Report {
  timestamp: number
  agent_version: string
  system: { hostname: string; os: string; os_version: string; kernel: string; arch: string; uptime: number }
  cpu: { usage: number; cores: number; load1: number }
  memory: { total: number; used: number; usage: number }
  swap: { total: number; used: number }
  disk: { mount: string; total: number; used: number; usage: number }[]
  network: NetIface[]
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
  /** 注册时的实际值（设计 18.2） */
  hostname: string
  ipv4: string
  ipv6: string
  group: string
  note: string
  provider: string
  plan: string
  region: string
  /** 续费价格 × 100 */
  price_cents: number
  currency: string
  billing_period: string
  /** YYYY-MM-DD，空表示未填 */
  expire_date: string
  traffic_limit_bytes: number
  traffic_reset_day: number
  traffic_count_mode: 'sum' | 'rx' | 'tx' | 'max'
  /** 最后一次上报时间，Unix 秒；0 表示从未上报 */
  last_seen_at: number
  /** 收到首次上报之前不存在 */
  latest?: Report
  /** 当前计费周期：used 按节点的计费模式取 rx、tx 或 rx+tx；limit 为字节，0 表示不限（设计 1.2.4） */
  traffic: { cycle_start: string; rx: number; tx: number; used: number; limit: number }
}

const TOKEN_KEY = 'vpsmon.token'

/**
 * 读取开发用 admin token。
 * 【安全】存放在 sessionStorage 而不是 localStorage：关闭标签页即清除，在正式登录（A2）之前缩小泄露面。
 * 部分隐私浏览模式下访问 storage 会抛异常，因此用 try/catch。
 */
export function getToken(): string {
  try {
    return sessionStorage.getItem(TOKEN_KEY) ?? ''
  } catch {
    return ''
  }
}

/** 保存开发用 admin token（仅当前标签页有效）。 */
export function setToken(t: string) {
  try {
    sessionStorage.setItem(TOKEN_KEY, t)
  } catch {
    /* storage 不可用时忽略 */
  }
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

/** 收到 401 时抛出，让界面区分“Token 错误或失效”（重新输入）与“面板不可达”（继续轮询并提示）。 */
export class UnauthorizedError extends ApiError {}

/**
 * 统一请求封装（设计 43.6）：附带 Token，把错误响应转换为 ApiError / UnauthorizedError。
 * 使用相对路径：开发时由 Vite 代理到 :8080；生产环境页面由面板内嵌提供，始终同源。
 */
async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch('/api/v1' + path, {
      method,
      headers: {
        Authorization: `Bearer ${getToken()}`,
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
      },
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
  /** 月流量额度，十进制 GB（设计 5.8）；0 或不填表示不限 */
  traffic_limit_gb?: number
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

/**
 * 把字节数格式化为可读字符串，如 “1.5 GB”（小于 10 保留一位小数）。
 * 使用十进制单位（1 GB = 10^9 Byte），与多数服务商一致（设计 5.8）。
 * @param n 字节数
 * @param perSec 为 true 时追加 “/s”，用于网速
 */
export function fmtBytes(n: number, perSec = false): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1000 && i < units.length - 1) {
    n /= 1000 // 十进制单位（设计 5.8）
    i++
  }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${units[i]}${perSec ? '/s' : ''}`
}
