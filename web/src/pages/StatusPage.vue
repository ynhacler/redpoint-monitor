<script setup lang="ts">
// 公开展示页（设计 45.3，修订第 303 条）：不用登录就能看的大屏，地址 /status，适合放在电视或副屏上常开。
// 一屏放下全部内容：汇总指标、地区分布与节点卡片，异常的节点排在前面；节点多到放不下时自动翻页。手机宽度改为上下滚动。
// 【安全】数据只来自 GET /api/v1/public/servers，只有管理员选中的节点与白名单字段；本页不读 store 中的节点，登录后打开看到的也一样。
// 标签：节点、订阅（管理员开启后才有，只读）、连通性测试（在访客自己的浏览器里运行）。云平台不在展示页中。
// 当前标签记在地址中（?tab=），电视可以固定打开某个标签。
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, getPublicServers, type PublicServer } from '../api'
import Flag from '../components/Flag.vue'
import WallSubscriptions from '../components/WallSubscriptions.vue'
import { countryName } from '../countries'
import { fmtAgo, fmtBytes, fmtPct, fmtTime, fmtUptimeShort } from '../format'
import { t } from '../i18n'
import { gaugeLevel } from '../metrics'
import { site } from '../site'

const ConnectivityPage = defineAsyncComponent(() => import('./ConnectivityPage.vue'))

const POLL_MS = 5000
const PAGE_MS = 12000

const items = ref<PublicServer[]>([])
const updatedAt = ref(0)
const loaded = ref(false)
const closed = ref(false) // 未开启
const lost = ref(false) // 最近一次刷新失败，显示的是之前的数据
const now = ref(Date.now())
const showSubs = ref(false)

const route = useRoute()
const router = useRouter()
const tabs = computed(() => [
  { k: 'nodes', t: t('节点') },
  ...(showSubs.value ? [{ k: 'subscriptions', t: t('订阅') }] : []),
  { k: 'connectivity', t: t('连通性测试') },
])
const tab = computed(() => (tabs.value.some((x) => x.k === route.query.tab) ? String(route.query.tab) : 'nodes'))
const pick = (k: string) => router.replace({ query: k === 'nodes' ? {} : { tab: k } })

async function load() {
  now.value = Date.now()
  try {
    const r = await getPublicServers()
    items.value = r.items
    updatedAt.value = r.updated_at
    showSubs.value = r.subscriptions
    closed.value = false
    lost.value = false
  } catch (e) {
    if (e instanceof ApiError && e.code === 'not_found') {
      closed.value = true
      items.value = []
      showSubs.value = false
    } else lost.value = true
  } finally {
    loaded.value = true
  }
}

const down = (s: PublicServer) => s.status === 'offline'
const levels = (s: PublicServer) => [gaugeLevel(s.cpu, 'cpu'), gaugeLevel(s.memory, 'mem'), gaugeLevel(s.disk, 'disk')]
// 异常优先：离线，然后是达到告警阈值的，再是接近阈值的；同级保持面板的顺序
const tier = (s: PublicServer) => (down(s) ? 0 : levels(s).includes('bad') ? 1 : s.status === 'unknown' || levels(s).includes('warn') ? 2 : 3)
const sorted = computed(() => items.value.map((s, i) => ({ s, key: i })).sort((a, b) => tier(a.s) - tier(b.s) || a.key - b.key))

const avg = (f: (s: PublicServer) => number | undefined) => {
  const v = items.value.map(f).filter((x): x is number => x != null)
  return v.length ? v.reduce((a, b) => a + b, 0) / v.length : undefined
}
const sum = (f: (s: PublicServer) => number | undefined) => items.value.reduce((a, s) => a + (f(s) ?? 0), 0)
const online = computed(() => items.value.filter((s) => s.status === 'online').length)
const attention = computed(() => items.value.filter((s) => tier(s) <= 1).length)
const avgCpu = computed(() => avg((s) => s.cpu))
const avgMem = computed(() => avg((s) => s.memory))
const regions = computed(() => {
  const m = new Map<string, number>()
  for (const s of items.value) if (s.country) m.set(s.country, (m.get(s.country) ?? 0) + 1)
  return [...m].sort((a, b) => b[1] - a[1]).slice(0, 12)
})

