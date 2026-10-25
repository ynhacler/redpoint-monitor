<script setup lang="ts">
// 节点（设计 9、10）：首页。顶部为统计，点击即按状态筛选（总览与列表合并，修订记录第 30 条）；
// 下方搜索、按分组筛选、排序（默认异常优先，设计 1.5.6）；待安装节点单独一组（设计 27.7）。
// 状态筛选写在地址中（?f=offline），刷新与分享后保持。排序方式记在本机浏览器（localStorage）。
// “自定义”排序按面板保存的顺序（修订第 150 条）：点“调整顺序”进入排列模式，拖动或用上下按钮排列后保存到面板，
// 所有浏览器与（本机没有自己顺序的）App 共用。
import NotifyWarning from '../components/NotifyWarning.vue'
import FleetCards from '../components/FleetCards.vue'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { errorText, putServerOrder, type ServerView } from '../api'
import EmptyState from '../components/EmptyState.vue'
import Flag from '../components/Flag.vue'
import { countryName } from '../countries'
import Icon from '../components/Icon.vue'
import ServerCard from '../components/ServerCard.vue'
import StatusDot from '../components/StatusDot.vue'
import { cpu, diskSummary, issues, mem, trafficPct } from '../metrics'
import { t } from '../i18n'
import { alertsOn, installed, isViewer, pending, refresh, state, statusRank } from '../store'

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

// 搜索与分组也写在地址中（修订第 230 条）：从节点详情返回时保持
const query = ref(String(route.query.q ?? ''))
const group = ref(String(route.query.g ?? ''))
watch([query, group], ([q, g]) => router.replace({ query: { ...route.query, q: q || undefined, g: g || undefined } }))
const sorts = [
  { v: 'abnormal', t: '异常优先' }, { v: 'cpu', t: 'CPU' }, { v: 'mem', t: '内存' },
  { v: 'disk', t: '磁盘' }, { v: 'traffic', t: '流量' }, { v: 'name', t: '名称' }, { v: 'custom', t: '自定义' },
] as const
type SortKey = (typeof sorts)[number]['v']
const sortKey = 'vpsmon.sortBy'
function loadSort(): SortKey {
  try {
    const v = localStorage.getItem(sortKey)
    return sorts.some((x) => x.v === v) ? (v as SortKey) : 'abnormal'
  } catch {
    return 'abnormal'
  }
}
const sortBy = ref<SortKey>(loadSort())
watch(sortBy, (v) => {
  try {
    localStorage.setItem(sortKey, v)
  } catch {
    // 无痕模式等：只是不记住
  }
})

// 自定义顺序：sort_order 从 1 开始，0 表示未排（排在后面，按名称）
const byCustom = (a: ServerView, b: ServerView) => (a.sort_order || Infinity) - (b.sort_order || Infinity)

