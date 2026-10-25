// Web 导航分组（设计 41.6，修订第 158、176 条）：顶栏只放 5 个分组（订阅为修订第 189 条，连通性测试为修订第 190 条），分组内的页面在顶栏下方的二级导航中切换。
// 分组与页面集中写在这里，顶栏、二级导航与路由守卫都用这一份，新增页面时只改这里。
import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { alertsOn } from './store'

/** 二级导航的一项：to 可以带 ?tab=，此时只有地址中的 tab 相同才算当前项 */
export interface NavItem {
  name: string
  to: string
  /** 查看者（只读账号，修订第 161 条）也能打开 */
  viewer?: boolean
  /** 手机“系统”菜单（修订第 231 条）：所在小组、图标与一行说明 */
  section?: string
  icon?: 'settings' | 'pulse' | 'bell' | 'user' | 'smartphone' | 'download' | 'logs'
  desc?: string
}

/** 顶栏的一个分组；items 只有一项时不显示二级导航 */
export interface NavGroup {
  key: string
  name: string
  /** 手机底部标签栏的短名称与图标（修订第 199 条） */
  short: string
  icon: 'dashboard' | 'cloud' | 'card' | 'globe' | 'settings'
  items: NavItem[]
  /** 属于本分组但不出现在二级导航中的地址前缀（如节点详情、新建节点） */
  extra?: string[]
  /** 手机底部标签栏的目标（默认第一项）；“系统”为菜单页 /system（修订第 231 条） */
  mobileTo?: string
}

/** 顶栏分组：仪表板、云平台、订阅（修订第 189 条）、系统（修订第 176 条）；告警与监测是“系统”下的页面，测速对比与费用统计不再显示 */
export const navGroups: NavGroup[] = [
  { key: 'dashboard', name: '仪表板', short: '节点', icon: 'dashboard', items: [{ name: '节点', to: '/', viewer: true }], extra: ['/servers'] },
  { key: 'cloud', name: '云平台', short: '云平台', icon: 'cloud', items: [{ name: '云平台', to: '/cloud' }] },
  { key: 'subscriptions', name: '订阅', short: '订阅', icon: 'card', items: [{ name: '订阅', to: '/subscriptions' }] }, // 设计 47
  { key: 'connectivity', name: '连通性测试', short: '连通性', icon: 'globe', items: [{ name: '连通性测试', to: '/connectivity', viewer: true }] }, // 设计 48
  {
    key: 'system', name: '系统', short: '系统', icon: 'settings', mobileTo: '/system', extra: ['/system'], items: [
      // 应用名称、语言、验证码、监听端口（设计 52）
      { name: '网站配置', to: '/site', section: '通用', icon: 'settings', desc: '应用名称、语言、登录验证码、监听端口' },
      { name: '监测', to: '/monitor', section: '监测与告警', icon: 'pulse', desc: '测速目标与频率、国内探针、服务监控' },
      { name: '告警', to: '/alerts', viewer: true, section: '监测与告警', icon: 'bell', desc: '告警记录、规则、静音与通知渠道' },
      { name: '账号', to: '/account', viewer: true, section: '账号与接入', icon: 'user', desc: '密码、两步验证、登录设备与 API Key' },
      { name: 'App 接入', to: '/app', section: '账号与接入', icon: 'smartphone', desc: '配对手机 App、已接入的设备' },
      { name: 'Agent 升级', to: '/upgrades', section: '维护', icon: 'download', desc: '各节点 Agent 版本与升级' },
      { name: '日志', to: '/logs', section: '维护', icon: 'logs', desc: '登录日志与操作日志' },
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

/** 按账号角色筛选：查看者只保留能打开的页面，没有可用页面的分组整个隐藏；告警关闭时不显示告警页（修订第 175 条） */
export function groupsFor(viewer: boolean): NavGroup[] {
  const keep = (x: NavItem) => (viewer ? !!x.viewer : true) && (alertsOn.value || x.to !== '/alerts')
  return navGroups.map((g) => ({ ...g, items: g.items.filter(keep) })).filter((g) => g.items.length)
}

/** 当前地址所属的分组（没有时为 undefined，如登录页） */
export function activeGroup(route: RouteLocationNormalizedLoaded, groups = navGroups): NavGroup | undefined {
  return groups.find((g) => g.items.some((x) => split(x.to).path === route.path)
    || g.extra?.some((p) => route.path.startsWith(p)))
}
