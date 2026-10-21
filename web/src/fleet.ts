// 舰队总览（修订第 174 条）：仪表板顶部的三张卡片——全部节点本月剩余流量与“预计哪台先用完”、
// 每月总成本（节点续费折算 + 云账单本月预估）、未来 30 天到期。纯函数，数据都来自节点列表与云账户。
import type { CloudAccount, ServerView } from './api'
import { monthlyCents, upcomingRenewals, type CostNode, type Renewal } from './costs'

/** 一台有流量额度的节点的预计 */
export interface TrafficRisk {
  server: ServerView
  /** 已用占额度的百分比 */
  pct: number
  /** 按日均推算还能用几天；已用完为 0；不会超过额度或数据不足时为 null */
  daysLeft: number | null
}

/** 全部有额度节点的剩余流量与风险，最可能先用完的在前 */
export interface TrafficFleet {
  /** 有流量额度的节点数 */
  limited: number
  /** 剩余流量合计（字节；已超额的按 0 计） */
  remaining: number
  /** 预计周期内会用完（含已用完）的节点，最先用完的在前 */
  risks: TrafficRisk[]
}

/** 汇总有流量额度的节点：剩余合计，以及按日均推算会在本周期用完（含已用完）的节点 */
export function trafficFleet(servers: ServerView[]): TrafficFleet {
  let limited = 0
  let remaining = 0
  const risks: TrafficRisk[] = []
  for (const s of servers) {
    const t = s.traffic
    if (!t || t.limit <= 0 || s.status === 'pending') continue
    limited++
    remaining += Math.max(t.limit - t.used, 0)
    const pct = (t.used / t.limit) * 100
    if (t.used >= t.limit) risks.push({ server: s, pct, daysLeft: 0 })
    else if (t.forecast?.over && t.forecast.daily > 0) {
      risks.push({ server: s, pct, daysLeft: Math.max(Math.floor((t.limit - t.used) / t.forecast.daily), 0) })
    }
  }
  risks.sort((a, b) => (a.daysLeft ?? 1e9) - (b.daysLeft ?? 1e9) || b.pct - a.pct)
  return { limited, remaining, risks }
}

/** 某个币种的每月总成本 */
export interface CostLine {
  currency: string
  /** 节点续费折算的月均（分） */
  nodes: number
  /** 云账单本月预估（没有预估时为本月已产生，分） */
  cloud: number
}

/** 每月总成本：按币种分别合计（不换算），以及资料不全的提醒 */
export interface CostFleet {
  lines: CostLine[]
  /** 没填价格或周期的节点数（待安装的不算） */
  unpriced: number
  /** 关联了云实例、价格已计入云账单而不再重复计算的节点数 */
  inCloud: number
}

/** 按币种汇总每月总成本；linked 为关联了云实例的节点（有云账单时不再计入它们的续费价格） */
export function costFleet(servers: ServerView[], accounts: CloudAccount[], linked: Set<number>): CostFleet {
  const by = new Map<string, CostLine>()
  const line = (cur: string) => {
    const k = cur || '—'
    if (!by.has(k)) by.set(k, { currency: k, nodes: 0, cloud: 0 })
    return by.get(k)!
  }
  let unpriced = 0
  let inCloud = 0
  for (const s of servers) {
    if (s.status === 'pending') continue
    if (linked.has(s.id) && accounts.some((a) => a.current_cost)) {
      inCloud++ // 云实例的费用在云账单里，节点上填的价格不再重复计入
      continue
    }
    const n: CostNode = { id: s.id, name: s.name, group: s.group, provider: s.provider, price_cents: s.price_cents,
      currency: s.currency, billing_period: s.billing_period, expire_date: s.expire_date }
    if (!s.price_cents || !s.billing_period) {
      unpriced++
      continue
    }
    const m = monthlyCents(n)
    if (m != null) line(s.currency).nodes += m
  }
  for (const a of accounts) {
    const c = a.current_cost
    if (c) line(c.currency).cloud += c.forecast_cents ?? c.amount_cents
  }
  return { lines: [...by.values()].filter((l) => l.nodes || l.cloud).sort((a, b) => b.nodes + b.cloud - (a.nodes + a.cloud)), unpriced, inCloud }
}

/** 未来 days 天内到期（含已过期 30 天内）的节点 */
export function renewalsSoon(servers: ServerView[], days = 30): Renewal[] {
  return upcomingRenewals(servers.filter((s) => s.status !== 'pending').map((s) => ({ id: s.id, name: s.name, group: s.group,
    provider: s.provider, price_cents: s.price_cents, currency: s.currency, billing_period: s.billing_period, expire_date: s.expire_date })), days)
}
