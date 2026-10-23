// 数字、单位与时间的展示格式（设计 41.4.1）。所有页面统一使用这里的函数，保证 Web 与 App 口径一致。
import { t } from './i18n'

/** 未知数据显示 “—”，不显示 0（设计 41.4.1） */
export const DASH = '—'

/** 数值 < 10 保留 1 位小数，≥ 10 取整：1.5、638 */
function num(n: number): string {
  return n < 10 && n % 1 !== 0 ? n.toFixed(1) : Math.round(n).toString()
}

/**
 * 把字节数格式化为可读字符串，如 “1.5 GB”。
 * 使用十进制单位（1 GB = 10^9 Byte），与多数服务商一致（设计 5.8）。
 * @param n 字节数；undefined / null 显示 “—”
 * @param perSec 为 true 时追加 “/s”，用于网速
 */
export function fmtBytes(n: number | null | undefined, perSec = false): string {
  if (n == null || Number.isNaN(n)) return DASH
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  while (n >= 1000 && i < units.length - 1) {
    n /= 1000
    i++
  }
  return `${i === 0 ? Math.round(n) : num(n)} ${units[i]}${perSec ? '/s' : ''}`
}

/** 流量按节点的单位口径显示（设计 5.8）：decimal 用 GB（10⁹），binary 用 GiB（2³⁰） */
export function fmtTraffic(n: number | null | undefined, unit: 'decimal' | 'binary' = 'decimal'): string {
  if (unit !== 'binary') return fmtBytes(n)
  if (n == null || Number.isNaN(n)) return DASH
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${i === 0 ? Math.round(n) : num(n)} ${units[i]}`
}

/** 字节 → GB / GiB 数值（表单回填用） */
export const bytesToGB = (n: number, unit: 'decimal' | 'binary' = 'decimal') => n / (unit === 'binary' ? 2 ** 30 : 1e9)

/**
 * 紧凑字节：“746 K”“2.6 M”“1.1 T”，用于卡片等空间紧张处（十进制，设计 5.8）。
 * 最多一位小数，避免列宽跳动；需要完整单位时用 fmtBytes。
 */
export function fmtBytesShort(n: number | null | undefined, perSec = false): string {
  if (n == null || Number.isNaN(n)) return DASH
  const units = ['B', 'K', 'M', 'G', 'T', 'P']
  let i = 0
  while (n >= 1000 && i < units.length - 1) {
    n /= 1000
    i++
  }
  const v = i === 0 || n >= 100 ? Math.round(n) : Math.round(n * 10) / 10
  return `${v} ${units[i]}${perSec ? '/s' : ''}`
}

/** 百分比取整，小于 1% 显示 “<1%”（设计 41.4.1） */
export function fmtPct(p: number | null | undefined): string {
  if (p == null || Number.isNaN(p)) return DASH
  if (p > 0 && p < 1) return '<1%'
  return `${Math.round(p)}%`
}

/**
 * 时间：今天显示 16:31；本年显示 10-02 16:31；更早显示 2025-10-02（设计 41.4.1）。
 * @param unix Unix 秒；0 或空显示 “—”
 */
/** 可用率（服务监控，设计 33.2）：保留两位小数，不四舍五入到 100%（99.996% 显示 99.99%，只有全部成功才显示 100%） */
export function fmtAvailability(p: number | null | undefined): string {
  if (p == null || Number.isNaN(p)) return DASH
  if (p >= 100) return '100%'
  return `${(Math.floor(p * 100) / 100).toFixed(2)}%`
}

/** 响应耗时：1 秒以内为毫秒（10 ms 以内的小数保留一位，坐标轴刻度不会重复），否则为秒（一位小数） */
export function fmtLatency(ms: number | null | undefined): string {
  if (ms == null || Number.isNaN(ms)) return DASH
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)} s`
  if (ms < 10 && !Number.isInteger(ms)) return `${(Math.round(ms * 10) / 10)} ms`
  return `${Math.round(ms)} ms`
}

