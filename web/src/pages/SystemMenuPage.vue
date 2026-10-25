<script setup lang="ts">
// 系统菜单（修订第 231 条，需求说明书 SET-01 / SET-04）：手机底部“系统”打开这一页，按小组列出系统下的各页面（图标、名称、一行说明），
// 代替顶栏下横向滚动的七个标签；各页面顶部显示“← 系统”返回这里。退出登录放在本页最下方，不再放在手机顶栏，避免误触。
// 电脑上二级导航不变，这一页也能打开。
import { computed } from 'vue'
import Icon from '../components/Icon.vue'
import { t } from '../i18n'
import { groupsFor, type NavItem } from '../nav'
import { isViewer, logout, state } from '../store'

const sections = computed(() => {
  const g = groupsFor(isViewer.value).find((x) => x.key === 'system')
  const out: { name: string; items: NavItem[] }[] = []
  for (const it of g?.items ?? []) {
    const name = it.section ?? ''
    const sec = out.find((x) => x.name === name)
    if (sec) sec.items.push(it)
    else out.push({ name, items: [it] })
  }
  return out
})
</script>

<template>
  <main class="page">
    <div class="page-head"><h1>{{ t('系统') }}</h1></div>
    <section v-for="sec in sections" :key="sec.name" class="sec">
      <h2 class="small muted">{{ t(sec.name) }}</h2>
      <ul class="panel menu">
        <li v-for="it in sec.items" :key="it.to">
          <RouterLink :to="it.to" class="row">
            <span class="ic" aria-hidden="true"><Icon :name="it.icon ?? 'settings'" :size="18" /></span>
            <span class="txt">
              <b>{{ t(it.name) }}</b>
              <span v-if="it.desc" class="small muted">{{ t(it.desc) }}</span>
            </span>
            <Icon name="chevron-right" :size="16" class="go" />
          </RouterLink>
        </li>
      </ul>
    </section>
    <section v-if="state.me" class="sec">
      <ul class="panel menu">
        <li>
          <button type="button" class="row out" @click="logout">
            <span class="ic" aria-hidden="true"><Icon name="logout" :size="18" /></span>
            <span class="txt"><b>{{ t('退出登录') }}</b><span class="small muted">{{ t('当前账号：{name}', { name: state.me.username }) }}</span></span>
          </button>
        </li>
      </ul>
    </section>
  </main>
</template>

<style scoped>
.sec { margin-bottom: var(--space-4); }
.sec h2 { margin: 0 0 var(--space-2) var(--space-1); font-weight: normal; }
.menu { list-style: none; margin: 0; padding: 0; overflow: hidden; }
.menu li + li { border-top: 1px solid var(--border); }
.row { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-3) var(--space-4); min-height: 56px; color: var(--text);
  text-decoration: none; width: 100%; background: none; border: 0; border-radius: 0; text-align: left; font: inherit; cursor: pointer; }
.row:hover { background: var(--surface-2); text-decoration: none; }
.ic { flex: none; width: 32px; height: 32px; border-radius: var(--radius-sm); display: grid; place-items: center;
  background: color-mix(in srgb, var(--accent) 10%, transparent); color: var(--accent); }
.out .ic { background: color-mix(in srgb, var(--bad) 10%, transparent); color: var(--bad); }
.txt { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.txt .small { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.go { color: var(--text-muted); flex: none; }
</style>
