// 页面路由（设计 3.3、10、11）。地址可刷新、可分享；服务端对未知路径返回 index.html（SPA 回退）。
import { createRouter, createWebHistory } from 'vue-router'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
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

router.afterEach((to) => {
  document.title = `${to.meta.title ?? ''} · VPS Monitor`
})
