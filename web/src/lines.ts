// 网络线路标签（修订第 279 条）：节点上由用户标注的线路，如 CN2 GIA、CMIN2，与 IP 协议栈标志一起显示在仪表板上。
// 自动识别回程线路属于第二阶段（设计 42.5），这里只是用户自己选择或输入的标签。
import type { ServerView } from './api'

/** 新建 / 编辑节点时可直接点选的常见线路，按运营商排列 */
export const linePresets = ['CN2 GIA', 'CN2 GT', '163', '9929', '4837', 'CUII', 'CMIN2', 'CMI', 'IPLC', 'IEPL', 'BGP']

const premium = new Set(['CN2 GIA', '9929', 'CUII', 'CMIN2', 'IPLC', 'IEPL'])
/** 是否为精品线路：标签着强调色，其余为中性色 */
export const isPremiumLine = (tag: string) => premium.has(tag.toUpperCase())

/** 节点上报的 IP 协议栈：v4 / v6 标志；旧版 Agent 没有时为空 */
export function stackBadges(s: ServerView): { text: string; title: string }[] {
  const st = s.latest?.system.ip_stack
  if (!st) return []
  const title = st.ipv4 && st.ipv6 ? '双栈：IPv4 + IPv6' : st.ipv4 ? '仅 IPv4' : st.ipv6 ? '仅 IPv6' : '没有默认路由'
  return [...(st.ipv4 ? [{ text: 'v4', title }] : []), ...(st.ipv6 ? [{ text: 'v6', title }] : [])]
}

/** 搜索用的标志文字：线路标签与 v4 / v6，搜索“cn2”“v6”即可找到对应节点 */
export const badgeWords = (s: ServerView) => [...s.line_tags, ...stackBadges(s).map((b) => b.text)]

/** 仪表板“线路”筛选的选项（修订第 280 条）：节点上出现过的线路标签，加上协议栈三类；值为 line:标签 或 stack:类别 */
export function lineFilterOptions(list: ServerView[]): { v: string; t: string }[] {
  const tags = [...new Set(list.flatMap((s) => s.line_tags))]
    .sort((a, b) => Number(isPremiumLine(b)) - Number(isPremiumLine(a)) || a.localeCompare(b))
  const stacks = new Set(list.map(stackKind).filter(Boolean))
  return [
    ...tags.map((x) => ({ v: `line:${x}`, t: x })),
    ...(['dual', 'v4', 'v6'] as const).filter((k) => stacks.has(k)).map((k) => ({ v: `stack:${k}`, t: stackNames[k] })),
  ]
}

const stackNames = { dual: '双栈：IPv4 + IPv6', v4: '仅 IPv4', v6: '仅 IPv6' }
function stackKind(s: ServerView): keyof typeof stackNames | '' {
  const st = s.latest?.system.ip_stack
  if (!st) return ''
  return st.ipv4 && st.ipv6 ? 'dual' : st.ipv4 ? 'v4' : st.ipv6 ? 'v6' : ''
}

/** 节点是否符合“线路”筛选；空值表示不筛选 */
export function matchLineFilter(s: ServerView, v: string): boolean {
  if (!v) return true
  if (v.startsWith('line:')) return s.line_tags.includes(v.slice(5))
  if (v.startsWith('stack:')) return stackKind(s) === v.slice(6)
  return true
}
