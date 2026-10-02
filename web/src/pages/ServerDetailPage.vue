<script setup lang="ts">
// 节点详情（设计 11）。自上而下：概况 → 指标速览与实时网速 → CPU → 内存 → 磁盘 → 网络 → 流量 → 历史 → 系统与资产，
// 指标顺序与 App 一致（设计 41.6）。实时数据由本页每 3 秒轮询本节点；历史曲线按所选范围单独请求（设计 19.7、21）。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ApiError, getHistory, getServer, UnauthorizedError, type HistoryRange, type HistoryView, type ServerView } from '../api'
import Chart, { type Series } from '../components/Chart.vue'
import Flag from '../components/Flag.vue'
import Icon from '../components/Icon.vue'
import { countryName } from '../countries'
import LivePanels from '../components/LivePanels.vue'
import QuickTiles from '../components/QuickTiles.vue'
import ServerSummary from '../components/ServerSummary.vue'
import TrafficCard from '../components/TrafficCard.vue'
import { DASH, fmtBandwidth, fmtBytes, fmtPrice, fmtTime, periodNames } from '../format'
import { isLive, issues, type LiveSample } from '../metrics'
import { logout, serverById, state } from '../store'

const props = defineProps<{
  /** 路由参数中的节点 ID */
  id: string
}>()

const sid = computed(() => Number(props.id))
const s = computed(() => serverById(sid.value))
const live = computed(() => (s.value ? isLive(s.value) : false))
const problems = computed(() => (s.value ? issues(s.value) : []))
const sys = computed(() => s.value?.latest?.system)

// ---- 实时采样：迷你折线与实时网速图 ----
// 保留最近 10 分钟；打开页面时用 1 小时历史中最近 10 分钟的点预填，避免图表从空白开始。
// 相邻两点间隔超过 30 秒（Agent 停报）插入空点，折线在此断开。
const SAMPLE_WINDOW = 600
const samples = ref<LiveSample[]>([])
function pushSample(p: LiveSample) {
  const list = samples.value
  const last = list[list.length - 1]
  if (last && p.ts <= last.ts) return
  if (last && p.ts - last.ts > 30) list.push({ ts: last.ts + 10, cpu: null, mem: null, rx: null, tx: null })
  list.push(p)
  const cut = p.ts - SAMPLE_WINDOW
  while (list.length && list[0].ts < cut) list.shift()
}
function addSample(v: ServerView) {
  const r = v.latest
  if (!r || !isLive(v)) return
  pushSample({
    ts: r.timestamp, cpu: r.cpu.usage, mem: r.memory.usage, tcp: r.conns?.tcp,
    rx: r.network.reduce((a, n) => a + n.rx_speed, 0), tx: r.network.reduce((a, n) => a + n.tx_speed, 0),
  })
}
async function seedSamples() {
  const id = sid.value
  try {
    const h = await getHistory(id, '1h')
    if (id !== sid.value) return
    const cut = Date.now() / 1000 - SAMPLE_WINDOW
    const seeded: LiveSample[] = []
    for (const p of h.items.filter((p) => p.ts >= cut)) {
      const prev = seeded[seeded.length - 1]
      if (prev && p.ts - prev.ts > 30) seeded.push({ ts: prev.ts + 10, cpu: null, mem: null, rx: null, tx: null })
      seeded.push({ ts: p.ts, cpu: p.cpu, mem: p.mem_total ? (p.mem_used / p.mem_total) * 100 : null, rx: p.rx_speed, tx: p.tx_speed })
    }
    // 预填点在实时点之前：合并后按时间排序去重
    const live = samples.value.filter((x) => !seeded.length || x.ts > seeded[seeded.length - 1].ts)
    samples.value = [...seeded, ...live]
  } catch {
    /* 预填失败不影响实时采样 */
  }
}

// ---- 实时详情 ----
// 列表接口省略每核使用率等只在详情显示的字段，详情页单独轮询本节点（与全局列表同为 3 秒）
const detail = ref<ServerView | null>(null)
let liveTimer: number | undefined
async function loadDetail() {
  try {
    detail.value = await getServer(sid.value)
    addSample(detail.value)
  } catch (e) {
    if (e instanceof UnauthorizedError) logout()
    // 其他错误忽略：实时区块退回使用全局列表中的数据
  }
}
watch(sid, () => {
  detail.value = null
  samples.value = []
  seedSamples()
  loadDetail()
  if (liveTimer) clearInterval(liveTimer)
  liveTimer = window.setInterval(loadDetail, 3000)
}, { immediate: true })
onBeforeUnmount(() => liveTimer && clearInterval(liveTimer))

