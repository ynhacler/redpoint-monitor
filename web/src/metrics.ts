// 从节点数据派生展示用的指标与健康判断，总览、列表、详情共用，保证同一节点在各处的结论一致。
import type { DiskInfo, ServerView } from './api'

/** 关注阈值：与告警默认值一致（设计 16.1），告警引擎完成后改为读取规则（TODO(A5)） */
export const thresholds = { cpu: 90, mem: 90, disk: 85, diskBad: 95, traffic: 80, trafficBad: 95 }

export const cpu = (s: ServerView) => s.latest?.cpu.usage
export const mem = (s: ServerView) => s.latest?.memory.usage
export const rx = (s: ServerView) => s.latest?.network.reduce((a, n) => a + n.rx_speed, 0)
export const tx = (s: ServerView) => s.latest?.network.reduce((a, n) => a + n.tx_speed, 0)

/** 使用率最高的挂载点：异常优先，快满的数据盘不能被根分区掩盖（设计 1.5.6） */
export const fullestDisk = (s: ServerView): DiskInfo | undefined =>
  s.latest?.disk.reduce<DiskInfo | undefined>((a, d) => (!a || d.usage > a.usage ? d : a), undefined)

/** 本周期流量使用率；不限流量时为 undefined */
export const trafficPct = (s: ServerView) =>
  s.traffic.limit > 0 ? (s.traffic.used / s.traffic.limit) * 100 : undefined

/** 离线或未知时不展示实时指标：旧数值看起来像实时数据，会误导（设计 43.6） */
export const isLive = (s: ServerView) => !!s.latest && (s.status === 'online' || s.status === 'unknown')

/** 需要关注的原因，按严重程度排序；空数组表示正常（设计 9 “需要关注”） */
export function issues(s: ServerView): { level: 'bad' | 'warn'; text: string }[] {
  const out: { level: 'bad' | 'warn'; text: string }[] = []
  if (s.status === 'offline') out.push({ level: 'bad', text: s.last_seen_at ? '离线' : '尚未上报' })
  if (s.status === 'unknown') out.push({ level: 'warn', text: '上报延迟' })
  if (isLive(s)) {
    const c = cpu(s) ?? 0
    const m = mem(s) ?? 0
    const d = fullestDisk(s)
    if (c >= thresholds.cpu) out.push({ level: 'warn', text: `CPU ${Math.round(c)}%` })
    if (m >= thresholds.mem) out.push({ level: 'warn', text: `内存 ${Math.round(m)}%` })
    if (d && d.usage >= thresholds.disk) {
      out.push({ level: d.usage >= thresholds.diskBad ? 'bad' : 'warn', text: `磁盘 ${d.mount} ${Math.round(d.usage)}%` })
    }
  }
  const t = trafficPct(s)
  if (t != null && t >= thresholds.traffic) {
    out.push({ level: t >= thresholds.trafficBad ? 'bad' : 'warn', text: `流量 ${Math.round(t)}%` })
  }
  return out.sort((a, b) => (a.level === b.level ? 0 : a.level === 'bad' ? -1 : 1))
}

/** 本周期还剩几天重置（设计 1.5.8）。cycle_start 为 YYYY-MM-DD，重置日按每月同一天计算 */
export function daysToReset(s: ServerView, now = new Date()): number {
  const day = s.traffic_reset_day || 1
  const next = new Date(now.getFullYear(), now.getMonth(), day)
  if (next <= now) next.setMonth(next.getMonth() + 1)
  // 重置日大于当月天数时（如 31 日），落到当月最后一天
  if (next.getDate() !== day) next.setDate(0)
  return Math.max(0, Math.ceil((next.getTime() - now.getTime()) / 86400000))
}
