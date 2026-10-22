// 订阅（设计 47，修订第 189 条）：常用服务目录、类别、扣费日计算与统计。全部是纯函数，界面只负责展示。
// 扣费日规则与面板（internal/server/subscriptions.go）一致：首次扣费 = 开始日期 + 试用天数，之后按周期递推，
// 按月类周期以首次扣费日为锚点、小月取月末；结束日期当天及之后不再扣费；一次性只扣首次。
// 图标见 components/SubAvatar.vue：内置的标志（brandIcons.ts），没有标志的用品牌色方块加首字母；不从外部加载。
import type { Subscription } from './api'

/** 订阅类别 */
export type SubCategory = Subscription['category']
/** 订阅周期 */
export type SubCycle = Subscription['cycle']

/** 类别名称 */
export const categoryNames: Record<SubCategory, string> = {
  video: '影音娱乐', tools: '效率&工具', reading: '阅读&学习', social: '社交', health: '健康&运动', game: '游戏', cloud: '云与开发', other: '其他',
}
/** 类别在图表中的颜色（设计令牌中的系列色与状态色） */
export const categoryColors: Record<SubCategory, string> = {
  video: 'var(--accent)', tools: 'var(--ok)', reading: 'color-mix(in srgb, var(--ok) 50%, var(--series-3))', social: 'var(--series-2)',
  health: 'var(--warn)', game: 'var(--bad)', cloud: 'var(--series-3)', other: 'var(--muted-state)',
}
/** 周期名称 */
export const cycleNames: Record<SubCycle, string> = {
  weekly: '每周', monthly: '每月', quarterly: '每季度', semiannually: '每半年', annually: '每年', once: '一次性',
}
/** 免费试用选项（天） */
export const trialOptions = [{ v: 0, t: '无试用期' }, { v: 7, t: '7 天' }, { v: 14, t: '14 天' }, { v: 30, t: '1 个月' }, { v: 90, t: '3 个月' }]
/** 扣费提醒选项（提前天数） */
export const remindOptions = [{ v: 0, t: '不提醒' }, { v: 1, t: '提前 1 天' }, { v: 2, t: '提前 2 天' }, { v: 3, t: '提前 3 天' }, { v: 7, t: '提前 7 天' }]
/** 可选币种 */
export const currencies = ['CNY', 'USD', 'EUR', 'HKD', 'JPY', 'GBP', 'TWD', 'SGD']

/** 常用服务目录中的一项 */
export interface CatalogItem {
  key: string
  name: string
  category: SubCategory
  color: string
  hot?: boolean
  currency?: string
}

