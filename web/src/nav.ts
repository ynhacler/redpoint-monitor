// Web 导航分组（设计 41.6，修订第 158 条）：顶栏只放 5 个分组，分组内的页面在顶栏下方的二级导航中切换。
// 分组与页面集中写在这里，顶栏、二级导航与路由守卫都用这一份，新增页面时只改这里。
import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { alertsOn } from './store'

/** 二级导航的一项：to 可以带 ?tab=，此时只有地址中的 tab 相同才算当前项 */
export interface NavItem {
  name: string
  to: string
  /** 查看者（只读账号，修订第 161 条）也能打开 */
  viewer?: boolean
}

/** 顶栏的一个分组；items 只有一项时不显示二级导航 */
export interface NavGroup {
  key: string
  name: string
  items: NavItem[]
  /** 属于本分组但不出现在二级导航中的地址前缀（如节点详情、新建节点） */
  extra?: string[]
}

/** 顶栏分组：仪表板、告警、监测、费用、系统 */
export const navGroups: NavGroup[] = [
  { key: 'dashboard', name: '仪表板', items: [{ name: '节点', to: '/', viewer: true }], extra: ['/servers'] },
  {
    key: 'alerts', name: '告警', items: [
      { name: '告警', to: '/alerts', viewer: true },
      { name: '系统事件', to: '/alerts?tab=system', viewer: true },
      { name: '规则', to: '/alerts?tab=rules' },
      { name: '静音', to: '/alerts?tab=silences' },
      { name: '通知', to: '/alerts?tab=notify' },
    ],
  },
  {
    key: 'monitor', name: '监测', items: [
      { name: '测速对比', to: '/latency', viewer: true },
      { name: '测速设置', to: '/probes' },
      { name: '服务', to: '/services' },
    ],
  },
  {
    key: 'costs', name: '费用', items: [
      { name: '费用统计', to: '/costs' },
      { name: '云账户', to: '/cloud' },
    ],
  },
  {
    key: 'system', name: '系统', items: [
      { name: '账号', to: '/account', viewer: true },
      { name: 'App 接入', to: '/app' },
      { name: 'Agent 升级', to: '/upgrades' },
      { name: '日志', to: '/logs' },
    ],
  },
]

const split = (to: string) => {
  const [path, query = ''] = to.split('?')
  return { path, tab: new URLSearchParams(query).get('tab') ?? undefined }
}

/** 某一项是否为当前页面：路径相同；项带 tab 时还要 tab 相同，不带 tab 时当前地址也不能带本分组其他项的 tab */
export function itemActive(item: NavItem, route: RouteLocationNormalizedLoaded, group: NavGroup): boolean {
  const { path, tab } = split(item.to)
  if (route.path !== path) return false
  const cur = typeof route.query.tab === 'string' ? route.query.tab : undefined
  if (tab) return cur === tab
  return !group.items.some((x) => x !== item && split(x.to).path === path && split(x.to).tab === cur)
}

/** 按账号角色筛选：查看者只保留能打开的页面，没有可用页面的分组整个隐藏；告警关闭时不显示告警分组（修订第 175 条） */
export function groupsFor(viewer: boolean): NavGroup[] {
  const groups = alertsOn.value ? navGroups : navGroups.filter((g) => g.key !== 'alerts')
  if (!viewer) return groups
  return groups.map((g) => ({ ...g, items: g.items.filter((x) => x.viewer) })).filter((g) => g.items.length)
}

/** 当前地址所属的分组（没有时为 undefined，如登录页） */
export function activeGroup(route: RouteLocationNormalizedLoaded, groups = navGroups): NavGroup | undefined {
  return groups.find((g) => g.items.some((x) => split(x.to).path === route.path)
    || g.extra?.some((p) => route.path.startsWith(p)))
}
