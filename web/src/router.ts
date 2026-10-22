// 页面路由（设计 3.3、8.1、10、11）。地址可刷新、可分享；服务端对未知路径返回 index.html（SPA 回退）。
// 未登录访问任何页面都先到 /login，登录后回到原页面；使用初始密码时只能停留在 /account（设计 17.4）。
// 页面按 nav.ts 的分组组织（修订第 158、176 条）；旧地址（/settings、/probes、/services、/costs、/latency）照常跳转。
import { ref } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import { alertsOn, checkAuth, isViewer, setSignedOutHandler, startPolling, state } from './store'

// 原“设置”页各标签的新地址（修订第 158 条）；修订第 126 条前这些标签在告警页，/alerts?tab=… 同样转过去
const settingsMoved: Record<string, string> = { probes: '/monitor', ssl: '/ssl', services: '/monitor', account: '/account' } // /ssl 再转到告警规则

/** 页面路由 */
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('./pages/LoginPage.vue'), meta: { title: '登录', public: true } },
    { path: '/account', name: 'account', component: () => import('./pages/AccountPage.vue'), meta: { viewer: true, title: '账号' } },
    { path: '/alerts', name: 'alerts', component: () => import('./pages/AlertsPage.vue'), meta: { viewer: true, title: '告警' } },
    // 测速设置与服务合并为“监测”（修订第 176 条）
    { path: '/monitor', name: 'monitor', component: () => import('./pages/MonitorSettingsPage.vue'), meta: { title: '监测' } },
    { path: '/probes', redirect: (to) => ({ path: '/monitor', hash: to.hash }) },
    // 证书到期并入“告警 → 规则”（修订第 173 条）：旧地址照常可用
    { path: '/ssl', redirect: { path: '/alerts', query: { tab: 'rules' }, hash: '#ssl' } },
    { path: '/services', redirect: { path: '/monitor', hash: '#services' } },
    // 旧“设置”页：按标签转到新位置，保留 #锚点（如国内探针 #reach）
    { path: '/settings', redirect: (to) => ({ path: settingsMoved[to.query.tab as string] ?? '/monitor', query: {}, hash: to.hash }) },
    { path: '/upgrades', name: 'upgrades', component: () => import('./pages/UpgradesPage.vue'), meta: { title: 'Agent 升级' } },
    { path: '/app', name: 'app', component: () => import('./pages/AppAccessPage.vue'), meta: { title: 'App 接入' } },
    // “费用”只保留云平台一页，测速对比不再显示（修订第 176 条）：旧地址跳转
    { path: '/costs', redirect: '/cloud' },
    { path: '/latency', redirect: '/' },
    { path: '/subscriptions', name: 'subscriptions', component: () => import('./pages/SubscriptionsPage.vue'), meta: { title: '订阅' } },
    { path: '/cloud', name: 'cloud', component: () => import('./pages/CloudPage.vue'), meta: { title: '云平台' } },
    { path: '/logs', name: 'logs', component: () => import('./pages/LogsPage.vue'), meta: { title: '日志' } },
    // 总览与节点列表合并为首页（修订记录第 30 条）；旧地址 /servers 保留跳转，书签不失效
    { path: '/', name: 'servers', component: () => import('./pages/ServersPage.vue'), meta: { viewer: true, title: '仪表板' } },
    { path: '/servers', redirect: (to) => ({ path: '/', query: to.query }) },
    { path: '/servers/batch', name: 'server-batch', component: () => import('./pages/BatchCreatePage.vue'), meta: { title: '批量新建' } },
    { path: '/servers/new', name: 'server-new', component: () => import('./pages/ServerEditPage.vue'), meta: { title: '新建节点' } },
    { path: '/servers/:id(\\d+)', name: 'server', component: () => import('./pages/ServerDetailPage.vue'), props: true, meta: { viewer: true, title: '节点详情' } },
    { path: '/servers/:id(\\d+)/edit', name: 'server-edit', component: () => import('./pages/ServerEditPage.vue'), props: true, meta: { title: '编辑节点' } },
    { path: '/servers/:id(\\d+)/install', name: 'server-install', component: () => import('./pages/InstallPage.vue'), props: true, meta: { title: '安装命令' } },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  // 地址带 #锚点 时滚动到该元素：页面内容多为异步加载，最多等 2 秒；否则回到顶部
  scrollBehavior: (to, from, saved) => {
    // 前进 / 后退回到原来的位置；同一页面只改查询参数（切换标签、筛选）时不滚动（修订第 180 条）
    if (saved) return saved
    if (!to.hash) return to.path === from.path ? false : { top: 0 }
    return new Promise((resolve) => {
      let tries = 0
      const tick = () => {
        // 顶栏（含二级导航）是吸顶的：让出它的高度，目标不被挡住
        if (document.querySelector(to.hash)) resolve({ el: to.hash, top: (document.querySelector('.topbar')?.clientHeight ?? 0) + 16 })
        else if (tries++ >= 40) resolve({ top: 0 })
        else setTimeout(tick, 50)
      }
      tick()
    })
  },
})