const liveServer = computed(() => (detail.value?.id === sid.value && detail.value.latest ? detail.value : s.value))

// ---- 历史曲线 ----
const ranges: HistoryRange[] = ['1h', '6h', '24h', '7d', '30d']
const range = ref<HistoryRange>('1h')
const history = ref<HistoryView | null>(null)
const historyError = ref('')
let timer: number | undefined

async function loadHistory() {
  try {
    history.value = await getHistory(sid.value, range.value)
    historyError.value = ''
  } catch (e) {
    if (e instanceof UnauthorizedError) logout()
    else if (e instanceof ApiError) historyError.value = e.message
  }
}
// 短范围每 30 秒刷新一次，长范围每 5 分钟（粒度粗，变化慢）
watch([sid, range], () => {
  loadHistory()
  if (timer) clearInterval(timer)
  timer = window.setInterval(loadHistory, range.value === '1h' || range.value === '6h' ? 30_000 : 300_000)
}, { immediate: true })
onBeforeUnmount(() => timer && clearInterval(timer))

const pts = computed(() => history.value?.items ?? [])
const coarse = computed(() => (history.value?.resolution ?? 10) > 10) // 聚合数据才有“峰值”
// 相邻两点间隔超过 3 个粒度视为没有数据（面板或 Agent 停机），插入 null 断开曲线，
// 否则图表会用直线把缺口连起来，看起来像真实数据
const line = (name: string, f: (p: (typeof pts.value)[number]) => number | null): Series => {
  const res = history.value?.resolution ?? 10
  const data: Series['data'] = []
  pts.value.forEach((p, i) => {
    const prev = pts.value[i - 1]
    if (prev && p.ts - prev.ts > res * 3) data.push([(prev.ts + res) * 1000, null])
    data.push([p.ts * 1000, f(p)])
  })
  return { name, data }
}
const charts = computed(() => [
  {
    title: 'CPU', max: 100, format: (v: number) => `${Math.round(v)}%`,
    series: coarse.value ? [line('平均', (p) => p.cpu), line('峰值', (p) => p.cpu_max)] : [line('CPU', (p) => p.cpu)],
  },
  {
    title: '内存', max: 100, format: (v: number) => `${Math.round(v)}%`,
    series: [line('内存', (p) => (p.mem_total ? (p.mem_used / p.mem_total) * 100 : null))],
  },
  {
    title: '网络', format: (v: number) => fmtBytes(v, true),
    series: [line('下行', (p) => p.rx_speed), line('上行', (p) => p.tx_speed)],
  },
  {
    title: '磁盘 IO', format: (v: number) => fmtBytes(v, true),
    series: [line('读', (p) => p.disk_read), line('写', (p) => p.disk_write)],
  },
  {
    title: '负载（1 分钟）', format: (v: number) => v.toFixed(v < 10 ? 2 : 0),
    series: [line('Load1', (p) => p.load1)],
  },
])

const expireDays = computed(() => {
  if (!s.value?.expire_date) return null
  return Math.ceil((new Date(s.value.expire_date + 'T00:00:00').getTime() - Date.now()) / 86400000)
})
</script>

