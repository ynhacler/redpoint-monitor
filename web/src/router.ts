// 页面路由（设计 3.3、8.1、10、11）。地址可刷新、可分享；服务端对未知路径返回 index.html（SPA 回退）。
// 未登录访问任何页面都先到 /login，登录后回到原页面；使用初始密码时只能停留在 /account（设计 17.4）。
import { createRouter, createWebHistory } from 'vue-router'
import { checkAuth, setSignedOutHandler, startPolling, state } from './store'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('./pages/LoginPage.vue'), meta: { title: '登录', public: true } },
    { path: '/account', name: 'account', component: () => import('./pages/AccountPage.vue'), meta: { title: '账号' } },
    { path: '/', name: 'overview', component: () => import('./pages/OverviewPage.vue'), meta: { title: '总览' } },
    { path: '/servers', name: 'servers', component: () => import('./pages/ServersPage.vue'), meta: { title: '节点' } },
    { path: '/servers/new', name: 'server-new', component: () => import('./pages/ServerEditPage.vue'), meta: { title: '新建节点' } },
    { path: '/servers/:id(\\d+)', name: 'server', component: () => import('./pages/ServerDetailPage.vue'), props: true, meta: { title: '节点详情' } },
    { path: '/servers/:id(\\d+)/edit', name: 'server-edit', component: () => import('./pages/ServerEditPage.vue'), props: true, meta: { title: '编辑节点' } },
    { path: '/servers/:id(\\d+)/install', name: 'server-install', component: () => import('./pages/InstallPage.vue'), props: true, meta: { title: '安装命令' } },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior: () => ({ top: 0 }),
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
  if (state.me.must_change_password && to.name !== 'account') return { name: 'account' }
  return true
})

router.afterEach((to) => {
  document.title = `${to.meta.title ?? ''} · VPS Monitor`
})
