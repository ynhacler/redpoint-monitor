// 从节点数据派生展示用的指标与健康判断，总览、列表、详情共用，保证同一节点在各处的结论一致。
import type { DiskInfo, DiskIO, ServerView } from './api'

/** 关注阈值：与告警默认值一致（设计 16.1），告警引擎完成后改为读取规则（TODO(A5)） */
export const thresholds = { cpu: 90, mem: 90, disk: 85, diskBad: 95, traffic: 80, trafficBad: 95 }

/**
 * 环形图配色（设计 41.2）：低于 warn 为 ok（绿），达到 warn 为 warn（橙），达到 bad 为 bad（红）。
 * bad 与“需要关注”的阈值一致；warn 提前一档，让接近阈值的节点在列表里一眼可见。
 */
export const gaugeThresholds = {
  cpu: { warn: 70, bad: thresholds.cpu },
  mem: { warn: 75, bad: thresholds.mem },
  disk: { warn: thresholds.disk, bad: thresholds.diskBad },
} as const

export function gaugeLevel(v: number | undefined, kind: keyof typeof gaugeThresholds): 'ok' | 'warn' | 'bad' {
  const t = gaugeThresholds[kind]
  if (v == null) return 'ok'
  return v >= t.bad ? 'bad' : v >= t.warn ? 'warn' : 'ok'
}

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

/** 本周期还剩几天重置（设计 1.5.8）：按面板返回的下一周期开始日计算，与服务端口径一致 */
export function daysToReset(s: ServerView, now = new Date()): number {
  const end = new Date(s.traffic.cycle_end + 'T00:00:00')
  return Math.max(0, Math.ceil((end.getTime() - now.getTime()) / 86400000))
}

/** 磁盘 IO 合计（读、写速率之和），用于卡片 */
export function ioTotal(s: ServerView): { read: number; write: number } | undefined {
  const io = s.latest?.disk_io
  if (!io?.length) return undefined
  return io.reduce((a, d) => ({ read: a.read + d.read_speed, write: a.write + d.write_speed }), { read: 0, write: 0 })
}

/**
 * 挂载点所在磁盘的 IO：/dev/vda1 → vda，/dev/nvme0n1p2 → nvme0n1，/dev/mapper/… 等找不到时返回 undefined。
 * 取设备名前缀最长的一块，避免 sda 误匹配 sdaa1。
 */
export function ioForDisk(d: DiskInfo, io: DiskIO[] | undefined): DiskIO | undefined {
  const name = d.device?.replace(/^\/dev\//, '')
  if (!name || !io) return undefined
  return io.filter((x) => name.startsWith(x.device)).sort((a, b) => b.device.length - a.device.length)[0]
}

/** 详情页的实时采样点（约 3 秒一个），用于迷你折线与实时网速图；null 表示该时刻没有数据 */
export interface LiveSample {
  /** Unix 秒 */
  ts: number
  cpu: number | null
  mem: number | null
  rx: number | null
  tx: number | null
  tcp?: number
}