/** 常用服务目录：名称与品牌色仅用于识别；有标志的见 brandIcons.ts */
export const catalog: CatalogItem[] = [
  { key: 'netflix', name: 'Netflix', category: 'video', color: '#E50914', hot: true, currency: 'USD' },
  { key: 'spotify', name: 'Spotify', category: 'video', color: '#1DB954', hot: true, currency: 'USD' },
  { key: 'youtube', name: 'YouTube Premium', category: 'video', color: '#FF0000', hot: true, currency: 'USD' },
  { key: 'disney', name: 'Disney+', category: 'video', color: '#113CCF', hot: true, currency: 'USD' },
  { key: 'hbomax', name: 'HBO Max', category: 'video', color: '#5822B4', currency: 'USD' },
  { key: 'appletv', name: 'Apple TV+', category: 'video', color: '#111111', currency: 'USD' },
  { key: 'applemusic', name: 'Apple Music', category: 'video', color: '#FA2D48', hot: true },
  { key: 'prime', name: 'Amazon Prime', category: 'video', color: '#00A8E1', currency: 'USD' },
  { key: 'bilibili', name: 'bilibili 大会员', category: 'video', color: '#FB7299', hot: true },
  { key: 'iqiyi', name: '爱奇艺', category: 'video', color: '#00BE06' },
  { key: 'tencentvideo', name: '腾讯视频', category: 'video', color: '#FF6A00' },
  { key: 'youku', name: '优酷', category: 'video', color: '#1E9FFF' },
  { key: 'mgtv', name: '芒果 TV', category: 'video', color: '#FF7F00' },
  { key: 'qqmusic', name: 'QQ 音乐', category: 'video', color: '#31C27C' },
  { key: 'neteasemusic', name: '网易云音乐', category: 'video', color: '#E60026' },
  { key: 'chatgpt', name: 'ChatGPT Plus', category: 'tools', color: '#10A37F', hot: true, currency: 'USD' },
  { key: 'claude', name: 'Claude Pro', category: 'tools', color: '#D97757', hot: true, currency: 'USD' },
  { key: 'copilot', name: 'GitHub Copilot', category: 'tools', color: '#24292F', currency: 'USD' },
  { key: 'm365', name: 'Microsoft 365', category: 'tools', color: '#D83B01', hot: true },
  { key: 'notion', name: 'Notion', category: 'tools', color: '#191919', currency: 'USD' },
  { key: '1password', name: '1Password', category: 'tools', color: '#0572EC', currency: 'USD' },
  { key: 'bitwarden', name: 'Bitwarden', category: 'tools', color: '#175DDC', currency: 'USD' },
  { key: 'adobe', name: 'Adobe Creative Cloud', category: 'tools', color: '#DA1F26' },
  { key: 'figma', name: 'Figma', category: 'tools', color: '#A259FF', currency: 'USD' },
  { key: 'linear', name: 'Linear', category: 'tools', color: '#5E6AD2', currency: 'USD' },
  { key: 'jetbrains', name: 'JetBrains', category: 'tools', color: '#000000', currency: 'USD' },
  { key: 'adguard', name: 'AdGuard', category: 'tools', color: '#68BC71' },
  { key: 'icloud', name: 'iCloud+', category: 'cloud', color: '#3693F3', hot: true },
  { key: 'googleone', name: 'Google One', category: 'cloud', color: '#4285F4', currency: 'USD' },
  { key: 'dropbox', name: 'Dropbox', category: 'cloud', color: '#0061FF', currency: 'USD' },
  { key: 'baidupan', name: '百度网盘', category: 'cloud', color: '#06A7FF' },
  { key: 'cloudflare', name: 'Cloudflare', category: 'cloud', color: '#F38020', currency: 'USD' },
  { key: 'github', name: 'GitHub', category: 'cloud', color: '#24292F', currency: 'USD' },
  { key: 'domain', name: '域名续费', category: 'cloud', color: '#2563EB' },
  { key: 'wechatread', name: '微信读书', category: 'reading', color: '#1F8CFF' },
  { key: 'kindle', name: 'Kindle Unlimited', category: 'reading', color: '#FF9900' },
  { key: 'duolingo', name: 'Duolingo', category: 'reading', color: '#58CC02', currency: 'USD' },
  { key: 'medium', name: 'Medium', category: 'reading', color: '#000000', currency: 'USD' },
  { key: 'xpremium', name: 'X Premium', category: 'social', color: '#000000', currency: 'USD' },
  { key: 'telegram', name: 'Telegram Premium', category: 'social', color: '#26A5E4', currency: 'USD' },
  { key: 'discord', name: 'Discord Nitro', category: 'social', color: '#5865F2', currency: 'USD' },
  { key: 'keep', name: 'Keep 会员', category: 'health', color: '#24C789' },
  { key: 'fitness', name: 'Apple Fitness+', category: 'health', color: '#A6FF00' },
  { key: 'switch', name: 'Nintendo Switch Online', category: 'game', color: '#E60012' },
  { key: 'psplus', name: 'PlayStation Plus', category: 'game', color: '#0070D1' },
  { key: 'gamepass', name: 'Xbox Game Pass', category: 'game', color: '#107C10' },
]
const byKey = new Map(catalog.map((c) => [c.key, c]))

/** 图标：目录中的品牌色与首字母；自定义的按名称取一个稳定的颜色 */
export function avatarOf(s: { brand?: string; name: string }): { color: string; letter: string } {
  const item = s.brand ? byKey.get(s.brand) : undefined
  const name = (item?.name ?? s.name).trim()
  const letter = (name.match(/[A-Za-z0-9一-鿿]/)?.[0] ?? '?').toUpperCase()
  if (item) return { color: item.color, letter }
  const palette = ['#2563EB', '#16A34A', '#7C3AED', '#0891B2', '#D97706', '#DB2777', '#4B5563']
  let h = 0
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return { color: palette[h % palette.length], letter }
}

// ---- 扣费日 ----

const parse = (d: string) => {
  const [y, m, day] = d.split('-').map(Number)
  return new Date(y, m - 1, day)
}
/** 本地日期格式化为 YYYY-MM-DD */
export const ymd = (t: Date) => `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, '0')}-${String(t.getDate()).padStart(2, '0')}`
const months: Partial<Record<SubCycle, number>> = { monthly: 1, quarterly: 3, semiannually: 6, annually: 12 }

