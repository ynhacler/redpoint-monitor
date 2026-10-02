<script setup lang="ts">
// 应用外壳：顶部导航、深浅色切换、账号与退出，以及各页面的容器。
// 登录状态与节点数据由 store 统一管理，各页面共用（设计 41.6）；未登录时由路由守卫带到 /login。
import { computed, onMounted, onUnmounted } from 'vue'
import Icon from './components/Icon.vue'
import { logout, state, stopPolling } from './store'
import { applyTheme, cycleTheme, themeMode } from './theme'

const themeLabels = { system: '跟随系统', light: '浅色', dark: '深色' } as const
// 使用初始密码时只显示账号页，不显示导航（设计 17.4）
const signedIn = computed(() => !!state.me && !state.me.must_change_password)

onMounted(() => applyTheme(themeMode.value))
onUnmounted(stopPolling)
</script>

<template>
  <header class="topbar">
    <div class="inner">
      <RouterLink to="/" class="brand">VPS Monitor</RouterLink>
      <nav v-if="signedIn">
        <RouterLink to="/" exact-active-class="active">总览</RouterLink>
        <RouterLink to="/servers" active-class="active">节点</RouterLink>
      </nav>
      <div class="tools">
        <button class="text" type="button" :title="`主题：${themeLabels[themeMode]}（点击切换）`" @click="cycleTheme">
          <Icon :name="themeMode === 'dark' ? 'moon' : themeMode === 'light' ? 'sun' : 'monitor'" />
        </button>
        <template v-if="state.me">
          <RouterLink to="/account" class="user small" title="账号与密码">{{ state.me.username }}</RouterLink>
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
  <RouterView v-if="state.authChecked" />
</template>

<style scoped>
.topbar { background: var(--surface); border-bottom: 1px solid var(--border); position: sticky; top: 0; z-index: 10; }
.inner { max-width: var(--max-width); margin: 0 auto; padding: 0 var(--space-4); height: 52px; display: flex; align-items: center; gap: var(--space-6); }
.brand { color: var(--text); font-weight: var(--weight-strong); font-size: var(--font-lg); text-decoration: none; }
nav { display: flex; gap: var(--space-1); }
nav a { color: var(--text-muted); padding: var(--space-1) var(--space-3); border-radius: var(--radius-sm); text-decoration: none; }
nav a:hover { color: var(--text); text-decoration: none; }
nav a.active { color: var(--text); background: var(--surface-2); font-weight: var(--weight-strong); }
.tools { margin-left: auto; display: flex; align-items: center; gap: var(--space-3); }
.tools button { padding: var(--space-1); }
.user { color: var(--text-muted); }
.user:hover { color: var(--text); text-decoration: none; }
.banner-wrap { padding-bottom: 0; }
.banner-wrap .banner { margin: 0; }
@media (max-width: 600px) {
  .inner { gap: var(--space-3); }
  .user { display: none; }
}
</style>
