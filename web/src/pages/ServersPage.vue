<script setup lang="ts">
// 节点（设计 9、10）：首页。顶部为统计，点击即按状态筛选（总览与列表合并，修订记录第 30 条）；
// 下方搜索、按分组筛选、排序（默认异常优先，设计 1.5.6）；待安装节点单独一组（设计 27.7）。
// 状态筛选写在地址中（?f=offline），刷新与分享后保持。排序方式记在本机浏览器（localStorage）。
// 手机（手机 UI 规范 3.1、3.3、3.4）：分组、供应商、线路、到期等低频条件合并到“筛选”底部抽屉（入口显示生效的条件数），
// 排序改为底部抽屉单选；电脑上仍为一排下拉框。
// “自定义”排序按面板保存的顺序（修订第 150 条）：点“调整顺序”进入排列模式，拖动或用上下按钮排列后保存到面板，
// 所有浏览器与（本机没有自己顺序的）App 共用。
import NotifyWarning from '../components/NotifyWarning.vue'
import FleetCards from '../components/FleetCards.vue'
import TodoCard from '../components/TodoCard.vue'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { errorText, putServerOrder, type ServerView } from '../api'
import EmptyState from '../components/EmptyState.vue'
import Flag from '../components/Flag.vue'
import { countryName } from '../countries'
import { badgeWords, lineFilterOptions, matchLineFilter } from '../lines'
import Icon from '../components/Icon.vue'
import ServerCard from '../components/ServerCard.vue'
import ServerTable from '../components/ServerTable.vue'
import BulkEditPanel from '../components/BulkEditPanel.vue'
import BottomSheet from '../components/BottomSheet.vue'
import { daysUntil } from '../format'
import { cardFieldOptions, cardHidden, resetCardFields, toggleCardField } from '../cardFields'
import StatusDot from '../components/StatusDot.vue'
import { abnormalTier, cpu, diskSummary, issues, mem, trafficPct } from '../metrics'
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
  router.replace({ query: { ...route.query, f: v === 'all' ? undefined : v }, hash: route.hash })
}

