// 数字、单位与时间的展示格式（设计 41.4.1）。所有页面统一使用这里的函数，保证 Web 与 App 口径一致。

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
export function fmtTime(unix: number | null | undefined, now = new Date()): string {
  if (!unix) return DASH
  const d = new Date(unix * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  const hm = `${p(d.getHours())}:${p(d.getMinutes())}`
  if (d.toDateString() === now.toDateString()) return hm
  if (d.getFullYear() === now.getFullYear()) return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${hm}`
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/** 时长：“4 分钟”“2 小时”“3 天”，用于离线时长与运行时间（设计 41.4.1） */
export function fmtDuration(seconds: number | null | undefined): string {
  if (seconds == null || seconds < 0) return DASH
  if (seconds < 60) return `${Math.floor(seconds)} 秒`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时`
  return `${Math.floor(seconds / 86400)} 天`
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
