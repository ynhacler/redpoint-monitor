<script setup lang="ts">
// 节点列表（设计 10）：搜索、按分组筛选、排序（默认异常优先，设计 1.5.6）；待安装节点单独一组（设计 27.7）。
import { computed, ref } from 'vue'
import type { ServerView } from '../api'
import EmptyState from '../components/EmptyState.vue'
import Flag from '../components/Flag.vue'
import { countryName } from '../countries'
import Icon from '../components/Icon.vue'
import ServerCard from '../components/ServerCard.vue'
import StatusDot from '../components/StatusDot.vue'
import { cpu, fullestDisk, issues, mem, trafficPct } from '../metrics'
import { installed, pending, state, statusRank } from '../store'

const query = ref('')
const group = ref('')
const sortBy = ref<'abnormal' | 'cpu' | 'mem' | 'disk' | 'traffic' | 'name'>('abnormal')
const sorts = [
  { v: 'abnormal', t: '异常优先' }, { v: 'cpu', t: 'CPU' }, { v: 'mem', t: '内存' },
  { v: 'disk', t: '磁盘' }, { v: 'traffic', t: '流量' }, { v: 'name', t: '名称' },
] as const

const groups = computed(() => [...new Set(installed.value.map((s) => s.group).filter(Boolean))].sort())

// 搜索名称、主机名、IP、分组、供应商、地区，不区分大小写
const matches = (s: ServerView) => {
  const q = query.value.trim().toLowerCase()
  if (group.value && s.group !== group.value) return false
  if (!q) return true
  // 国家可按代码或中文名搜索，如 “jp”“日本”
  return [s.name, s.hostname, s.ipv4, s.ipv6, s.expected_ipv4, s.group, s.provider, s.region, s.country, countryName(s.country)]
    .some((v) => v?.toLowerCase().includes(q))
}

const desc = (f: (s: ServerView) => number | undefined) => (a: ServerView, b: ServerView) => (f(b) ?? -1) - (f(a) ?? -1)
const sorted = computed(() => {
  const list = installed.value.filter(matches)
  const byName = (a: ServerView, b: ServerView) => a.name.localeCompare(b.name)
  const cmp: Record<typeof sortBy.value, (a: ServerView, b: ServerView) => number> = {
    // 异常优先：离线 > 未知 > 有告警的在线节点 > 正常在线，同级按名称
    abnormal: (a, b) =>
      statusRank[a.status] - statusRank[b.status] || issues(b).length - issues(a).length || byName(a, b),
    cpu: desc(cpu),
    mem: desc(mem),
    disk: desc((s) => fullestDisk(s)?.usage),
    traffic: desc(trafficPct),
    name: byName,
  }
  return list.sort((a, b) => cmp[sortBy.value](a, b) || byName(a, b))
})
const pendingFiltered = computed(() => pending.value.filter(matches))
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>节点</h1>
      <RouterLink to="/servers/new" class="btn"><Icon name="plus" />新建节点</RouterLink>
    </div>

    <div v-if="state.servers.length" class="filters">
      <label class="search">
        <Icon name="search" />
        <input v-model="query" type="search" placeholder="搜索名称、IP、分组、供应商" />
      </label>
      <select v-if="groups.length" v-model="group" aria-label="分组">
        <option value="">全部分组</option>
        <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
      </select>
      <div class="segmented" role="group" aria-label="排序">
        <button v-for="o in sorts" :key="o.v" type="button" :class="{ active: sortBy === o.v }" @click="sortBy = o.v">{{ o.t }}</button>
      </div>
    </div>

    <p v-if="!state.loaded" class="muted">加载中…</p>
    <EmptyState v-else-if="!state.servers.length" text="还没有节点。新建一个节点，按页面上的命令在主机上安装 Agent。">
      <RouterLink to="/servers/new" class="btn"><Icon name="plus" />新建节点</RouterLink>
    </EmptyState>
    <template v-else>
      <div v-if="sorted.length" class="grid server-grid">
        <ServerCard v-for="s in sorted" :key="s.id" :server="s" />
      </div>
      <p v-else-if="installed.length" class="muted">没有匹配的节点。</p>

      <!-- 待安装：不参与排序与异常统计，提供安装命令入口（设计 27.7） -->
      <section v-if="pendingFiltered.length" class="section">
        <h3 class="muted">待安装</h3>
        <div class="grid">
          <div v-for="s in pendingFiltered" :key="s.id" class="pending-card">
            <div class="row">
              <strong class="name"><Flag :code="s.country" /> {{ s.name }}</strong>
              <StatusDot status="pending" />
            </div>
            <div class="muted small">{{ [s.region, s.provider, s.group].filter(Boolean).join(' · ') || '尚未在主机上安装 Agent' }}</div>
            <div class="row actions">
              <RouterLink :to="`/servers/${s.id}/install`" class="btn secondary"><Icon name="terminal" />安装命令</RouterLink>
              <RouterLink :to="`/servers/${s.id}/edit`" class="btn text"><Icon name="edit" />编辑</RouterLink>
            </div>
          </div>
        </div>
      </section>
    </template>
  </main>
</template>

<style scoped>
/* 节点卡片一行五列，最窄需要约 320px（见 ServerCard） */
.server-grid { grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); }
.filters { display: flex; gap: var(--space-2); flex-wrap: wrap; align-items: center; margin-bottom: var(--space-4); }
.search { display: flex; align-items: center; gap: var(--space-2); flex: 1 1 240px; max-width: 360px;
  border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--surface); padding: 0 var(--space-3); color: var(--text-muted); }
.search input { border: 0; padding-left: 0; flex: 1; background: transparent; outline: none; }
.pending-card { border: 1px dashed var(--border); border-radius: var(--radius-md); padding: var(--space-4); display: flex; flex-direction: column; gap: var(--space-1); }
.pending-card .row { justify-content: space-between; }
.pending-card .actions { justify-content: flex-start; margin-top: var(--space-3); }
.name { font-size: var(--font-lg); }
</style>