/** 时间：今天只显示时分，本年显示月日时分，更早显示年月日（设计 41.4.1） */
export function fmtTime(unix: number | null | undefined, now = new Date()): string {
  if (!unix) return DASH
  const d = new Date(unix * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  const hm = `${p(d.getHours())}:${p(d.getMinutes())}`
  if (d.toDateString() === now.toDateString()) return hm
  if (d.getFullYear() === now.getFullYear()) return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${hm}`
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/** 完整时间到秒：2026-10-02 16:31:05，用于日志等需要精确时间的地方 */
export function fmtDateTime(unix: number | null | undefined): string {
  if (!unix) return DASH
  const d = new Date(unix * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/** 时长：“4 分钟”“2 小时”“3 天”，用于离线时长与运行时间（设计 41.4.1） */
/** 日期：YYYY-MM-DD（本地时区），用于到期日等（设计 41.4.1） */
export function fmtDate(unix: number | null | undefined): string {
  if (!unix) return DASH
  const d = new Date(unix * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/** 时长：如“4 分钟”“2 小时”“3 天” */
export function fmtDuration(seconds: number | null | undefined): string {
  if (seconds == null || seconds < 0) return DASH
  if (seconds < 60) return t('{n} 秒', { n: Math.floor(seconds) })
  if (seconds < 3600) return t('{n} 分钟', { n: Math.floor(seconds / 60) })
  if (seconds < 86400) return t('{n} 小时', { n: Math.floor(seconds / 3600) })
  return t('{n} 天', { n: Math.floor(seconds / 86400) })
}

/**
 * 开机时间（紧凑）：不足 1 天按小时（“5小时”，不足 1 小时为“<1小时”）；1～999 天按天（“10天”）；
 * 超过 999 天按年，保留一位小数（“2.8年”），避免列表中出现 4 位天数。
 */
export function fmtUptimeShort(seconds: number | null | undefined): string {
  if (seconds == null || seconds < 0) return DASH
  const days = seconds / 86400
  if (days < 1) {
    const h = Math.floor(seconds / 3600)
    return h < 1 ? t('<1小时') : t('{n}小时', { n: h })
  }
  if (days <= 999) return t('{n}天', { n: Math.floor(days) })
  return t('{n}年', { n: (days / 365).toFixed(1) })
}

/** 开机时间拆成数值与单位（规则同 fmtUptimeShort），用于“数值大、单位小”的展示 */
export function uptimeParts(seconds: number | null | undefined): { v: string; u: string } {
  if (seconds == null || seconds < 0) return { v: DASH, u: '' }
  const days = seconds / 86400
  if (days < 1) {
    const h = Math.floor(seconds / 3600)
    return h < 1 ? { v: '<1', u: t('小时') } : { v: String(h), u: t('小时') }
  }
  if (days <= 999) return { v: String(Math.floor(days)), u: t('天') }
  return { v: (days / 365).toFixed(1), u: t('年') }
}

/** 运行时间：取两级单位，“37 天 7 小时”“5 小时 12 分”“12 分”，用于详情页（列表用 fmtDuration） */
export function fmtUptime(seconds: number | null | undefined): string {
  if (seconds == null || seconds < 0) return DASH
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return h ? t('{d} 天 {h} 小时', { d, h }) : t('{n} 天', { n: d })
  if (h > 0) return m ? t('{h} 小时 {m} 分', { h, m }) : t('{n} 小时', { n: h })
  return m > 0 ? t('{n} 分', { n: m }) : t('{n} 秒', { n: Math.floor(seconds) })
}

/** 带宽：Mbps 数值，≥1000 时换算为 Gbps（“1 Gbps”“200 Mbps”）；0 表示未填 */
export function fmtBandwidth(mbps: number | null | undefined): string {
  if (!mbps) return DASH
  return mbps >= 1000 ? `${Math.round((mbps / 1000) * 10) / 10} Gbps` : `${mbps} Mbps`
}

/** 金额：按币种显示，0 也显示（费用、余额，设计 44）；null 显示 “—” */
export function fmtMoney(cents: number | null | undefined, currency: string): string {
  if (cents == null) return DASH
  return `${currency} ${(cents / 100).toFixed(2)}`
}

/** 价格：按币种显示，如 “USD 5.99”；未填显示 “—” */
export function fmtPrice(cents: number, currency: string): string {
  if (!cents) return DASH
  return `${currency} ${(cents / 100).toFixed(2)}`
}

/** 续费周期的中文名（设计 1.2.5） */
export const periodNames: Record<string, string> = {
  monthly: '月付', quarterly: '季付', semiannually: '半年付', annually: '年付',
  biennially: '两年付', triennially: '三年付', one_time: '一次性',
}

/** 计费模式的中文名（设计 1.2.4） */
export const countModeNames: Record<string, string> = {
  sum: '入 + 出', max: '入、出取较大值', tx: '仅出站', rx: '仅入站',
}

/** User-Agent 粗略识别为“浏览器 · 系统”（日志与登录会话共用）；识别不出时返回前 40 个字符 */
export function uaSummary(ua: string): string {
  if (!ua) return ''
  const b = /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox'
    : /Safari\//.test(ua) ? 'Safari' : /curl|Go-http|vpsmon/i.test(ua) ? ua.split(/[ /]/)[0] : ''
  const o = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS'
    : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  return [b, o].filter(Boolean).join(' · ') || ua.slice(0, 40)
}

/**
 * 节点到期的简短显示（卡片右上角，修订第 138 条）：已过期 / 剩 N 天（100 天内）/ 到期日；
 * 一次性付款且没有到期日为“永久”；没有填写到期日时返回 null（不显示）。
 * level：已过期或 7 天内为 bad，30 天内为 warn。
 */
export function fmtExpiry(expireDate: string, billingPeriod: string, now = new Date()): { text: string; title: string; level: string } | null {
  if (!expireDate) {
    return billingPeriod === 'one_time' ? { text: t('永久'), title: t('一次性付款，没有到期日'), level: '' } : null
  }
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const days = Math.round((new Date(expireDate + 'T00:00:00').getTime() - today) / 86400000)
  if (Number.isNaN(days)) return null
  const title = t('到期日 {date}', { date: expireDate })
  if (days < 0) return { text: t('已过期'), title: `${title}，${t('已过期 {n} 天', { n: -days })}`, level: 'bad' }
  const level = days <= 7 ? 'bad' : days <= 30 ? 'warn' : ''
  if (days === 0) return { text: t('今天到期'), title, level }
  return { text: days < 100 ? t('剩{n}天', { n: days }) : expireDate, title, level }
}