// ---- 排列模式 ----
const arranging = ref<ServerView[] | null>(null)
const saving = ref(false)
const arrangeError = ref('')
const dragFrom = ref<number | null>(null)
const listEl = ref<HTMLElement | null>(null)
function startArrange() {
  arranging.value = [...installed.value].sort((a, b) => byCustom(a, b) || a.name.localeCompare(b.name))
  arrangeError.value = ''
}
function move(from: number, to: number) {
  const l = arranging.value!
  if (to < 0 || to >= l.length || from === to) return
  l.splice(to, 0, ...l.splice(from, 1))
}
// 拖动用 Pointer Events（鼠标、触屏、触控笔都适用；HTML5 拖放在手机上不可用）：按住把手移动，
// 经过其他行的中线时即时换位，松开结束
function onGripDown(e: PointerEvent, i: number) {
  dragFrom.value = i
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}
function onGripMove(e: PointerEvent) {
  if (dragFrom.value === null || !listEl.value) return
  const rows = Array.from(listEl.value.children) as HTMLElement[]
  const to = rows.findIndex((row) => {
    const r = row.getBoundingClientRect()
    return e.clientY >= r.top && e.clientY < r.bottom
  })
  if (to === -1 || to === dragFrom.value) return
  const r = rows[to].getBoundingClientRect()
  // 越过目标行的中线才换位，避免在两行之间来回跳
  const past = to > dragFrom.value ? e.clientY > r.top + r.height / 2 : e.clientY < r.bottom - r.height / 2
  if (!past) return
  move(dragFrom.value, to)
  dragFrom.value = to
}
function onGripUp() {
  dragFrom.value = null
}
async function saveArrange() {
  saving.value = true
  arrangeError.value = ''
  try {
    await putServerOrder(arranging.value!.map((s) => s.id))
    await refresh()
    arranging.value = null
    sortBy.value = 'custom'
  } catch (e) {
    arrangeError.value = errorText(e, t('保存失败'))
  } finally {
    saving.value = false
  }
}

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
    custom: byCustom,
  }
  return list.sort((a, b) => cmp[sortBy.value](a, b) || byName(a, b))
})
const pendingFiltered = computed(() => pending.value.filter(matches))
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>{{ t('节点') }}</h1>
      <span v-if="state.updatedAt" class="muted small updated">{{ t('更新于 {time}', { time: state.updatedAt.toLocaleTimeString() }) }}</span>
      <!-- 批量新建的入口在新建节点页（修订第 178 条） -->
      <RouterLink v-if="!isViewer" to="/servers/new" class="btn"><Icon name="plus" />{{ t('新建节点') }}</RouterLink>
    </div>

    <!-- 没有启用的通知渠道时常驻提醒（修订第 173 条） -->
    <NotifyWarning v-if="installed.length && alertsOn" />

    <!-- 舰队总览：剩余流量与先用完的节点、每月总成本、30 天内到期（修订第 174 条） -->
    <FleetCards v-if="installed.length" />

    <div v-if="state.servers.length" class="filters">
      <!-- 统计即筛选（修订第 187 条：由一排大方块改为搜索框前的小标签）：数字为该状态的节点数，点击只看这些节点；
           离线、需要关注等为 0 时不显示 -->
      <div v-if="installed.length" class="chips" role="tablist" :aria-label="t('按状态筛选')">
        <button v-for="x in filters.filter((f) => f.v === 'all' || f.v === 'online' || counts[f.v] || statusFilter === f.v)" :key="x.v"
          type="button" role="tab" class="chip" :aria-selected="statusFilter === x.v" :class="{ active: statusFilter === x.v }" @click="setFilter(x.v)">
          {{ t(x.t) }}<b class="num" :class="counts[x.v] && x.level && x.v !== 'all' ? x.level : ''">{{ counts[x.v] }}</b>
        </button>
        <a v-if="pending.length" href="#pending" class="chip">{{ t('待安装') }}<b class="num muted">{{ pending.length }}</b></a>
      </div>
      <label class="search">
        <Icon name="search" />
        <input v-model="query" type="search" :placeholder="t('搜索名称、IP、分组、供应商')" />
      </label>
      <select v-if="groups.length" v-model="group" :aria-label="t('分组')">
        <option value="">{{ t('全部分组') }}</option>
        <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
      </select>
      <select v-model="sortBy" :aria-label="t('排序')">
        <option v-for="o in sorts" :key="o.v" :value="o.v">{{ t('排序：{name}', { name: t(o.t) }) }}</option>
      </select>
      <button v-if="sortBy === 'custom' && installed.length > 1 && !arranging && !isViewer" type="button" class="btn secondary" @click="startArrange">
        <Icon name="grip" />{{ t('调整顺序') }}
      </button>
    </div>

    <!-- 排列模式：按住把手拖动或用上下按钮；保存到面板，所有浏览器共用 -->
    <section v-if="arranging" class="panel arrange" :aria-label="t('调整顺序')">
      <div class="arrange-head">
        <strong>{{ t('调整顺序') }}</strong>
        <span class="muted small">{{ t('按住左侧把手拖动，或用右侧按钮排列；保存后所有浏览器与 App（本机没有自己的顺序时）按此顺序显示。') }}</span>
      </div>
      <ol ref="listEl" class="arrange-list">
        <li v-for="(s, i) in arranging" :key="s.id" :class="{ dragging: dragFrom === i }">
          <span class="grip muted" role="presentation" @pointerdown.prevent="onGripDown($event, i)" @pointermove="onGripMove"
            @pointerup="onGripUp" @pointercancel="onGripUp"><Icon name="grip" /></span>
          <span class="idx muted num">{{ i + 1 }}</span>
          <span class="nm"><Flag :code="s.country" /> {{ s.name }}</span>
          <span v-if="s.group" class="muted small grp">{{ s.group }}</span>
          <span class="mv">
            <button type="button" class="btn text icon" :disabled="i === 0" :aria-label="t('{name} 上移', { name: s.name })" @click="move(i, i - 1)"><Icon name="arrow-up" /></button>
            <button type="button" class="btn text icon" :disabled="i === arranging.length - 1" :aria-label="t('{name} 下移', { name: s.name })" @click="move(i, i + 1)"><Icon name="arrow-down" /></button>
          </span>
        </li>
      </ol>
      <p v-if="arrangeError" class="small err">{{ arrangeError }}</p>
      <div class="arrange-actions">
        <button type="button" class="btn" :disabled="saving" @click="saveArrange">{{ saving ? t('保存中…') : t('保存顺序') }}</button>
        <button type="button" class="btn secondary" :disabled="saving" @click="arranging = null">{{ t('取消') }}</button>
        <button type="button" class="btn text" :disabled="saving" @click="arranging!.sort((a, b) => a.name.localeCompare(b.name))">{{ t('按名称排列') }}</button>
      </div>
    </section>

    <p v-if="!state.loaded" class="muted">{{ t('加载中…') }}</p>
    <EmptyState v-else-if="!state.servers.length" :text="t('还没有节点。新建一个节点，按页面上的命令在主机上安装 Agent。')">
      <RouterLink to="/servers/new" class="btn"><Icon name="plus" />{{ t('新建节点') }}</RouterLink>
    </EmptyState>
    <template v-else>
      <div v-if="sorted.length" class="grid server-grid">
        <ServerCard v-for="s in sorted" :key="s.id" :server="s" />
      </div>
      <p v-else-if="installed.length" class="muted empty-filter">
        {{ statusFilter === 'all' ? t('没有匹配的节点。') : t('没有“{name}”的节点。', { name: t(filters.find((x) => x.v === statusFilter)!.t) }) }}
        <button v-if="statusFilter !== 'all'" type="button" class="text" @click="setFilter('all')">{{ t('查看全部') }}</button>
      </p>

      <!-- 待安装：不参与排序与异常统计，提供安装命令入口（设计 27.7） -->
      <section v-if="pendingFiltered.length" id="pending" class="section">
        <h3 class="muted">{{ t('待安装') }}</h3>
        <div class="grid">
          <div v-for="s in pendingFiltered" :key="s.id" class="pending-card">
            <div class="row">
              <strong class="name"><Flag :code="s.country" /> {{ s.name }}</strong>
              <StatusDot status="pending" />
            </div>
            <div class="muted small">{{ [s.region, s.provider, s.group].filter(Boolean).join(' · ') || t('尚未在主机上安装 Agent') }}</div>
            <div v-if="!isViewer" class="row actions">
              <RouterLink :to="`/servers/${s.id}/install`" class="btn secondary"><Icon name="terminal" />{{ t('安装命令') }}</RouterLink>
              <RouterLink :to="`/servers/${s.id}/edit`" class="btn text"><Icon name="edit" />{{ t('编辑') }}</RouterLink>
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
.chips { display: flex; gap: var(--space-1); flex-wrap: wrap; align-items: center; }
.chip { display: inline-flex; align-items: center; gap: 6px; padding: 4px var(--space-3); border-radius: var(--radius-full);
  background: var(--surface); color: var(--text-muted); border: 1px solid var(--border); font-size: var(--font-sm); text-decoration: none; }