// 会话失效（任意请求返回 401）或退出时回到登录页，并记住原页面
setSignedOutHandler(() => {
  const cur = router.currentRoute.value
  if (cur.name !== 'login') router.push({ name: 'login', query: { redirect: cur.fullPath } })
})


router.beforeEach(async (to) => {
  if (!state.authChecked) {
    await checkAuth()
    if (state.me && !state.me.must_change_password) startPolling()
  }
  if (to.meta.public) {
    return state.me && to.name === 'login' ? '/' : true
  }
  if (!state.me) return { name: 'login', query: { redirect: to.fullPath } }
  // 使用初始密码时只能停留在账号页：这里必须直接放行，不能再转到设置页，否则两条规则互相跳转、页面卡死
  if (state.me.must_change_password) return to.name === 'account' ? true : { name: 'account' }
  // 查看者（只读，修订第 161 条）只能打开标了 viewer 的页面；告警页只有“告警”标签
  if (isViewer.value) {
    if (!to.meta.viewer) return '/'
    if (to.name === 'alerts' && to.query.tab && to.query.tab !== 'system') return { name: 'alerts' }
  }
  if (to.name === 'alerts' && !alertsOn.value) return '/' // 告警已关闭（修订第 175 条）
  if (to.name === 'alerts' && ['probes', 'ssl', 'services'].includes(to.query.tab as string)) return { path: settingsMoved[to.query.tab as string], query: {} }
  return true
})

router.afterEach((to) => {
  document.title = `${to.meta.title ?? ''} · VPS Monitor`
})

// ---- 加载状态与预取（修订第 173 条） ----
// 页面按需加载：首次打开某页要先下载它的代码（节点详情还要下载图表库），网络慢时会有数秒没有反应，
// 用户以为点击无效又点一次。这里提供“正在加载”的状态供顶部进度条使用，并在登录后空闲时预取全部页面的代码。

/** 是否正在切换页面（超过 150 毫秒才置为 true，快速切换时进度条不闪） */
export const navigating = ref(false)
let navTimer: ReturnType<typeof setTimeout> | undefined
router.beforeEach(() => {
  clearTimeout(navTimer)
  navTimer = setTimeout(() => (navigating.value = true), 150)
})
const navDone = () => {
  clearTimeout(navTimer)
  navigating.value = false
}
router.afterEach(navDone)
router.onError(navDone)

let prefetched = false
/** 登录后在浏览器空闲时预取全部页面的代码（含图表库），之后切换页面不再等待下载 */
export function prefetchPages() {
  if (prefetched) return
  prefetched = true
  const run = () => {
    for (const r of router.getRoutes()) {
      const c = r.components?.default
      if (typeof c === 'function') (c as () => Promise<unknown>)().catch(() => { /* 预取失败不影响使用，打开时再加载 */ })
    }
    import('./components/Chart.vue').catch(() => {})
  }
  const idle = (window as unknown as { requestIdleCallback?: (cb: () => void) => void }).requestIdleCallback
  if (idle) idle(run)
  else setTimeout(run, 1500)
}
