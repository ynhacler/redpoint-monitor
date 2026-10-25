// 云实例状态的显示（修订第 232 条，需求说明书 CLOUD-01）：按面板归一化的 state_norm 显示中文，原样的 state 放在提示中；
// 状态同步超过 12 小时（两个实例同步周期）标为“可能已过期”，不把旧状态当成当前状态。
import { fmtTime } from './format'
import { t } from './i18n'

/** 归一化状态的名称与色调 */
const names: Record<string, { text: string; tone: '' | 'ok' | 'warn' | 'bad' | 'muted' }> = {
  running: { text: '运行中', tone: 'ok' },
  stopped: { text: '已停止', tone: 'muted' },
  starting: { text: '启动中', tone: 'warn' },
  stopping: { text: '停止中', tone: 'warn' },
  pending: { text: '创建中', tone: 'warn' },
  rebooting: { text: '重启中', tone: 'warn' },
  expired: { text: '已到期 / 停用', tone: 'bad' },
  frozen: { text: '已冻结', tone: 'bad' },
  deleting: { text: '释放中', tone: 'bad' },
  error: { text: '异常', tone: 'bad' },
  unknown: { text: '未知', tone: 'muted' },
}

/** 状态视为可能过期的时长（秒） */
export const STATE_STALE_S = 12 * 3600

/** 一个实例状态的显示：文字、色调、提示（含原样状态与同步时间）、是否可能过期 */
export function cloudState(i: { state: string; state_norm?: string; state_at?: number }, now = Date.now() / 1000) {
  const n = names[i.state_norm ?? ''] ?? (i.state.toLowerCase() === 'running' ? names.running : names.unknown) // 旧版面板没有 state_norm
  const stale = !!i.state_at && now - i.state_at > STATE_STALE_S
  const title = [t('云厂商状态：{s}', { s: i.state || '—' }), i.state_at ? t('同步于 {time}', { time: fmtTime(i.state_at) }) : ''].filter(Boolean).join(' · ')
  return { text: t(n.text), tone: stale ? 'muted' : n.tone, title, stale }
}
