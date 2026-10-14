// 页面路由（设计 3.3、8.1、10、11）。地址可刷新、可分享；服务端对未知路径返回 index.html（SPA 回退）。
// 未登录访问任何页面都先到 /login，登录后回到原页面；使用初始密码时只能停留在 /account（设计 17.4）。
// 页面按 nav.ts 的 5 个分组组织（修订第 158 条）；原“设置”页拆到“监测”与“系统”，旧地址照常可用。
import { createRouter, createWebHistory } from 'vue-router'
import { checkAuth, setSignedOutHandler, startPolling, state } from './store'

// 原“设置”页各标签的新地址（修订第 158 条）；修订第 126 条前这些标签在告警页，/alerts?tab=… 同样转过去
const settingsMoved: Record<string, string> = { probes: '/probes', ssl: '/ssl', services: '/services', account: '/account' }

/** 页面路由 */
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('./pages/LoginPage.vue'), meta: { title: '登录', public: true } },
    { path: '/account', name: 'account', component: () => import('./pages/AccountPage.vue'), meta: { title: '账号' } },
    { path: '/alerts', name: 'alerts', component: () => import('./pages/AlertsPage.vue'), meta: { title: '告警' } },
    { path: '/probes', name: 'probes', component: () => import('./pages/MonitorSettingsPage.vue'), props: { kind: 'probes' }, meta: { title: '测速设置' } },
    { path: '/ssl', name: 'ssl', component: () => import('./pages/MonitorSettingsPage.vue'), props: { kind: 'ssl' }, meta: { title: '证书' } },
    { path: '/services', name: 'services', component: () => import('./pages/MonitorSettingsPage.vue'), props: { kind: 'services' }, meta: { title: '服务' } },
    // 旧“设置”页：按标签转到新位置，保留 #锚点（如国内探针 #reach）
    { path: '/settings', redirect: (to) => ({ path: settingsMoved[to.query.tab as string] ?? '/probes', query: {}, hash: to.hash }) },
    { path: '/upgrades', name: 'upgrades', component: () => import('./pages/UpgradesPage.vue'), meta: { title: 'Agent 升级' } },
    { path: '/app', name: 'app', component: () => import('./pages/AppAccessPage.vue'), meta: { title: 'App 接入' } },
    { path: '/costs', name: 'costs', component: () => import('./pages/CostsPage.vue'), meta: { title: '费用统计' } },
    { path: '/latency', name: 'latency', component: () => import('./pages/LatencyOverviewPage.vue'), meta: { title: '测速对比' } },
    { path: '/cloud', name: 'cloud', component: () => import('./pages/CloudPage.vue'), meta: { title: '云账户' } },
    { path: '/logs', name: 'logs', component: () => import('./pages/LogsPage.vue'), meta: { title: '日志' } },
    // 总览与节点列表合并为首页（修订记录第 30 条）；旧地址 /servers 保留跳转，书签不失效
    { path: '/', name: 'servers', component: () => import('./pages/ServersPage.vue'), meta: { title: '仪表板' } },
    { path: '/servers', redirect: (to) => ({ path: '/', query: to.query }) },
    { path: '/servers/batch', name: 'server-batch', component: () => import('./pages/BatchCreatePage.vue'), meta: { title: '批量新建' } },
    { path: '/servers/new', name: 'server-new', component: () => import('./pages/ServerEditPage.vue'), meta: { title: '新建节点' } },
    { path: '/servers/:id(\\d+)', name: 'server', component: () => import('./pages/ServerDetailPage.vue'), props: true, meta: { title: '节点详情' } },
    { path: '/servers/:id(\\d+)/edit', name: 'server-edit', component: () => import('./pages/ServerEditPage.vue'), props: true, meta: { title: '编辑节点' } },
    { path: '/servers/:id(\\d+)/install', name: 'server-install', component: () => import('./pages/InstallPage.vue'), props: true, meta: { title: '安装命令' } },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  // 地址带 #锚点 时滚动到该元素：页面内容多为异步加载，最多等 2 秒；否则回到顶部
  scrollBehavior: (to) => {
    if (!to.hash) return { top: 0 }
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
  if (to.name === 'alerts' && ['probes', 'ssl', 'services'].includes(to.query.tab as string)) return { path: settingsMoved[to.query.tab as string], query: {} }
  return true
})

router.afterEach((to) => {
  document.title = `${to.meta.title ?? ''} · VPS Monitor`
})