/** 锚点日期加 n 个月，目标月没有这一天时取月末 */
function addMonthsClamped(anchor: Date, n: number): Date {
  const first = new Date(anchor.getFullYear(), anchor.getMonth() + n, 1)
  const last = new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate()
  return new Date(first.getFullYear(), first.getMonth(), Math.min(anchor.getDate(), last))
}

/** 区间 [from, to)（本地日期）内的全部扣费日 */
export function chargesBetween(s: Subscription, from: Date, to: Date): Date[] {
  const first = parse(s.start_date)
  first.setDate(first.getDate() + s.trial_days)
  const end = s.end_date ? parse(s.end_date) : null
  const out: Date[] = []
  for (let k = 0; k < 10000; k++) {
    let t: Date
    if (s.cycle === 'once') {
      if (k > 0) break
      t = first
    } else if (s.cycle === 'weekly') {
      t = new Date(first.getFullYear(), first.getMonth(), first.getDate() + 7 * k)
    } else {
      t = addMonthsClamped(first, k * (months[s.cycle] ?? 1))
    }
    if (t >= to || (end && t >= end)) break
    if (t >= from) out.push(t)
  }
  return out
}

/** 按扣费周期折算为每月花费（分）：每周按 52 / 12，一次性为 0 */
export function monthlyCents(s: Subscription): number {
  if (s.cycle === 'once') return 0
  if (s.cycle === 'weekly') return (s.price_cents * 52) / 12
  return s.price_cents / (months[s.cycle] ?? 1)
}

/** 扣费说明：“每月 5 日扣费”“每年 10 月 26 日扣费”“每周三扣费”“已结束订阅”“试用中，11 月 8 日开始扣费” */
export function chargeText(s: Subscription, today = new Date()): string {
  if (s.ended) return '已结束订阅'
  const first = parse(s.start_date)
  first.setDate(first.getDate() + s.trial_days)
  if (s.trial_days > 0 && first > today) return `试用中，${first.getMonth() + 1} 月 ${first.getDate()} 日开始扣费`
  if (s.cycle === 'once') return s.next_charge ? `${s.next_charge.slice(5).replace('-', ' 月 ')} 日扣费` : '一次性，已扣费'
  if (s.cycle === 'weekly') return `每周${'日一二三四五六'[first.getDay()]}扣费`
  if (s.cycle === 'monthly') return `每月 ${first.getDate()} 日扣费`
  if (s.cycle === 'annually') return `每年 ${first.getMonth() + 1} 月 ${first.getDate()} 日扣费`
  const next = s.next_charge ? parse(s.next_charge) : null
  return next ? `${cycleNames[s.cycle]}，下次 ${next.getMonth() + 1} 月 ${next.getDate()} 日` : cycleNames[s.cycle]
}

// ---- 统计 ----

/** 按币种的金额（分） */
export type Money = Map<string, number>
const add = (m: Money, cur: string, v: number) => m.set(cur, (m.get(cur) ?? 0) + v)

/** 区间内的实际扣费合计（按币种） */
export function spendBetween(list: Subscription[], from: Date, to: Date): Money {
  const m: Money = new Map()
  for (const s of list) {
    const n = chargesBetween(s, from, to).length
    if (n) add(m, s.currency, n * s.price_cents)
  }
  return m
}

/** t 所在月（加 offset 个月）的 1 日 */
export const monthStart = (t: Date, offset = 0) => new Date(t.getFullYear(), t.getMonth() + offset, 1)

/** 主币种：金额最大的币种（没有时为 CNY） */
export function mainCurrency(list: Subscription[]): string {
  const m: Money = new Map()
  for (const s of list) add(m, s.currency, monthlyCents(s))
  let best = 'CNY'
  let v = -1
  for (const [c, x] of m) if (x > v) [best, v] = [c, x]
  return best
}

/** 一个类别的统计 */
export interface CategoryStat {
  category: SubCategory
  cents: number
  count: number
  pct: number
}

/** 区间内某币种按类别的花费，金额大的在前 */
export function byCategory(list: Subscription[], currency: string, from: Date, to: Date): CategoryStat[] {
  const m = new Map<SubCategory, { cents: number; count: number }>()
  for (const s of list) {
    if (s.currency !== currency) continue
    const n = chargesBetween(s, from, to).length
    if (!n) continue
    const x = m.get(s.category) ?? { cents: 0, count: 0 }
    x.cents += n * s.price_cents
    x.count++
    m.set(s.category, x)
  }
  const total = [...m.values()].reduce((a, x) => a + x.cents, 0)
  return [...m.entries()].map(([category, x]) => ({ category, ...x, pct: total ? (x.cents / total) * 100 : 0 }))
    .sort((a, b) => b.cents - a.cents)
}
