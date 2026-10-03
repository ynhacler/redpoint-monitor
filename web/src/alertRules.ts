// 告警规则的展示：名称、单位、条件摘要（设计 16.1、16.2）。规则页与节点详情共用。
import type { AlertRule, AlertSeverity } from './api'

/** 规则名称（按 rule_key） */
export const ruleNames: Record<string, string> = {
  offline: '节点离线',
  cpu: 'CPU',
  memory: '内存',
  disk: '磁盘',
  disk_critical: '磁盘（严重）',
  swap: 'Swap',
  load: '负载',
  traffic_80: '流量 80%',
  traffic_90: '流量 90%',
  traffic_95: '流量 95%',
  traffic_100: '流量 100%',
  traffic_forecast: '流量预计超额',
  agent_clock: 'Agent 时钟偏差',
}

export const severityNames: Record<AlertSeverity, string> = { critical: '严重', warning: '警告', info: '提示' }

/** 阈值单位：离线与时钟偏差为秒，负载为“倍核数”，其余为百分比 */
export function thresholdUnit(type: string): string {
  if (type === 'offline' || type === 'agent_clock') return '秒'
  if (type === 'load') return '倍核数'
  return '%'
}

/** 输入框的范围与步长，与服务端校验一致（alert_api.go thresholdRange） */
export function thresholdRange(type: string): { min: number; max: number; step: number } {
  switch (type) {
    case 'offline': return { min: 30, max: 86400, step: 10 }
    case 'load': return { min: 0.1, max: 100, step: 0.1 }
    case 'traffic': return { min: 1, max: 200, step: 1 }
    case 'traffic_forecast': return { min: 50, max: 1000, step: 1 }
    case 'agent_clock': return { min: 10, max: 86400, step: 10 }
  }
  return { min: 1, max: 100, step: 1 }
}

function dur(s: number): string {
  if (!s) return ''
  if (s % 3600 === 0) return `${s / 3600} 小时`
  if (s % 60 === 0) return `${s / 60} 分钟`
  return `${s} 秒`
}

/** 条件摘要，如“> 90%，持续 5 分钟；< 80% 持续 2 分钟后恢复” */
export function ruleSummary(r: AlertRule): string {
  const u = thresholdUnit(r.type)
  const v = (x: number) => (u === '%' ? `${x}%` : `${x} ${u}`)
  if (r.type === 'offline') return `超过 ${dur(r.threshold)}未上报`
  let s = `${r.operator === '>=' ? '≥' : '>'} ${v(r.threshold)}`
  if (r.duration_s) s += `，持续 ${dur(r.duration_s)}`
  if (r.type.startsWith('traffic')) return s
  s += `；< ${v(r.recover_threshold)}${r.recover_duration_s ? ` 持续 ${dur(r.recover_duration_s)}后` : ' 即'}恢复`
  return s
}
