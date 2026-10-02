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
  status: 'online' | 'unknown' | 'offline'
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

/**
 * 收到 401 时抛出，让界面区分“Token 错误或失效”（重新输入）与“面板不可达”（继续轮询并提示）。
 */
export class UnauthorizedError extends Error {}

/**
 * 获取节点列表。
 * 使用相对路径：开发时由 Vite 代理到 :8080；生产环境页面由面板内嵌提供，始终同源。
 * @throws UnauthorizedError Token 无效（401）
 */
export async function listServers(): Promise<ServerView[]> {
  const res = await fetch('/api/v1/servers', { headers: { Authorization: `Bearer ${getToken()}` } })
  if (res.status === 401) throw new UnauthorizedError()
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
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
