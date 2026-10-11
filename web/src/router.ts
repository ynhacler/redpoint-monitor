// 页面路由（设计 3.3、8.1、10、11）。地址可刷新、可分享；服务端对未知路径返回 index.html（SPA 回退）。
// 未登录访问任何页面都先到 /login，登录后回到原页面；使用初始密码时只能停留在 /account（设计 17.4），之后 /account 转到“设置 → 账号”。
import { createRouter, createWebHistory } from 'vue-router'
import { checkAuth, setSignedOutHandler, startPolling, state } from './store'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('./pages/LoginPage.vue'), meta: { title: '登录', public: true } },
    { path: '/account', name: 'account', component: () => import('./pages/AccountPage.vue'), meta: { title: '账号' } },
    { path: '/alerts', name: 'alerts', component: () => import('./pages/AlertsPage.vue'), meta: { title: '告警' } },
    { path: '/settings', name: 'settings', component: () => import('./pages/SettingsPage.vue'), meta: { title: '设置' } },
    { path: '/upgrades', name: 'upgrades', component: () => import('./pages/UpgradesPage.vue'), meta: { title: 'Agent 升级' } },
    { path: '/app', name: 'app', component: () => import('./pages/AppAccessPage.vue'), meta: { title: 'App 接入' } },
    { path: '/costs', name: 'costs', component: () => import('./pages/CostsPage.vue'), meta: { title: '费用统计' } },
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
        if (document.querySelector(to.hash)) resolve({ el: to.hash, top: 16 })
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

const movedTabs = ['services', 'ssl', 'probes']

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
  // 账号页平时在“设置 → 账号”；服务、证书、测速从告警页移到设置页，旧地址与书签照常可用（修订第 126 条）
  if (to.name === 'account') return { name: 'settings', query: { tab: 'account' } }
  if (to.name === 'alerts' && movedTabs.includes(to.query.tab as string)) return { name: 'settings', query: { tab: to.query.tab } }
  return true
})

router.afterEach((to) => {
  document.title = `${to.meta.title ?? ''} · VPS Monitor`
})