const bars = (s: PublicServer) => [
  { k: 'CPU', v: s.cpu, level: gaugeLevel(s.cpu, 'cpu') },
  { k: t('内存'), v: s.memory, level: gaugeLevel(s.memory, 'mem') },
  { k: t('磁盘'), v: s.disk, level: gaugeLevel(s.disk, 'disk') },
]

// ---- 放不下时翻页：每页的数量按实际的卡片高度与列数计算 ----
const grid = ref<HTMLElement>()
const perPage = ref(Number.POSITIVE_INFINITY)
const page = ref(0)
const pages = computed(() => Math.max(1, Math.ceil(sorted.value.length / perPage.value)))
const shown = computed(() => (pages.value === 1 ? sorted.value : sorted.value.slice(page.value * perPage.value, (page.value + 1) * perPage.value)))
const phone = window.matchMedia('(max-width: 600px)')

function measure() {
  const el = grid.value
  const first = el?.firstElementChild as HTMLElement | null | undefined
  if (phone.matches) perPage.value = Number.POSITIVE_INFINITY
  if (!el || !first || phone.matches) return
  const cs = getComputedStyle(el)
  const gap = parseFloat(cs.rowGap) || 0
  const rows = Math.max(1, Math.floor((el.clientHeight + gap) / (first.offsetHeight + gap)))
  perPage.value = cs.gridTemplateColumns.split(' ').length * rows
}
watch(pages, (n) => {
  if (page.value >= n) page.value = 0
})
watch(() => items.value.length > 0, () => nextTick(measure))

let poll: ReturnType<typeof setInterval>
let flip: ReturnType<typeof setInterval>
let observer: ResizeObserver | undefined
const robots = document.createElement('meta')
robots.name = 'robots'
robots.content = 'noindex'

onMounted(() => {
  document.head.append(robots)
  load()
  poll = setInterval(load, POLL_MS)
  flip = setInterval(() => (page.value = (page.value + 1) % pages.value), PAGE_MS)
  observer = new ResizeObserver(measure)
})
watch(grid, (el) => {
  observer?.disconnect()
  if (el) observer?.observe(el)
})
onUnmounted(() => {
  robots.remove()
  clearInterval(poll)
  clearInterval(flip)
  observer?.disconnect()
})
</script>

