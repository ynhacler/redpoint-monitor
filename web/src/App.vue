<script setup lang="ts">
// 应用外壳：顶部导航（5 个分组）与二级导航（nav.ts，修订第 158 条）、深浅色切换、账号与退出，以及各页面的容器。
// 手机宽度（≤ 600px，修订第 199 条）：分组导航移到底部标签栏（图标 + 短名称），顶栏只留品牌名与工具区，页面内容更多。
// 登录状态与节点数据由 store 统一管理，各页面共用（设计 41.6）；未登录时由路由守卫带到 /login。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { getPanelVersion } from './api'
import ErrorBoundary from './components/ErrorBoundary.vue'
import Icon from './components/Icon.vue'
import { t } from './i18n'
import { activeAlertCount } from './metrics'
import { activeGroup, groupsFor, itemActive } from './nav'
import { navigating, prefetchPages } from './router'
import { site } from './site'
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
// 二级页面（节点详情、编辑、安装命令，修订第 230 条）：手机上不显示底部标签栏，由页面自己的返回与吸顶标签栏导航，内容不被两层导航挤占
const secondLevel = computed(() => ['server', 'server-edit', 'server-install'].includes(String(route.name)))
watch(secondLevel, (v) => document.documentElement.classList.toggle('no-tabbar', v), { immediate: true })
// 顶栏（含二级导航）的实际高度写入 --topbar-h，页面中吸顶的元素贴在它下面
const topbar = ref<HTMLElement | null>(null)
let topbarObs: ResizeObserver | null = null
const setTopbarH = () => document.documentElement.style.setProperty('--topbar-h', `${topbar.value?.offsetHeight ?? 52}px`)
onMounted(() => {
  setTopbarH()
  if (!topbar.value || typeof ResizeObserver === 'undefined') return
  topbarObs = new ResizeObserver(setTopbarH)
  topbarObs.observe(topbar.value)
})
onUnmounted(() => topbarObs?.disconnect())

// 面板版本显示在页脚（登录后读取一次）；git describe 的形式如 v0.4.1、v0.4.1-3-gabc123
const panelVersion = ref('')
// 登录后预取全部页面的代码，之后切换页面不再等待下载（修订第 173 条）
watch(signedIn, (on) => { if (on) prefetchPages() }, { immediate: true })
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
  <header ref="topbar" class="topbar">
    <div class="inner">
      <RouterLink to="/" class="brand">{{ site.app_name }}</RouterLink>
      <nav v-if="signedIn" class="groups">
        <!-- 仪表板即首页（统计 + 节点列表）；节点详情、编辑、安装命令也属于“仪表板” -->
        <RouterLink v-for="g in groups" :key="g.key" :to="g.items[0].to" :class="{ active: group?.key === g.key, 'with-badge': g.key === 'alerts' }">
          {{ t(g.name) }}<span v-if="g.key === 'alerts' && alertCount" class="badge num">{{ alertCount }}</span>
        </RouterLink>
      </nav>
      <div class="tools">
        <button class="text" type="button" :title="t('主题：{m}（点击切换）', { m: t(themeLabels[themeMode]) })" @click="cycleTheme">
          <Icon :name="themeMode === 'dark' ? 'moon' : themeMode === 'light' ? 'sun' : 'monitor'" />
        </button>
        <template v-if="state.me">
          <RouterLink to="/account" class="user small" :title="t('账号与密码')">
            <Icon name="user" /><span class="username">{{ state.me.username }}</span>
          </RouterLink>
          <button class="text" type="button" :title="t('退出登录')" @click="logout">
            <Icon name="logout" />
          </button>
        </template>
      </div>
    </div>
    <!-- 二级导航：当前分组有多个页面时显示 -->
    <nav v-if="signedIn && group && group.items.length > 1" class="subnav" :aria-label="t(group.name)">
      <div class="sub-inner">
        <RouterLink v-for="x in group.items" :key="x.to" :to="x.to" :class="{ active: itemActive(x, route, group) }">{{ t(x.name) }}</RouterLink>
      </div>
    </nav>
  </header>

  <div v-if="state.error && signedIn" class="page banner-wrap">
    <div class="banner">{{ state.error }}</div>
  </div>
  <!-- 页面加载中：顶部细进度条；首次确认登录状态时显示占位，不再只剩顶栏（修订第 173 条） -->
  <div v-if="navigating || !state.authChecked" class="progress" role="progressbar" :aria-label="t('加载中')" />
  <ErrorBoundary>
    <RouterView v-if="state.authChecked" />
    <main v-else class="page loading muted small">{{ t('加载中…') }}</main>
  </ErrorBoundary>
  <footer v-if="signedIn && panelVersion" class="footer small">
    {{ site.app_name }} {{ t('面板') }} <span class="num" :title="t('面板版本（Agent 版本见“Agent 升级”页）')">{{ panelVersion }}</span>
  </footer>
  <!-- 手机底部标签栏（修订第 199 条）：宽屏不显示 -->
  <nav v-if="signedIn && !secondLevel" class="tabbar" :aria-label="t('分组')">
    <RouterLink v-for="g in groups" :key="g.key" :to="g.items[0].to" :class="{ active: group?.key === g.key }">
      <Icon :name="g.icon" :size="22" />
      <span>{{ t(g.short) }}</span>
    </RouterLink>
  </nav>