<template>
  <main class="page">
    <div class="nav-row">
      <RouterLink to="/" class="back muted small"><Icon name="arrow-left" :size="14" />节点</RouterLink>
      <div v-if="s" class="actions-row">
        <RouterLink :to="`/servers/${s.id}/install`" class="btn secondary"><Icon name="terminal" />{{ s.status === 'pending' ? '安装命令' : '重新安装' }}</RouterLink>
        <RouterLink :to="`/servers/${s.id}/edit`" class="btn secondary"><Icon name="edit" />编辑</RouterLink>
      </div>
    </div>

    <p v-if="!state.loaded" class="muted">加载中…</p>
    <p v-else-if="!s" class="muted">节点不存在或已删除。<RouterLink to="/">返回列表</RouterLink></p>

    <template v-else>
      <div v-if="s.status === 'pending'" class="banner warn">
        该节点尚未安装 Agent。<RouterLink :to="`/servers/${s.id}/install`">查看安装命令</RouterLink>
      </div>
      <div v-else-if="problems.length" class="banner" :class="{ warn: problems[0].level === 'warn' }">
        {{ problems.map((p) => p.text).join(' · ') }}
        <template v-if="s.status === 'offline' && s.last_seen_at">，最后上报 {{ fmtTime(s.last_seen_at) }}</template>
      </div>

      <!-- 概况（设计 11.1） -->
      <ServerSummary :server="liveServer ?? s" :live="live" />

      <!-- 实时：速览 → CPU → 内存 → 磁盘 → 网络；离线时不显示旧数值（设计 43.6） -->
      <template v-if="live && liveServer?.latest">
        <section class="section">
          <QuickTiles :server="liveServer" :samples="samples" />
        </section>
        <section class="section">
          <LivePanels :server="liveServer" />
        </section>
      </template>

      <!-- 流量 -->
      <section class="section">
        <TrafficCard :server="s" @unauthorized="logout" />
      </section>

      <!-- 历史曲线（设计 11.2、41.5） -->
      <section class="section">
        <div class="section-head">
          <h3>历史</h3>
          <div class="segmented" role="group" aria-label="时间范围">
            <button v-for="r in ranges" :key="r" type="button" :class="{ active: range === r }" @click="range = r">{{ r.toUpperCase() }}</button>
          </div>
        </div>
        <p v-if="historyError" class="banner">{{ historyError }}</p>
        <p v-else-if="history && !pts.length" class="muted">这个时间范围内还没有数据。</p>
        <div v-else class="charts">
          <Chart v-for="c in charts" :key="c.title" :title="c.title" :series="c.series" :format="c.format" :max="c.max" />
        </div>
      </section>

      <!-- 系统与资产 -->
      <section class="section info-grid">
        <div class="panel">
          <h3>系统</h3>
          <dl class="kv">
            <dt>主机名</dt><dd>{{ sys?.hostname || s.hostname || DASH }}</dd>
            <dt>IPv4</dt><dd class="num">{{ s.ipv4 || s.expected_ipv4 || DASH }}</dd>
            <dt>IPv6</dt><dd class="num">{{ s.ipv6 || s.expected_ipv6 || DASH }}</dd>
            <dt>内核</dt><dd>{{ sys?.kernel || DASH }}</dd>
            <dt>Agent</dt><dd>{{ s.latest?.agent_version || DASH }}</dd>
            <dt>注册时间</dt><dd>{{ fmtTime(s.enrolled_at) }}</dd>
            <dt>最后上报</dt><dd>{{ fmtTime(s.last_seen_at) }}</dd>
          </dl>
        </div>
        <div class="panel">
          <h3>资产</h3>
          <dl class="kv">
            <dt>供应商</dt><dd>{{ s.provider || DASH }}</dd>
            <dt>套餐</dt><dd>{{ s.plan || DASH }}</dd>
            <dt>带宽</dt><dd>{{ fmtBandwidth(s.bandwidth_mbps) }}</dd>
            <dt>国家 / 地区</dt><dd>
              <template v-if="s.country"><Flag :code="s.country" /> {{ countryName(s.country) }}</template>
              <template v-else>{{ DASH }}</template>
            </dd>
            <dt>城市 / 机房</dt><dd>{{ s.region || DASH }}</dd>
            <dt>分组</dt><dd>{{ s.group || DASH }}</dd>
            <dt>续费</dt><dd>{{ fmtPrice(s.price_cents, s.currency) }}<template v-if="s.billing_period"> / {{ periodNames[s.billing_period] }}</template></dd>
            <dt>到期</dt>
            <dd>
              {{ s.expire_date || DASH }}
              <span v-if="expireDays != null" :class="{ bad: expireDays <= 3, warn: expireDays > 3 && expireDays <= 14 }">
                （{{ expireDays >= 0 ? `${expireDays} 天后` : `已过期 ${-expireDays} 天` }}）
              </span>
            </dd>
            <dt>备注</dt><dd>{{ s.note || DASH }}</dd>
          </dl>
        </div>
      </section>
    </template>
  </main>
</template>

<style scoped>
.nav-row { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); margin-bottom: var(--space-3); }
.actions-row { display: flex; gap: var(--space-2); }
.back { display: inline-flex; align-items: center; gap: var(--space-1); }
.title .row { gap: var(--space-3); }
.section-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: var(--space-3); gap: var(--space-3); flex-wrap: wrap; }
.charts { display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); gap: var(--space-3); }
.info-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: var(--space-3); }
.kv { display: grid; grid-template-columns: auto 1fr; gap: var(--space-2) var(--space-4); margin: var(--space-3) 0 0; }
.kv dt { color: var(--text-muted); font-size: var(--font-sm); }
.kv dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
@media (max-width: 600px) {
  .charts { grid-template-columns: 1fr; }
}
</style>
