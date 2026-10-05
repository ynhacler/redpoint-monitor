<script setup lang="ts">
// 节点（设计 9、10）：首页。顶部为统计，点击即按状态筛选（总览与列表合并，修订记录第 30 条）；
// 下方搜索、按分组筛选、排序（默认异常优先，设计 1.5.6）；待安装节点单独一组（设计 27.7）。
// 状态筛选写在地址中（?f=offline），刷新与分享后保持。
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { ServerView } from '../api'
import EmptyState from '../components/EmptyState.vue'
import Flag from '../components/Flag.vue'
import { countryName } from '../countries'
import Icon from '../components/Icon.vue'
import ServerCard from '../components/ServerCard.vue'
import StatusDot from '../components/StatusDot.vue'
import { cpu, diskSummary, issues, mem, trafficPct } from '../metrics'
import { installed, pending, state, statusRank } from '../store'

const route = useRoute()
const router = useRouter()

// 状态筛选（原总览的统计卡片）：全部 / 在线 / 离线 / 需要关注 / 流量 ≥ 80%
type StatusFilter = 'all' | 'online' | 'offline' | 'attention' | 'traffic'
const filters: { v: StatusFilter; t: string; test: (s: ServerView) => boolean; level?: 'ok' | 'bad' | 'warn' }[] = [
  { v: 'all', t: '全部', test: () => true },
  { v: 'online', t: '在线', test: (s) => s.status === 'online', level: 'ok' },
  { v: 'offline', t: '离线', test: (s) => s.status === 'offline', level: 'bad' },
  { v: 'attention', t: '需要关注', test: (s) => issues(s).length > 0, level: 'warn' },
  { v: 'traffic', t: '流量≥80%', test: (s) => (trafficPct(s) ?? 0) >= 80, level: 'warn' },
]
const statusFilter = computed<StatusFilter>(() => {
  const f = route.query.f
  return filters.some((x) => x.v === f) ? (f as StatusFilter) : 'all'
})
const counts = computed(() => Object.fromEntries(filters.map((x) => [x.v, installed.value.filter(x.test).length])))
function setFilter(v: StatusFilter) {
  router.replace({ query: { ...route.query, f: v === 'all' ? undefined : v } })
}

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
  const test = filters.find((x) => x.v === statusFilter.value)!.test
  const list = installed.value.filter((s) => test(s) && matches(s))
  const byName = (a: ServerView, b: ServerView) => a.name.localeCompare(b.name)
  const cmp: Record<typeof sortBy.value, (a: ServerView, b: ServerView) => number> = {
    // 异常优先：离线 > 未知 > 有告警的在线节点 > 正常在线，同级按名称
    abnormal: (a, b) =>
      statusRank[a.status] - statusRank[b.status] || issues(b).length - issues(a).length || byName(a, b),
    cpu: desc(cpu),
    mem: desc(mem),
    disk: desc((s) => diskSummary(s)?.usage),
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
      <span v-if="state.updatedAt" class="muted small updated">更新于 {{ state.updatedAt.toLocaleTimeString() }}</span>
      <RouterLink v-if="installed.length" to="/upgrades" class="btn secondary">Agent 升级</RouterLink>
      <RouterLink to="/servers/batch" class="btn secondary">批量新建</RouterLink>
      <RouterLink to="/servers/new" class="btn"><Icon name="plus" />新建节点</RouterLink>
    </div>

    <!-- 统计即筛选：数字为该状态的节点数，点击只看这些节点 -->
    <div v-if="installed.length" class="stats" role="tablist" aria-label="按状态筛选">
      <button v-for="x in filters" :key="x.v" type="button" role="tab" class="stat" :aria-selected="statusFilter === x.v"
        :class="{ active: statusFilter === x.v }" @click="setFilter(x.v)">
        <span class="muted small">{{ x.t }}</span>
        <span class="n num" :class="counts[x.v] && x.level && x.v !== 'all' ? x.level : ''">{{ counts[x.v] }}</span>
      </button>
      <a v-if="pending.length" href="#pending" class="stat">
        <span class="muted small">待安装</span><span class="n num muted">{{ pending.length }}</span>
      </a>
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
      <p v-else-if="installed.length" class="muted empty-filter">
        {{ statusFilter === 'all' ? '没有匹配的节点。' : `没有“${filters.find((x) => x.v === statusFilter)!.t}”的节点。` }}
        <button v-if="statusFilter !== 'all'" type="button" class="text" @click="setFilter('all')">查看全部</button>
      </p>

      <!-- 待安装：不参与排序与异常统计，提供安装命令入口（设计 27.7） -->
      <section v-if="pendingFiltered.length" id="pending" class="section">
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
/* 统计：一行等宽，窄屏横向滚动而不是换行，保持紧凑 */
.updated { margin-left: auto; margin-right: var(--space-3); }
.stats { display: grid; grid-auto-flow: column; grid-auto-columns: minmax(96px, 1fr); gap: var(--space-2); margin-bottom: var(--space-4);
  overflow-x: auto; scrollbar-width: none; }
.stat { display: flex; flex-direction: column; align-items: flex-start; gap: 2px; padding: var(--space-3); text-align: left;
  background: var(--surface); color: var(--text); border: 1px solid var(--border); border-radius: var(--radius-md); text-decoration: none; }
.stat:hover { border-color: var(--text-muted); text-decoration: none; filter: none; }
.stat.active { border-color: var(--accent); box-shadow: inset 0 0 0 1px var(--accent); }
.n { font-size: var(--font-xl); line-height: var(--line-xl); font-weight: var(--weight-strong); }
.n.ok { color: var(--ok); }
.n.bad { color: var(--bad); }
.n.warn { color: var(--warn); }
.empty-filter { display: flex; align-items: center; gap: var(--space-2); }
/* 手机：五项挤进一行（待安装有入口时仍可横向滑动），去掉“更新于”腾出标题行空间 */
@media (max-width: 600px) {
  .updated { display: none; }
  .stats { grid-auto-columns: minmax(58px, 1fr); gap: 6px; }
  .stat { padding: var(--space-2); align-items: center; text-align: center; }
  .stat .small { font-size: var(--font-xs); white-space: nowrap; }
  .n { font-size: var(--font-lg); line-height: var(--line-lg); }
  .page-head h1 { margin-right: auto; }
}
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
