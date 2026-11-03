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
