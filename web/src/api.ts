// Thin API client. Types mirror internal/protocol and internal/server JSON.
// TODO(M3): replace the dev admin token with Web session login.

export interface NetIface {
  interface: string
  rx_bytes: number
  tx_bytes: number
  rx_speed: number
  tx_speed: number
}

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

export interface ServerView {
  id: number
  name: string
  status: 'online' | 'unknown' | 'offline'
  last_seen_at: number
  latest?: Report
  traffic: { cycle_start: string; rx: number; tx: number; used: number; limit: number }
}

const TOKEN_KEY = 'vpsmon.token'

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

export class UnauthorizedError extends Error {}

export async function listServers(): Promise<ServerView[]> {
  const res = await fetch('/api/v1/servers', { headers: { Authorization: `Bearer ${getToken()}` } })
  if (res.status === 401) throw new UnauthorizedError()
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export function fmtBytes(n: number, perSec = false): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1000 && i < units.length - 1) {
    n /= 1000 // decimal units, matching most VPS providers (design 5.8)
    i++
  }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${units[i]}${perSec ? '/s' : ''}`
}