</template>

<style scoped>
.progress { position: fixed; top: 0; left: 0; right: 0; height: 2px; z-index: 20; overflow: hidden; }
.progress::after { content: ''; position: absolute; top: 0; bottom: 0; width: 30%; background: var(--accent);
  animation: progress 1s ease-in-out infinite; }
@keyframes progress { from { left: -30%; } to { left: 100%; } }
.loading { text-align: center; padding-top: var(--space-8); }
.footer { max-width: var(--max-width); margin: var(--space-7) auto var(--space-5); padding: 0 var(--space-4); color: var(--text-muted);
  text-align: center; }
.topbar { background: var(--surface); border-bottom: 1px solid var(--border); position: sticky; top: 0; z-index: 10; }
.inner { max-width: var(--max-width); margin: 0 auto; padding: 0 var(--space-4); height: 52px; display: flex; align-items: center; gap: var(--space-6); }
.brand { white-space: nowrap; color: var(--text); font-weight: var(--weight-strong); font-size: var(--font-lg); text-decoration: none; }
/* 导航可收缩并横向滚动，任何宽度都不把右侧工具区挤出屏幕（修订第 152 条） */
nav.groups { display: flex; gap: var(--space-1); min-width: 0; overflow-x: auto; scrollbar-width: none; }
nav.groups a { white-space: nowrap; color: var(--text-muted); padding: var(--space-1) var(--space-3); border-radius: var(--radius-sm); text-decoration: none; }
nav.groups a:hover { color: var(--text); text-decoration: none; }
nav.groups a.active { color: var(--text); background: var(--surface-2); font-weight: var(--weight-strong); }
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
  nav.groups a { padding: var(--space-1) var(--space-2); }
  .sub-inner { gap: var(--space-4); }
}
/* 底部标签栏（修订第 199 条）：只在手机宽度显示；固定在底部，避开 iPhone 的主屏幕指示条 */
.tabbar { display: none; }
@media (max-width: 600px) {
  .inner { gap: var(--space-2); height: 46px; }
  .brand { font-size: var(--font-md); }
  .tools { gap: var(--space-1); }
  nav.groups { display: none; }
  .tabbar { display: grid; grid-auto-flow: column; grid-auto-columns: 1fr; position: fixed; left: 0; right: 0; bottom: 0; z-index: 10;
    background: color-mix(in srgb, var(--surface) 92%, transparent); backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);
    border-top: 1px solid var(--border); padding-bottom: env(safe-area-inset-bottom); }
  .tabbar a { display: flex; flex-direction: column; align-items: center; gap: 2px; padding: 6px 0 5px; color: var(--text-muted);
    font-size: 10px; line-height: 12px; text-decoration: none; }
  .tabbar a.active { color: var(--accent); font-weight: var(--weight-strong); }
  .footer { margin: var(--space-5) auto var(--space-3); }
}
</style>