<template>
  <div class="wall" :class="{ scroll: tab !== 'nodes' }">
    <header class="head">
      <h1>{{ site.app_name }}</h1>
      <nav v-if="loaded && !closed" class="tabs" :aria-label="t('分组')">
        <button v-for="x in tabs" :key="x.k" type="button" :class="{ active: tab === x.k }" :aria-current="tab === x.k ? 'page' : undefined" @click="pick(x.k)">{{ x.t }}</button>
      </nav>
      <p class="clock num">
        <span v-if="lost" class="bad">{{ t('连接中断，显示的是 {ago}的数据', { ago: fmtAgo(updatedAt, now / 1000) }) }}</span>
        <span v-else-if="updatedAt" class="muted">{{ t('更新于 {ago}', { ago: fmtAgo(updatedAt, now / 1000) }) }}</span>
        <strong>{{ fmtTime(now / 1000) }}</strong>
      </p>
    </header>

    <p v-if="!loaded" class="notice muted">{{ t('加载中…') }}</p>
    <div v-else-if="closed" class="notice">
      <h2>{{ t('展示页未开启') }}</h2>
      <p class="muted">{{ t('管理员可以在“系统 → 网站配置”中开启') }}</p>
    </div>
    <WallSubscriptions v-else-if="tab === 'subscriptions'" />
    <ConnectivityPage v-else-if="tab === 'connectivity'" />
    <p v-else-if="!items.length" class="notice muted">{{ t('还没有公开的节点') }}</p>

    <template v-else>
      <section class="kpis" :aria-label="t('汇总')">
        <div class="kpi"><span>{{ t('在线节点') }}</span><b class="num" :class="{ bad: online < items.length }">{{ online }}<small> / {{ items.length }}</small></b></div>
        <div class="kpi"><span>{{ t('需要关注') }}</span><b class="num" :class="attention ? 'bad' : 'ok'">{{ attention }}</b></div>
        <div class="kpi"><span>{{ t('平均 CPU') }}</span><b class="num">{{ fmtPct(avgCpu) }}</b></div>
        <div class="kpi"><span>{{ t('平均内存') }}</span><b class="num">{{ fmtPct(avgMem) }}</b></div>
        <div class="kpi"><span>{{ t('总下行') }}</span><b class="num">{{ fmtBytes(sum((s) => s.rx_speed), true) }}</b></div>
        <div class="kpi"><span>{{ t('总上行') }}</span><b class="num">{{ fmtBytes(sum((s) => s.tx_speed), true) }}</b></div>
      </section>

      <ul v-if="regions.length" class="regions num" :aria-label="t('地区分布')">
        <li v-for="[c, n] in regions" :key="c" :title="countryName(c)"><Flag :code="c" /> {{ n }}</li>
      </ul>

      <section ref="grid" class="grid" :class="{ center: pages === 1 }" :aria-label="t('节点')">
        <article v-for="{ s, key } in shown" :key="key" class="node" :class="{ down: down(s) }">
          <div class="title">
            <i class="dot" :class="down(s) ? 'bad' : s.status === 'unknown' ? 'warn' : 'ok'" aria-hidden="true" />
            <Flag :code="s.country" />
            <h2 :title="s.name">{{ s.name }}</h2>
            <span v-if="down(s)" class="state bad">{{ t('离线') }}</span>
            <span v-else-if="s.status === 'unknown'" class="state warn">{{ t('状态未知') }}</span>
          </div>
          <div v-for="b in bars(s)" :key="b.k" class="meter">
            <span class="k">{{ b.k }}</span>
            <span class="track" aria-hidden="true"><span class="fill" :class="b.level" :style="{ width: `${Math.min(100, b.v ?? 0)}%` }" /></span>
            <span class="v num" :class="b.level === 'ok' ? '' : b.level">{{ fmtPct(b.v) }}</span>
          </div>
          <p class="foot num">
            <span>↓ {{ fmtBytes(s.rx_speed, true) }}</span>
            <span>↑ {{ fmtBytes(s.tx_speed, true) }}</span>
            <span v-if="s.traffic_pct != null">{{ t('流量') }} {{ fmtPct(s.traffic_pct) }}</span>
            <span v-if="s.uptime != null" class="up">{{ fmtUptimeShort(s.uptime) }}</span>
          </p>
        </article>
      </section>

      <p v-if="pages > 1" class="pager num muted">{{ page + 1 }} / {{ pages }}</p>
    </template>
  </div>
</template>

<style scoped>
/* 大屏上整体放大，3 米外也看得清；--z 同时用来修正高度（zoom 会放大 100dvh） */
.wall { --z: 1; zoom: var(--z); height: calc(100dvh / var(--z)); display: flex; flex-direction: column; gap: var(--space-4); padding: var(--space-5) var(--space-6);
  background: var(--bg); overflow: hidden; }
