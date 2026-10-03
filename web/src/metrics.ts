// 从节点数据派生展示用的指标与健康判断，总览、列表、详情共用，保证同一节点在各处的结论一致。
import type { AlertBrief, DiskInfo, DiskIO, ServerView } from './api'

/** 环形图配色用的阈值，与告警默认值一致（设计 16.1）；“需要关注”以服务端告警为准（见 issues） */
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

/** 全部磁盘的合计（列表卡片与速览显示的口径）。fullest 为使用率最高的挂载点，用于配色与提示 */
export interface DiskSummary {
  total: number
  used: number
  usage: number
  /** 参与合计的磁盘数（同一设备的多个挂载只算一次） */
  count: number
  fullest: DiskInfo
  /** 合计与最满挂载点中较严重的级别：快满的单个分区不会被合计掩盖（设计 1.5.6） */
  level: 'ok' | 'warn' | 'bad'
}

/**
 * 合计所有挂载点的容量。同一设备（bind mount、btrfs 子卷等）只算一次；没有设备名的旧版 Agent 按挂载点区分。
 * 使用率口径与 df 一致：上报了可用空间时为 used / (used + available)，否则为 used / total。
 */
export function diskSummary(s: ServerView): DiskSummary | undefined {
  const list = s.latest?.disk
  if (!list?.length) return undefined
  const seen = new Set<string>()
  let total = 0
  let used = 0
  let avail = 0
  let hasAvail = true
  for (const d of list) {
    const key = d.device || d.mount
    if (seen.has(key)) continue
    seen.add(key)
    total += d.total
    used += d.used
    if (d.available == null) hasAvail = false
    else avail += d.available
  }
  const denom = hasAvail ? used + avail : total
  const usage = denom ? (used / denom) * 100 : 0
  const fullest = fullestDisk(s)!
  const rank = { ok: 0, warn: 1, bad: 2 }
  const a = gaugeLevel(usage, 'disk')
  const b = gaugeLevel(fullest.usage, 'disk')
  return { total, used, usage, count: seen.size, fullest, level: rank[b] > rank[a] ? b : a }
}

/** 本周期流量使用率；不限流量时为 undefined */
export const trafficPct = (s: ServerView) =>
  s.traffic.limit > 0 ? (s.traffic.used / s.traffic.limit) * 100 : undefined

/** 离线或未知时不展示实时指标：旧数值看起来像实时数据，会误导（设计 43.6） */
export const isLive = (s: ServerView) => !!s.latest && (s.status === 'online' || s.status === 'unknown')

/** 告警类型的简短名称，用于卡片与列表上的标签 */
function alertLabel(a: AlertBrief): string {
  const pct = `${Math.round(a.value)}%`
  switch (a.type) {
    case 'cpu': return `CPU ${pct}`
    case 'memory': return `内存 ${pct}`
    case 'disk': return `磁盘 ${pct}`
    case 'swap': return `Swap ${pct}`
    case 'load': return `负载 ${a.value.toFixed(1)}×`
    case 'traffic': return `流量 ${pct}`
    case 'traffic_forecast': return '流量预计超额'
    case 'agent_clock': return `时钟偏差 ${Math.round(a.value)} 秒`
    default: return a.message
  }
}

/**
 * 需要关注的原因，严重在前；空数组表示正常（设计 9 “需要关注”）。
 * 资源与流量问题来自面板的告警引擎（阈值、持续时间、回差都在服务端，设计 16），Web 与 App 结论一致；
 * 离线、上报延迟按在线状态即时显示，不等离线告警的 120 秒。
 */
export function issues(s: ServerView): { level: 'bad' | 'warn'; text: string }[] {
  const out: { level: 'bad' | 'warn'; text: string }[] = []
  if (s.maintenance) return out // 维护中：不产生告警，也不算需要关注（设计 1.5.15）
  if (s.status === 'offline') out.push({ level: 'bad', text: s.last_seen_at ? '离线' : '尚未上报' })
  if (s.status === 'unknown') out.push({ level: 'warn', text: '上报延迟' })
  for (const a of s.alerts ?? []) {
    if (a.type === 'offline' || a.silenced) continue // 离线已由在线状态表示；已静音的不计入
    out.push({ level: a.severity === 'critical' ? 'bad' : 'warn', text: alertLabel(a) })
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

/** 展示用状态：维护中优先于在线状态（设计 41.3 StatusDot） */
export const displayStatus = (s: ServerView) => (s.maintenance ? 'maintenance' : s.status)

/** 进行中且未静音的告警条数（导航角标） */
export const activeAlertCount = (list: ServerView[]) =>
  list.reduce((n, s) => n + (s.maintenance ? 0 : s.alerts?.filter((a) => !a.silenced).length ?? 0), 0)
