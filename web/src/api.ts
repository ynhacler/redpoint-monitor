// Thin API client. Types mirror internal/protocol and internal/server JSON.
// TODO(M3): replace the dev admin token with Web session login.

// rx/tx_bytes are cumulative kernel counters; rx/tx_speed are bytes/s computed by the agent.
export interface NetIface {
  interface: string
  rx_bytes: number
  tx_bytes: number
  rx_speed: number
  tx_speed: number
}

// One agent report (internal/protocol.Report). Byte values are raw bytes, usage is 0-100.
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

// One row of GET /api/v1/servers. Status is decided by the server from last_seen_at
// (online ≤30s, unknown ≤120s, else offline; design ch. 22), so Web and App agree.
export interface ServerView {
  id: number
  name: string
  status: 'online' | 'unknown' | 'offline'
  last_seen_at: number // unix seconds; 0 = never reported
  latest?: Report // absent until the first report arrives
  // Current billing cycle. used is rx, tx or rx+tx depending on the server's count mode;
  // limit is in bytes, 0 = unlimited.
  traffic: { cycle_start: string; rx: number; tx: number; used: number; limit: number }
}

const TOKEN_KEY = 'vpsmon.token'

// The admin token lives in sessionStorage, not localStorage: it is gone when the tab closes,
// which limits exposure until real session login lands (M3). try/catch because storage
// access throws in some private-browsing modes.
export function getToken(): string {
  try {
    return sessionStorage.getItem(TOKEN_KEY) ?? ''
  } catch {
    return ''
  }
}

export function setToken(t: string) {
  try {
    sessionStorage.setItem(TOKEN_KEY, t)
  } catch {
    /* storage unavailable */
  }
}

// Thrown on 401 so the UI can tell "wrong/expired token" (ask again) from
// "server unreachable" (keep polling and show a banner).
export class UnauthorizedError extends Error {}

// Relative URL: in dev Vite proxies /api to :8080; in production the server embeds and
// serves this page itself, so it is always same-origin.
export async function listServers(): Promise<ServerView[]> {
  const res = await fetch('/api/v1/servers', { headers: { Authorization: `Bearer ${getToken()}` } })
  if (res.status === 401) throw new UnauthorizedError()
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

// fmtBytes formats bytes as "1.5 GB" (one decimal below 10, none above).
// perSec appends "/s" for speeds.
export function fmtBytes(n: number, perSec = false): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1000 && i < units.length - 1) {
    n /= 1000 // decimal units, matching most VPS providers (design 5.8)
    i++
  }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${units[i]}${perSec ? '/s' : ''}`
}