// 搜索与分组也写在地址中（修订第 230 条）：从节点详情返回时保持
const query = ref(String(route.query.q ?? ''))
const group = ref(String(route.query.g ?? ''))
const line = ref(String(route.query.l ?? '')) // 线路 / 协议栈筛选（修订第 280 条）
const provider = ref(String(route.query.p ?? '')) // 供应商（手机 UI 规范 3.4）
const allChip = { v: '', t: '全部' }
const expiryOptions = [{ v: '7', t: '7 天内到期' }, { v: '30', t: '30 天内到期' }] as const
const expiry = ref(expiryOptions.some((o) => o.v === route.query.e) ? String(route.query.e) : '') // 到期范围（含已过期）
// 路由的 hash 保留：底部抽屉打开时为 #sheet（系统返回关闭抽屉）
function writeQuery() {
  const [q, g, l, p, e] = [query.value, group.value, line.value, provider.value, expiry.value]
  router.replace({ query: { ...route.query, q: q || undefined, g: g || undefined, l: l || undefined, p: p || undefined, e: e || undefined },
    hash: route.hash })
}
watch([query, group, line, provider, expiry], writeQuery)
// 抽屉中改的条件写在 #sheet 那一项历史上；关闭抽屉（返回到打开前的那一项）后重新写入地址，刷新与分享时保持
watch(() => route.hash, (h, old) => {
  if (old === '#sheet' && h !== '#sheet') writeQuery()
})
// 排序（手机 UI 规范 3.3）：t 为抽屉与电脑下拉中的完整说明（含方向），short 为入口按钮上的简称
const sorts = [
  { v: 'abnormal', t: '异常优先', short: '异常优先' },
  { v: 'expiry', t: '到期时间（最近优先）', short: '到期时间' },
  { v: 'cpu', t: 'CPU 使用率（高→低）', short: 'CPU' },
  { v: 'mem', t: '内存使用率（高→低）', short: '内存' },
  { v: 'disk', t: '磁盘使用率（高→低）', short: '磁盘' },
  { v: 'traffic', t: '流量使用率（高→低）', short: '流量' },
  { v: 'name', t: '名称（A→Z）', short: '名称' },
  { v: 'custom', t: '自定义顺序', short: '自定义' },
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
// 卡片 / 表格（修订第 239 条）：表格只在宽度 ≥ 900px 时可选并显示，手机始终为卡片；选择记在本机
const layout = ref<'cards' | 'table'>(readLayout())
function readLayout(): 'cards' | 'table' {
  try {
    return localStorage.getItem('vpsmon.serversLayout') === 'table' ? 'table' : 'cards'
  } catch {
    return 'cards'
  }
}
function setLayout(v: 'cards' | 'table') {
  layout.value = v
  try {
    localStorage.setItem('vpsmon.serversLayout', v)
  } catch {
    // 无痕模式等：只是不记住
  }
}
const sortFromTable = (k: string) => {
  if (sorts.some((x) => x.v === k)) sortBy.value = k as SortKey
}
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
const providers = computed(() => [...new Set(installed.value.map((s) => s.provider).filter(Boolean))].sort())
const lineOptions = computed(() => lineFilterOptions(installed.value))
const hasExpiry = computed(() => installed.value.some((s) => s.expire_date))
// 生效的低频筛选条件数（显示在“筛选”入口上）与一键清除
const activeFilters = computed(() => [group.value, provider.value, line.value, expiry.value].filter(Boolean).length)
function clearFilters() {
  group.value = provider.value = line.value = expiry.value = ''
}
function clearAll() {
  clearFilters()
  query.value = ''
  setFilter('all')
}
const sheet = ref<'' | 'filter' | 'sort' | 'fields'>('') // 底部抽屉（手机）/ 对话框（电脑）
const sortShort = computed(() => sorts.find((x) => x.v === sortBy.value)!.short)
function pickSort(v: SortKey) {
  sortBy.value = v
  sheet.value = ''
}
const bulk = ref(false) // 批量修改面板（修订第 281 条）

// 搜索名称、主机名、IP、分组、供应商、地区、线路与 v4 / v6，不区分大小写
const matches = (s: ServerView) => {
  const q = query.value.trim().toLowerCase()
  if (group.value && s.group !== group.value) return false
  if (provider.value && s.provider !== provider.value) return false
  if (!matchLineFilter(s, line.value)) return false
  if (expiry.value) {
    const d = daysUntil(s.expire_date)
    if (d == null || d > Number(expiry.value)) return false
  }
  if (!q) return true
  // 国家可按代码或中文名搜索，如 “jp”“日本”
  return [s.name, s.hostname, s.ipv4, s.ipv6, s.expected_ipv4, s.group, s.provider, s.region, s.country, countryName(s.country), ...badgeWords(s)]
    .some((v) => v?.toLowerCase().includes(q))
}

const desc = (f: (s: ServerView) => number | undefined) => (a: ServerView, b: ServerView) => (f(b) ?? -1) - (f(a) ?? -1)
const sorted = computed(() => {
  const test = filters.find((x) => x.v === statusFilter.value)!.test
  const list = installed.value.filter((s) => test(s) && matches(s))
  const byName = (a: ServerView, b: ServerView) => a.name.localeCompare(b.name)
  const cmp: Record<typeof sortBy.value, (a: ServerView, b: ServerView) => number> = {
    // 异常优先（手机 UI 规范 3.3）：离线 → 上报延迟 / 严重告警 → 流量用完 / 7 天内到期 → 其他告警 → 正常；
    // 同级按问题数，再按名称
    abnormal: (a, b) =>
      abnormalTier(a) - abnormalTier(b) || statusRank[a.status] - statusRank[b.status] || issues(b).length - issues(a).length || byName(a, b),
    // 到期时间：最近（含已过期）在前，没有到期日的排在最后
    expiry: (a, b) => (daysUntil(a.expire_date) ?? Infinity) - (daysUntil(b.expire_date) ?? Infinity),
    cpu: desc(cpu),
    mem: desc(mem),
    disk: desc((s) => diskSummary(s)?.usage),
    traffic: desc(trafficPct),
    name: byName,
    custom: byCustom,
  }
  // 值相同（或都缺失，差为 NaN）时按名称，排序稳定
  return list.sort((a, b) => cmp[sortBy.value](a, b) || byName(a, b))
})
const filtering = computed(() => statusFilter.value !== 'all' || activeFilters.value > 0 || query.value.trim() !== '')
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
    <!-- 待处理（修订第 241 条） -->
    <TodoCard />
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
      <!-- 电脑：低频条件为一排下拉框；手机上收进“筛选”抽屉 -->
      <select v-if="groups.length || group" v-model="group" class="inline-filter" :aria-label="t('分组')">
        <option value="">{{ t('全部分组') }}</option>
        <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
      </select>
      <select v-if="providers.length > 1 || provider" v-model="provider" class="inline-filter" :aria-label="t('供应商')">
        <option value="">{{ t('全部供应商') }}</option>
        <option v-for="p in providers" :key="p" :value="p">{{ p }}</option>
      </select>
      <!-- 线路标签与协议栈：有可选项时才显示；选中的值已不存在时仍保留，便于看出为何没有节点 -->
      <select v-if="lineOptions.length || line" v-model="line" class="inline-filter" :aria-label="t('网络线路')">
        <option value="">{{ t('全部线路') }}</option>
        <option v-for="o in lineOptions" :key="o.v" :value="o.v">{{ t(o.t) }}</option>
        <option v-if="line && !lineOptions.some((o) => o.v === line)" :value="line">{{ line.replace(/^\w+:/, '') }}</option>
      </select>
      <select v-if="hasExpiry || expiry" v-model="expiry" class="inline-filter" :aria-label="t('到期')">
        <option value="">{{ t('全部到期时间') }}</option>
        <option v-for="o in expiryOptions" :key="o.v" :value="o.v">{{ t(o.t) }}</option>
      </select>
      <button v-if="activeFilters" type="button" class="btn text inline-filter" @click="clearFilters">{{ t('清除筛选') }}</button>
      <!-- 手机：筛选与排序入口 -->
      <button type="button" class="btn secondary sheet-btn" :class="{ on: activeFilters }" @click="sheet = 'filter'">
        <Icon name="filter" />{{ t('筛选') }}<b v-if="activeFilters" class="count num">{{ activeFilters }}</b>
      </button>
      <button type="button" class="btn secondary sheet-btn sort-btn" :aria-label="t('排序：{name}', { name: t(sortShort) })" @click="sheet = 'sort'">
        <Icon name="sort" />{{ t(sortShort) }}
      </button>
      <div class="segmented layout-switch wide-only" role="group" :aria-label="t('显示方式')">
        <button type="button" :class="{ active: layout === 'cards' }" :aria-pressed="layout === 'cards'" @click="setLayout('cards')">{{ t('卡片') }}</button>
        <button type="button" :class="{ active: layout === 'table' }" :aria-pressed="layout === 'table'" @click="setLayout('table')">{{ t('表格') }}</button>
      </div>
      <label class="sort-select inline-filter">
        <span class="muted">{{ t('排序') }}</span>
        <select v-model="sortBy">
          <option v-for="o in sorts" :key="o.v" :value="o.v">{{ t(o.t) }}</option>
        </select>
      </label>
      <!-- 卡片显示字段（修订第 288 条）：电脑上只在卡片视图出现，手机上只显示图标 -->
      <button v-if="installed.length" type="button" class="btn secondary fields-btn" :class="{ 'narrow-only': layout === 'table', on: cardHidden.size }"
        :aria-label="t('卡片显示')" :title="t('卡片显示')" @click="sheet = 'fields'">
        <Icon name="sliders" /><span class="wide-only">{{ t('卡片显示') }}</span>
      </button>
      <button v-if="sortBy === 'custom' && installed.length > 1 && !arranging && !isViewer" type="button" class="btn secondary" @click="startArrange">
        <Icon name="grip" />{{ t('调整顺序') }}
      </button>
      <button v-if="installed.length > 1 && !arranging && !bulk && !isViewer" type="button" class="btn secondary bulk-btn"
        :aria-label="t('批量修改')" :title="t('批量修改')" @click="bulk = true">
        <Icon name="edit" /><span class="wide-only">{{ t('批量修改') }}</span>
      </button>
    </div>

    <BottomSheet :open="sheet === 'filter'" :title="t('筛选节点')" @close="sheet = ''">
      <fieldset v-if="groups.length || group" class="sheet-group">
        <legend>{{ t('分组') }}</legend>
        <div class="chips wrap">
          <button v-for="g in ['', ...groups]" :key="g" type="button" class="chip" :class="{ active: group === g }" :aria-pressed="group === g" @click="group = g">{{ g || t('全部') }}</button>
        </div>
      </fieldset>
      <fieldset v-if="providers.length > 1 || provider" class="sheet-group">
        <legend>{{ t('供应商') }}</legend>
        <div class="chips wrap">
          <button v-for="p in ['', ...providers]" :key="p" type="button" class="chip" :class="{ active: provider === p }" :aria-pressed="provider === p" @click="provider = p">{{ p || t('全部') }}</button>
        </div>
      </fieldset>
      <fieldset v-if="lineOptions.length || line" class="sheet-group">
        <legend>{{ t('网络线路') }}</legend>
        <div class="chips wrap">
          <button v-for="o in [allChip, ...lineOptions]" :key="o.v" type="button" class="chip" :class="{ active: line === o.v }" :aria-pressed="line === o.v" @click="line = o.v">{{ t(o.t) }}</button>
        </div>
      </fieldset>
      <fieldset v-if="hasExpiry || expiry" class="sheet-group">
        <legend>{{ t('到期') }}</legend>
        <div class="chips wrap">
          <button v-for="o in [allChip, ...expiryOptions]" :key="o.v" type="button" class="chip" :class="{ active: expiry === o.v }" :aria-pressed="expiry === o.v" @click="expiry = o.v">{{ t(o.t) }}</button>
        </div>
      </fieldset>
      <p v-if="!groups.length && providers.length < 2 && !lineOptions.length && !hasExpiry" class="muted small">{{ t('节点设置了分组、供应商、线路或到期日后，可以在这里筛选。') }}</p>
      <template #footer>
        <button type="button" class="btn secondary" :disabled="!activeFilters" @click="clearFilters">{{ t('重置') }}</button>
        <button type="button" class="btn grow" @click="sheet = ''">{{ t('查看 {n} 台结果', { n: sorted.length }) }}</button>
      </template>
    </BottomSheet>

    <BottomSheet :open="sheet === 'sort'" :title="t('节点排序')" @close="sheet = ''">
      <ul class="sort-list" role="listbox" :aria-label="t('节点排序')">
        <li v-for="o in sorts" :key="o.v" role="option" :aria-selected="sortBy === o.v">
          <button type="button" :class="{ active: sortBy === o.v }" @click="pickSort(o.v)">
            <span>{{ t(o.t) }}</span><Icon v-if="sortBy === o.v" name="check" />
          </button>
        </li>
      </ul>
    </BottomSheet>

    <BottomSheet :open="sheet === 'fields'" :title="t('卡片显示')" @close="sheet = ''">
      <p class="muted small fields-note">{{ t('名称、状态与 CPU / 内存 / 磁盘始终显示。设置只保存在本浏览器。') }}</p>
      <ul class="sort-list fields-list">
        <li v-for="o in cardFieldOptions" :key="o.k">
          <label>
            <span>{{ t(o.t) }}<small v-if="o.hint" class="muted">{{ t(o.hint) }}</small></span>
            <input type="checkbox" :checked="!cardHidden.has(o.k)" @change="toggleCardField(o.k)" />
          </label>
        </li>
      </ul>
      <template #footer>
        <button type="button" class="btn secondary" :disabled="!cardHidden.size" @click="resetCardFields">{{ t('全部显示') }}</button>
        <button type="button" class="btn grow" @click="sheet = ''">{{ t('完成') }}</button>
      </template>
    </BottomSheet>

    <BulkEditPanel v-if="bulk" :servers="sorted" :groups="groups" @close="bulk = false" />

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
      <template v-if="sorted.length">
        <ServerTable v-if="layout === 'table'" class="wide-only" :servers="sorted" :sort-by="sortBy" @sort="sortFromTable" />
        <div class="grid server-grid" :class="{ 'narrow-only': layout === 'table' }">
          <ServerCard v-for="s in sorted" :key="s.id" :server="s" />
        </div>
      </template>
      <!-- 没有结果：说明是搜索还是筛选造成的，并可一键清除（手机 UI 规范 3.4） -->
      <p v-else-if="installed.length" class="muted empty-filter">
        {{ query.trim() ? t('没有匹配“{q}”的节点。', { q: query.trim() })
          : statusFilter !== 'all' && !activeFilters ? t('没有“{name}”的节点。', { name: t(filters.find((x) => x.v === statusFilter)!.t) })
          : t('没有符合筛选条件的节点。') }}
        <button v-if="filtering" type="button" class="text" @click="clearAll">{{ t('清除搜索与筛选') }}</button>
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
/* 筛选与排序：电脑为下拉框，手机为抽屉入口（手机 UI 规范 3.1） */
.sheet-btn { display: none; }
.sort-select { display: inline-flex; align-items: center; gap: var(--space-2); }
.sort-select select { width: auto; }
.sheet-btn .count { min-width: 18px; height: 18px; padding: 0 5px; border-radius: var(--radius-full); background: var(--accent); color: var(--on-accent);
  font-size: var(--font-xs); line-height: 1.125rem; text-align: center; }
.sheet-btn.on { border-color: var(--accent); color: var(--accent); }
.sheet-group { border: 0; margin: 0; padding: var(--space-3) 0 0; min-width: 0; }
.sheet-group legend { padding: 0; margin-bottom: var(--space-2); font-size: var(--font-sm); color: var(--text-muted); }
.chips.wrap { flex-wrap: wrap; overflow: visible; }
.chips.wrap .chip { min-height: 36px; }
.grow { flex: 1; justify-content: center; }
.sort-list { list-style: none; margin: 0; padding: 0; }
.sort-list button { display: flex; align-items: center; justify-content: space-between; width: 100%; min-height: 48px; padding: 0 var(--space-1);
  background: none; border: 0; border-bottom: 1px solid var(--border); color: var(--text); font: inherit; text-align: left; cursor: pointer; }
.sort-list li:last-child button { border-bottom: 0; }
/* 卡片显示字段：每行一个开关，整行可点 */
.fields-btn.on { border-color: var(--accent); color: var(--accent); }
.fields-note { margin: var(--space-2) 0 0; }
.fields-list label { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); min-height: 48px;
  padding: var(--space-1); border-bottom: 1px solid var(--border); cursor: pointer; }
