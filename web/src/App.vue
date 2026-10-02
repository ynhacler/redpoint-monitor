<script setup lang="ts">
// 应用外壳：顶部导航、深浅色切换、开发用 Token 输入，以及各页面的容器。
// 节点数据由 store 统一轮询，各页面共用（设计 41.6：总览、节点列表、详情口径一致）。
import { onMounted, onUnmounted, ref } from 'vue'
import Icon from './components/Icon.vue'
import { login, logout, startPolling, state, stopPolling } from './store'
import { applyTheme, cycleTheme, themeMode } from './theme'

const tokenInput = ref('')
const themeLabels = { system: '跟随系统', light: '浅色', dark: '深色' } as const

onMounted(() => {
  applyTheme(themeMode.value)
  if (!state.needToken) startPolling()
})
onUnmounted(stopPolling)

function submitToken() {
  if (tokenInput.value.trim()) login(tokenInput.value)
  tokenInput.value = ''
}
</script>

<template>
  <header class="topbar">
    <div class="inner">
      <RouterLink to="/" class="brand">VPS Monitor</RouterLink>
      <nav v-if="!state.needToken">
        <RouterLink to="/" exact-active-class="active">总览</RouterLink>
        <RouterLink to="/servers" active-class="active">节点</RouterLink>
      </nav>
      <div class="tools">
        <button class="text" type="button" :title="`主题：${themeLabels[themeMode]}（点击切换）`" @click="cycleTheme">
          <Icon :name="themeMode === 'dark' ? 'moon' : themeMode === 'light' ? 'sun' : 'monitor'" />
        </button>
        <button v-if="!state.needToken" class="text" type="button" title="退出（清除本标签页保存的 Token）" @click="logout">
          <Icon name="logout" />
        </button>
      </div>
    </div>
  </header>

  <!-- TODO(A2): 改为用户名密码登录页（设计 8.1、8.2） -->
  <main v-if="state.needToken" class="page">
    <form class="panel login" @submit.prevent="submitToken">
      <h1>进入面板</h1>
      <p class="muted small">开发阶段使用管理员 Token 访问。Token 只保存在当前标签页，关闭后需要重新输入。</p>
      <label for="tok" class="small">管理员 Token</label>
      <input id="tok" v-model="tokenInput" type="password" placeholder="adm_…" autocomplete="off" autofocus />
      <button type="submit">进入</button>
    </form>
  </main>

  <template v-else>
    <div v-if="state.error" class="page banner-wrap">
      <div class="banner">{{ state.error }}</div>
    </div>
    <RouterView />
  </template>
</template>

<style scoped>
.topbar { background: var(--surface); border-bottom: 1px solid var(--border); position: sticky; top: 0; z-index: 10; }
.inner { max-width: var(--max-width); margin: 0 auto; padding: 0 var(--space-4); height: 52px; display: flex; align-items: center; gap: var(--space-6); }
.brand { color: var(--text); font-weight: var(--weight-strong); font-size: var(--font-lg); text-decoration: none; }
nav { display: flex; gap: var(--space-1); }
nav a { color: var(--text-muted); padding: var(--space-1) var(--space-3); border-radius: var(--radius-sm); text-decoration: none; }
nav a:hover { color: var(--text); text-decoration: none; }
nav a.active { color: var(--text); background: var(--surface-2); font-weight: var(--weight-strong); }
.tools { margin-left: auto; display: flex; gap: var(--space-2); }
.tools button { padding: var(--space-1); }
.login { max-width: 420px; margin: var(--space-8) auto 0; display: flex; flex-direction: column; gap: var(--space-3); }
.login button { align-self: flex-start; }
.banner-wrap { padding-bottom: 0; }
.banner-wrap .banner { margin: 0; }
@media (max-width: 600px) {
  .inner { gap: var(--space-3); }
}
</style>