@media (min-width: 1800px) { .wall { --z: 1.25; } }
@media (min-width: 2500px) { .wall { --z: 1.75; } }
@media (min-width: 3400px) { .wall { --z: 2.5; } }
.wall.scroll { overflow-y: auto; }
.head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); flex: none; }
.tabs { display: flex; gap: var(--space-1); margin-right: auto; }
.tabs button { background: none; border: 0; color: var(--text-muted); padding: var(--space-1) var(--space-3); border-radius: var(--radius-sm); cursor: pointer; white-space: nowrap; }
.tabs button.active { background: var(--surface-2); color: var(--text); font-weight: var(--weight-strong); }
.head h1 { margin: 0; font-size: var(--font-xl); line-height: var(--line-xl); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.clock { margin: 0; display: flex; align-items: baseline; gap: var(--space-3); white-space: nowrap; }
.clock strong { font-size: var(--font-xl); }
.notice { margin: auto; text-align: center; }
.notice h2, .notice p { margin: 0 0 var(--space-1); }
.kpis { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: var(--space-3); }
.kpi { display: flex; flex-direction: column; gap: var(--space-1); padding: var(--space-3) var(--space-4); background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md); min-width: 0; }
.kpi span { color: var(--text-muted); }
.kpi b { font-size: var(--font-num); line-height: var(--line-num); font-weight: var(--weight-strong); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.kpi small { font-size: var(--font-lg); color: var(--text-muted); font-weight: var(--weight-regular); }
.regions { list-style: none; margin: 0; padding: 0; display: flex; gap: var(--space-4); overflow: hidden; color: var(--text-muted); }
.regions li { display: flex; align-items: center; gap: var(--space-1); white-space: nowrap; }
.grid { flex: 1; min-height: 0; overflow: hidden; display: grid; grid-template-columns: repeat(auto-fit, minmax(var(--card-min), 1fr)); grid-auto-rows: min-content; align-content: start; gap: var(--space-3); }
/* 节点少时放在中间，不全挤在上面 */
.grid.center { align-content: safe center; }
.node { display: flex; flex-direction: column; gap: var(--space-2); padding: var(--space-3) var(--space-4); background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md); min-width: 0; }
.node.down { border-color: var(--bad); }
.title { display: flex; align-items: center; gap: var(--space-2); min-width: 0; }
.title h2 { flex: 1; min-width: 0; margin: 0; font-size: var(--font-lg); line-height: var(--line-lg); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dot { width: 8px; height: 8px; border-radius: var(--radius-full); flex: none; }
.dot.ok { background: var(--ok); }
.dot.warn { background: var(--warn); }
.dot.bad { background: var(--bad); }
.state { flex: none; font-size: var(--font-sm); }
.meter { display: grid; grid-template-columns: 4.5em minmax(0, 1fr) 3.5em; align-items: center; gap: var(--space-2); }
.meter .k { color: var(--text-muted); white-space: nowrap; }
.meter .v { text-align: right; white-space: nowrap; }
.track { height: 6px; border-radius: var(--radius-full); background: var(--track); overflow: hidden; }
.fill { display: block; height: 100%; border-radius: var(--radius-full); background: var(--ok); transition: width .4s; }
.fill.warn { background: var(--warn); }
.fill.bad { background: var(--bad); }
.foot { margin: 0; display: flex; gap: var(--space-3); color: var(--text-muted); font-size: var(--font-sm); line-height: var(--line-sm); white-space: nowrap; overflow: hidden; }
.foot .up { margin-left: auto; }
.pager { margin: 0; text-align: center; }
.ok { color: var(--ok); }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
.muted { color: var(--text-muted); }
@media (prefers-reduced-motion: reduce) { .fill { transition: none; } }
/* 手机：上下滚动，不翻页 */
@media (max-width: 600px) {
  .wall, .wall.scroll { height: auto; min-height: 100dvh; padding: var(--space-4); overflow: visible; }
  .head { flex-wrap: wrap; }
  .tabs { order: 3; width: 100%; overflow-x: auto; }
  .clock .muted, .clock .bad { font-size: var(--font-sm); }
  .kpis { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .regions { flex-wrap: wrap; gap: var(--space-2) var(--space-3); }
  .grid, .grid.center { flex: none; overflow: visible; grid-template-columns: minmax(0, 1fr); align-content: start; }
}
</style>
