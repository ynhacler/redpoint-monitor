<script setup lang="ts">
// 应用外壳：顶部导航、深浅色切换、账号与退出，以及各页面的容器。
// 登录状态与节点数据由 store 统一管理，各页面共用（设计 41.6）；未登录时由路由守卫带到 /login。
import { computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import ErrorBoundary from './components/ErrorBoundary.vue'
import Icon from './components/Icon.vue'
import { activeAlertCount } from './metrics'
import { logout, state, stopPolling } from './store'
import { applyTheme, cycleTheme, themeMode } from './theme'

const route = useRoute()
const themeLabels = { system: '跟随系统', light: '浅色', dark: '深色' } as const
// 使用初始密码时只显示账号页，不显示导航（设计 17.4）
// 进行中的告警条数（来自节点列表的活动告警摘要），显示在导航上
const alertCount = computed(() => activeAlertCount(state.servers))
const signedIn = computed(() => !!state.me && !state.me.must_change_password)

onMounted(() => applyTheme(themeMode.value))
onUnmounted(stopPolling)
</script>

<template>
  <header class="topbar">
    <div class="inner">
      <RouterLink to="/" class="brand">VPS Monitor</RouterLink>
      <nav v-if="signedIn">
        <!-- 仪表板即首页（统计 + 节点列表）；节点详情、编辑、安装命令也属于“仪表板” -->
        <RouterLink to="/" :class="{ active: route.path === '/' || route.path.startsWith('/servers') }">仪表板</RouterLink>
        <RouterLink to="/alerts" active-class="active" class="with-badge">告警<span v-if="alertCount" class="badge num">{{ alertCount }}</span></RouterLink>
        <RouterLink to="/cloud" active-class="active">云账户</RouterLink>
        <RouterLink to="/logs" active-class="active">日志</RouterLink>
      </nav>
      <div class="tools">
        <button class="text" type="button" :title="`主题：${themeLabels[themeMode]}（点击切换）`" @click="cycleTheme">
          <Icon :name="themeMode === 'dark' ? 'moon' : themeMode === 'light' ? 'sun' : 'monitor'" />
        </button>
        <template v-if="state.me">
          <RouterLink to="/account" class="user small" title="账号与密码">
            <Icon name="user" /><span class="username">{{ state.me.username }}</span>
          </RouterLink>
          <button class="text" type="button" title="退出登录" @click="logout">
            <Icon name="logout" />
          </button>
        </template>
      </div>
    </div>
  </header>

  <div v-if="state.error && signedIn" class="page banner-wrap">
    <div class="banner">{{ state.error }}</div>
  </div>
  <ErrorBoundary>
    <RouterView v-if="state.authChecked" />
  </ErrorBoundary>
</template>

<style scoped>
.topbar { background: var(--surface); border-bottom: 1px solid var(--border); position: sticky; top: 0; z-index: 10; }
.inner { max-width: var(--max-width); margin: 0 auto; padding: 0 var(--space-4); height: 52px; display: flex; align-items: center; gap: var(--space-6); }
.brand { white-space: nowrap; color: var(--text); font-weight: var(--weight-strong); font-size: var(--font-lg); text-decoration: none; }
nav { display: flex; gap: var(--space-1); }
nav a { white-space: nowrap; color: var(--text-muted); padding: var(--space-1) var(--space-3); border-radius: var(--radius-sm); text-decoration: none; }
nav a:hover { color: var(--text); text-decoration: none; }
nav a.active { color: var(--text); background: var(--surface-2); font-weight: var(--weight-strong); }
.with-badge { display: inline-flex; align-items: center; gap: 4px; }
.badge { min-width: 16px; height: 16px; padding: 0 4px; border-radius: var(--radius-full); background: var(--bad); color: var(--on-accent);
  font-size: 10px; line-height: 16px; text-align: center; font-weight: var(--weight-strong); }
.tools { margin-left: auto; display: flex; align-items: center; gap: var(--space-3); }
.tools button { padding: var(--space-1); }
.user { color: var(--text-muted); display: flex; align-items: center; gap: var(--space-1); }
.user:hover { color: var(--text); text-decoration: none; }
.banner-wrap { padding-bottom: 0; }
.banner-wrap .banner { margin: 0; }
@media (max-width: 600px) {
  .inner { gap: var(--space-2); }
  .brand { font-size: var(--font-md); }
  .tools { gap: var(--space-1); }
  .username { display: none; } /* 窄屏只显示图标，仍可进入账号页修改密码 */
  nav a { padding: var(--space-1) var(--space-2); }
  /* 导航项较多时可横向滚动，不与右侧工具区重叠 */
  nav { min-width: 0; overflow-x: auto; scrollbar-width: none; }
}
/* 很窄的屏幕（≤ 420px）不显示品牌名：“仪表板”同样回到首页，留出空间给导航 */
@media (max-width: 420px) {
  .brand { display: none; }
}
</style>
