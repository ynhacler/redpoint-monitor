<script setup lang="ts">
// 应用外壳：顶部导航（5 个分组）与二级导航（nav.ts，修订第 158 条）、深浅色切换、账号与退出，以及各页面的容器。
// 登录状态与节点数据由 store 统一管理，各页面共用（设计 41.6）；未登录时由路由守卫带到 /login。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { getPanelVersion } from './api'
import ErrorBoundary from './components/ErrorBoundary.vue'
import Icon from './components/Icon.vue'
import { activeAlertCount } from './metrics'
import { activeGroup, groupsFor, itemActive } from './nav'
import { isViewer, logout, state, stopPolling } from './store'
import { applyTheme, cycleTheme, themeMode } from './theme'

const route = useRoute()
const themeLabels = { system: '跟随系统', light: '浅色', dark: '深色' } as const
// 使用初始密码时只显示账号页，不显示导航（设计 17.4）
// 进行中的告警条数（来自节点列表的活动告警摘要），显示在导航上
const alertCount = computed(() => activeAlertCount(state.servers))
const signedIn = computed(() => !!state.me && !state.me.must_change_password)
const groups = computed(() => groupsFor(isViewer.value))
const group = computed(() => activeGroup(route, groups.value))

// 面板版本显示在页脚（登录后读取一次）；git describe 的形式如 v0.4.1、v0.4.1-3-gabc123
const panelVersion = ref('')
watch(signedIn, async (on) => {
  if (!on || panelVersion.value) return
  try {
    const v = await getPanelVersion()
    panelVersion.value = /^\d/.test(v) ? `v${v}` : v
  } catch {
    /* 版本只是展示信息，读取失败时不显示 */
  }
}, { immediate: true })

onMounted(() => applyTheme(themeMode.value))
onUnmounted(stopPolling)
</script>

<template>
  <header class="topbar">
    <div class="inner">
      <RouterLink to="/" class="brand">VPS Monitor</RouterLink>
      <nav v-if="signedIn">
        <!-- 仪表板即首页（统计 + 节点列表）；节点详情、编辑、安装命令也属于“仪表板” -->
        <RouterLink v-for="g in groups" :key="g.key" :to="g.items[0].to" :class="{ active: group?.key === g.key, 'with-badge': g.key === 'alerts' }">
          {{ g.name }}<span v-if="g.key === 'alerts' && alertCount" class="badge num">{{ alertCount }}</span>
        </RouterLink>
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
    <!-- 二级导航：当前分组有多个页面时显示 -->
    <nav v-if="signedIn && group && group.items.length > 1" class="subnav" :aria-label="group.name">
      <div class="sub-inner">
        <RouterLink v-for="x in group.items" :key="x.to" :to="x.to" :class="{ active: itemActive(x, route, group) }">{{ x.name }}</RouterLink>
      </div>
    </nav>
  </header>

  <div v-if="state.error && signedIn" class="page banner-wrap">
    <div class="banner">{{ state.error }}</div>
  </div>
  <ErrorBoundary>
    <RouterView v-if="state.authChecked" />
  </ErrorBoundary>
  <footer v-if="signedIn && panelVersion" class="footer small">
    VPS Monitor 面板 <span class="num" title="面板版本（Agent 版本见“Agent 升级”页）">{{ panelVersion }}</span>
  </footer>
</template>

<style scoped>
.footer { max-width: var(--max-width); margin: var(--space-7) auto var(--space-5); padding: 0 var(--space-4); color: var(--text-muted);
  text-align: center; }
.topbar { background: var(--surface); border-bottom: 1px solid var(--border); position: sticky; top: 0; z-index: 10; }
.inner { max-width: var(--max-width); margin: 0 auto; padding: 0 var(--space-4); height: 52px; display: flex; align-items: center; gap: var(--space-6); }
.brand { white-space: nowrap; color: var(--text); font-weight: var(--weight-strong); font-size: var(--font-lg); text-decoration: none; }
/* 导航可收缩并横向滚动，任何宽度都不把右侧工具区挤出屏幕（修订第 152 条） */
nav:not(.subnav) { display: flex; gap: var(--space-1); min-width: 0; overflow-x: auto; scrollbar-width: none; }
nav:not(.subnav) a { white-space: nowrap; color: var(--text-muted); padding: var(--space-1) var(--space-3); border-radius: var(--radius-sm); text-decoration: none; }
nav:not(.subnav) a:hover { color: var(--text); text-decoration: none; }
nav:not(.subnav) a.active { color: var(--text); background: var(--surface-2); font-weight: var(--weight-strong); }
/* 二级导航：下划线标出当前页，窄屏横向滚动 */
.subnav { border-top: 1px solid var(--border); }
.sub-inner { max-width: var(--max-width); margin: 0 auto; padding: 0 var(--space-4); display: flex; gap: var(--space-5); overflow-x: auto;
  scrollbar-width: none; }
.subnav a { white-space: nowrap; color: var(--text-muted); padding: var(--space-2) 0; border-bottom: 2px solid transparent;
  text-decoration: none; font-size: var(--font-md); }
.subnav a:hover { color: var(--text); text-decoration: none; }
.subnav a.active { color: var(--text); border-bottom-color: var(--accent); font-weight: var(--weight-strong); }
.with-badge { display: inline-flex; align-items: center; gap: 4px; }
.badge { min-width: 16px; height: 16px; padding: 0 4px; border-radius: var(--radius-full); background: var(--bad); color: var(--on-accent);
  font-size: 10px; line-height: 16px; text-align: center; font-weight: var(--weight-strong); }
.tools { margin-left: auto; display: flex; align-items: center; gap: var(--space-3); flex-shrink: 0; }
.tools button { padding: var(--space-1); }
.user { color: var(--text-muted); display: flex; align-items: center; gap: var(--space-1); }
.user:hover { color: var(--text); text-decoration: none; }
.banner-wrap { padding-bottom: 0; }
.banner-wrap .banner { margin: 0; }
/* 中等宽度（平板、窄窗口）：收紧间距，用户名只显示图标 */
@media (max-width: 900px) {
  .inner { gap: var(--space-3); }
  .username { display: none; }
  nav:not(.subnav) a { padding: var(--space-1) var(--space-2); }
  .sub-inner { gap: var(--space-4); }
}
@media (max-width: 600px) {
  .inner { gap: var(--space-2); }
  .brand { font-size: var(--font-md); }
  .tools { gap: var(--space-1); }
}
/* 很窄的屏幕（≤ 420px）不显示品牌名：“仪表板”同样回到首页，留出空间给导航 */
@media (max-width: 420px) {
  .brand { display: none; }
  nav:not(.subnav) a { padding: var(--space-1); }
}
</style>
