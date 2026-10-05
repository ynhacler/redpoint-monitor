// 费用统计（设计 36.3）：按节点的续费价格与周期，按币种汇总月均 / 年均开销、按供应商或分组拆分、近期续费。
// 只用节点资产信息（设计 1.2.3），在浏览器中计算；不同币种不换算（没有汇率来源，也不联网获取）。

/** 计费周期折合的月数；一次性付款不计入月均 / 年均 */
export const periodMonths: Record<string, number> = {
  monthly: 1, quarterly: 3, semiannually: 6, annually: 12, biennially: 24, triennially: 36,
}

export interface CostNode {
  id: number
  name: string
  group: string
  provider: string
  price_cents: number
  currency: string
  billing_period: string
  expire_date: string
}

export interface CurrencyTotal {
  currency: string
  /** 月均（分），按周期折算后求和 */
  monthly: number
  /** 年均（分）= 月均 × 12 */
  yearly: number
  /** 计入月均的节点数 */
  count: number
  /** 一次性付款合计（分） */
  oneTime: number
}

/** 单个节点折合的月均（分）；未填价格、未填周期或一次性付款时为 null */
export function monthlyCents(n: CostNode): number | null {
  const m = periodMonths[n.billing_period]
  if (!n.price_cents || !m) return null
  return n.price_cents / m
}

/** 按币种汇总，币种按年均从高到低 */
export function totalsByCurrency(nodes: CostNode[]): CurrencyTotal[] {
  const by = new Map<string, CurrencyTotal>()
  for (const n of nodes) {
    if (!n.price_cents) continue
    const cur = n.currency || '—'
    const t = by.get(cur) ?? { currency: cur, monthly: 0, yearly: 0, count: 0, oneTime: 0 }
    const m = monthlyCents(n)
    if (m != null) {
      t.monthly += m
      t.count++
    } else if (n.billing_period === 'one_time') {
      t.oneTime += n.price_cents
    }
    t.yearly = t.monthly * 12
    by.set(cur, t)
  }
  return [...by.values()].sort((a, b) => b.yearly - a.yearly || a.currency.localeCompare(b.currency))
}

export interface Breakdown {
  key: string
  currency: string
  monthly: number
  count: number
}

/** 按供应商或分组拆分月均（同一币种内比较），月均从高到低；空值归为“未填写” */
export function breakdown(nodes: CostNode[], by: 'provider' | 'group'): Breakdown[] {
  const m = new Map<string, Breakdown>()
  for (const n of nodes) {
    const v = monthlyCents(n)
    if (v == null) continue
    const key = (by === 'provider' ? n.provider : n.group) || '未填写'
    const id = `${n.currency}\u0000${key}`
    const b = m.get(id) ?? { key, currency: n.currency || '—', monthly: 0, count: 0 }
    b.monthly += v
    b.count++
    m.set(id, b)
  }
  return [...m.values()].sort((a, b) => a.currency.localeCompare(b.currency) || b.monthly - a.monthly)
}

export interface Renewal {
  node: CostNode
  days: number
}

/** days 天内（含已过期 30 天内）到期的节点，按到期日升序 */
export function upcomingRenewals(nodes: CostNode[], days: number, now = new Date()): Renewal[] {
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const out: Renewal[] = []
  for (const n of nodes) {
    if (!n.expire_date) continue
    const d = Math.round((new Date(n.expire_date + 'T00:00:00').getTime() - today) / 86400000)
    if (d <= days && d >= -30) out.push({ node: n, days: d })
  }
  return out.sort((a, b) => a.days - b.days)
}