.fields-list li:last-child label { border-bottom: 0; }
.fields-list label > span { display: flex; flex-direction: column; }
.fields-list small { font-size: var(--font-xs); }
.fields-list input { flex: none; width: 20px; height: 20px; accent-color: var(--accent); }
.sort-list button.active { color: var(--accent); font-weight: var(--weight-strong); }
/* 表格视图只在宽屏（修订第 239 条） */
@media (max-width: 899px) { .wide-only { display: none !important; } }
@media (min-width: 900px) { .narrow-only { display: none !important; } }
/* 统计：一行等宽，窄屏横向滚动而不是换行，保持紧凑 */
.updated { margin-left: auto; margin-right: var(--space-3); }
.chips { display: flex; gap: var(--space-1); flex-wrap: wrap; align-items: center; }
.chip { display: inline-flex; align-items: center; gap: 6px; padding: 4px var(--space-3); border-radius: var(--radius-full);
  background: var(--surface); color: var(--text-muted); border: 1px solid var(--border); font-size: var(--font-sm); text-decoration: none; }
@media (hover: hover) { .chip:hover { color: var(--text); border-color: var(--text-muted); text-decoration: none; filter: none; } } /* 触摸屏点按后 :hover 会停留，只在真正能悬停的设备上加深边框（修订第 262 条） */
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
  /* 低频条件收进“筛选”抽屉，排序为抽屉；两个入口与批量修改在同一行（手机 UI 规范 3.1） */
  .filters .inline-filter { display: none; }
  .filters .btn.secondary { display: none; }
  .filters .btn.secondary.sheet-btn { display: inline-flex; flex: 1 1 0; min-width: 0; min-height: 40px; justify-content: center; }
  .filters .btn.secondary.fields-btn { display: inline-flex; flex: none; min-height: 40px; padding: 0 var(--space-3); }
  .filters .btn.secondary.bulk-btn { display: inline-flex; flex: none; min-height: 40px; padding: 0 var(--space-3); } /* 批量修改在手机上也可用，只显示图标 */
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