.chip:hover { color: var(--text); border-color: var(--text-muted); text-decoration: none; filter: none; }
.chip.active { color: var(--text); border-color: var(--accent); box-shadow: inset 0 0 0 1px var(--accent); }
.chip b { color: var(--text); font-weight: var(--weight-strong); }
.chip b.ok { color: var(--ok); }
.chip b.bad { color: var(--bad); }
.chip b.warn { color: var(--warn); }
.empty-filter { display: flex; align-items: center; gap: var(--space-2); }
/* 手机：去掉“更新于”腾出标题行空间。手机主要用来查看（修订第 199 条）：新建节点在电脑上进行，按钮不显示；
   状态标签一行横向滚动，搜索与排序同一行 */
@media (max-width: 600px) {
  .updated { display: none; }
  .page-head { margin-bottom: var(--space-2); }
  .page-head h1 { margin-right: auto; font-size: var(--font-xl); }
  .page-head .btn { display: none; }
  .filters { gap: var(--space-2); margin-bottom: var(--space-3); }
  .chips { flex-wrap: nowrap; overflow-x: auto; scrollbar-width: none; flex-basis: 100%; }
  .chip { flex: none; padding: 3px var(--space-3); }
  /* 搜索独占一行，分组与排序在下一行平分，文字不截断（修订第 230 条） */
  .filters .search { flex: 1 1 100%; max-width: none; min-width: 0; }
  .filters select { flex: 1 1 0; width: auto; min-width: 0; max-width: none; }
  .filters .btn.secondary { display: none; }
  .grid.server-grid { grid-template-columns: minmax(0, 1fr); gap: var(--space-2); }
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
.arrange { margin-bottom: var(--space-4); }
.arrange-head { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-2); margin-bottom: var(--space-3); }
.arrange-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-1); }
.arrange-list li { display: flex; align-items: center; gap: var(--space-2); padding: var(--space-2) var(--space-3); border: 1px solid var(--border);
  border-radius: var(--radius-sm); background: var(--surface); }
.arrange-list li.dragging { border-color: var(--accent); box-shadow: 0 0 0 1px var(--accent); }
.arrange-list .grip { cursor: grab; touch-action: none; padding: var(--space-1); margin: calc(-1 * var(--space-1)); display: flex; }
.arrange-list li.dragging .grip { cursor: grabbing; }
.arrange-list .idx { width: 2ch; text-align: right; }
.arrange-list .nm { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.arrange-list .grp { white-space: nowrap; }
.arrange-list .mv { display: flex; gap: 2px; }
.arrange-list .icon { padding: var(--space-1); }
.arrange-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-top: var(--space-3); }
.err { color: var(--bad); }
@media (max-width: 600px) { .arrange-list .grp { display: none; } }
</style>
