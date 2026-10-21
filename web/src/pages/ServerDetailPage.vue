<script setup lang="ts">
// 节点详情（设计 11）。顶部：提示、静音与维护、概况、健康摘要；其下分页（修订第 172 条）：监控（速览 → CPU → 内存 → 磁盘 →
// 网络 → 历史，指标顺序与 App 一致，设计 41.6）、流量、网络、容器与代理、安全（管理员）、告警。实时数据由本页每 3 秒轮询本节点；历史曲线按所选范围单独请求（设计 19.7、21）。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { errorText, getHistory, getServer, type HistoryRange, type HistoryView, type ServerView } from '../api'
import AlertHistory from '../components/AlertHistory.vue'
import Chart, { type Series } from '../components/Chart.vue'
import CloudInstanceCard from '../components/CloudInstanceCard.vue'
import ContainersCard from '../components/ContainersCard.vue'
import FirewallCard from '../components/FirewallCard.vue'
import PortBaselineCard from '../components/PortBaselineCard.vue'
import ProxiesCard from '../components/ProxiesCard.vue'
import HealthCard from '../components/HealthCard.vue'
import LatencyCard from '../components/LatencyCard.vue'
import ReachCard from '../components/ReachCard.vue'
import Icon from '../components/Icon.vue'
import LivePanels from '../components/LivePanels.vue'
import ServerSummary from '../components/ServerSummary.vue'
import SilenceControls from '../components/SilenceControls.vue'
import TrafficCard from '../components/TrafficCard.vue'
import TrafficHistory from '../components/TrafficHistory.vue'
import { fmtBytes, fmtTime } from '../format'
import { isLive, issues, type LiveSample } from '../metrics'
import { alertsOn, isViewer, serverById, state } from '../store'

// ---- 分页（修订第 172 条） ----
type TabKey = 'monitor' | 'traffic' | 'network' | 'apps' | 'security' | 'alerts'
interface Tab { key: TabKey; name: string; badge?: number; badgeLevel?: string; dot?: boolean }

const props = defineProps<{
  /** 路由参数中的节点 ID */
  id: string
}>()

const sid = computed(() => Number(props.id))
const s = computed(() => serverById(sid.value))
const live = computed(() => (s.value ? isLive(s.value) : false))
const problems = computed(() => (s.value ? issues(s.value) : []))
const sys = computed(() => s.value?.latest?.system)

const route = useRoute()
const router = useRouter()
// 只列出这台节点有内容的页：容器与代理在有数据时出现；安全页只给管理员；待安装节点只有监控与流量
const tabs = computed<Tab[]>(() => {
  const v = s.value
  if (!v) return []
  const d = liveServer.value ?? v
  const pending = v.status === 'pending'
  const out: Tab[] = [{ key: 'monitor', name: '监控' }, { key: 'traffic', name: '流量' }]
  if (!pending) out.push({ key: 'network', name: '网络' })
  const stopped = (d.containers_missing?.length ?? 0) + (d.proxies_missing?.length ?? 0)
  if (d.latest?.containers?.length || d.latest?.proxies?.length || stopped) {
    out.push({ key: 'apps', name: '容器与代理', dot: stopped > 0 })
  }
  if (!isViewer.value && (d.latest?.ports || d.latest?.firewall)) {
    const fw = d.latest?.firewall
    const weak = !!fw && (!fw.tools.some((t) => t.active && t.name !== 'fail2ban') || fw.tools.some((t) => t.active && t.policy === 'allow'))
    out.push({ key: 'security', name: '安全', dot: weak })
  }
  if (!pending && alertsOn.value) {
    const n = v.alerts.length
    out.push({ key: 'alerts', name: '告警', badge: n || undefined, badgeLevel: v.alerts.some((a) => a.severity === 'critical') ? 'bad' : 'warn' })
  }
  return out
})
// 地址中的页不存在（如节点没有容器）时回到监控
const tab = computed<TabKey>(() => tabs.value.find((t) => t.key === route.query.tab)?.key ?? 'monitor')
const setTab = (t: TabKey) => router.replace({ query: { ...route.query, tab: t === 'monitor' ? undefined : t } })

// ---- 实时采样：网络块中的实时网速曲线 ----
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
// 1 小时历史同时用于预填实时曲线与默认的历史曲线：短时间内只请求一次
let recent1h: { id: number; at: number; p: ReturnType<typeof getHistory> } | null = null
function history1h(id: number) {
  if (!recent1h || recent1h.id !== id || Date.now() - recent1h.at > 5000) {
    recent1h = { id, at: Date.now(), p: getHistory(id, '1h') }
  }
  return recent1h.p
}

async function seedSamples() {
  const id = sid.value
  try {
    const h = await history1h(id)
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
// 列表接口省略每核使用率等只在详情显示的字段，详情页单独每 2 秒刷新本节点。
// 按需实时模式（设计 46.2）：页面可见时请求带 live=1，节点临时以 2 秒采样；切到后台或关闭后不再续期，30 秒内恢复
const detail = ref<ServerView | null>(null)
let liveTimer: number | undefined
async function loadDetail() {
  try {
    detail.value = await getServer(sid.value, document.visibilityState === 'visible')
    addSample(detail.value)
  } catch {
    // 忽略：实时区块退回使用全局列表中的数据；401 由统一处理回到登录页
  }
}
watch(sid, () => {
  detail.value = null
  samples.value = []
  seedSamples()
  loadDetail()
  if (liveTimer) clearInterval(liveTimer)
  liveTimer = window.setInterval(loadDetail, 2000)
}, { immediate: true })
onBeforeUnmount(() => liveTimer && clearInterval(liveTimer))

const liveServer = computed(() => (detail.value?.id === sid.value && detail.value.latest ? detail.value : s.value))

// 采集失败项（设计 43.5）：显示采集项的中文名称
const itemNames: Record<string, string> = {
  system: '系统', cpu: 'CPU', memory: '内存', disk: '磁盘', disk_io: '磁盘 IO', network: '网络',
  processes: '进程', conns: '连接', ports: '端口', extra: '扩展指标',
}
const collectErrors = computed(() =>
  (liveServer.value?.latest?.collect_errors ?? []).map((e) => ({ item: itemNames[e.item] ?? e.item, message: e.message })),
)

// ---- 历史曲线 ----
const ranges: HistoryRange[] = ['1h', '6h', '24h', '7d', '30d']
const range = ref<HistoryRange>('1h')
const history = ref<HistoryView | null>(null)
const historyError = ref('')
let timer: number | undefined

async function loadHistory() {
  try {
    history.value = await (range.value === '1h' ? history1h(sid.value) : getHistory(sid.value, range.value))
    historyError.value = ''
  } catch (e) {
    historyError.value = errorText(e)
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
// 否则图表会用直线把缺口连起来，看起来像真实数据。原始粒度按节点的采样间隔计（设计 4.2）
const line = (name: string, f: (p: (typeof pts.value)[number]) => number | null): Series => {
  const res = Math.max(history.value?.resolution ?? 10, s.value?.report_interval_s || 10)
  const data: Series['data'] = []
  pts.value.forEach((p, i) => {
    const prev = pts.value[i - 1]
    if (prev && p.ts - prev.ts > res * 3) data.push([(prev.ts + res) * 1000, null])
    data.push([p.ts * 1000, f(p)])
  })
  return { name, data }
}
const hasData = (f: (p: (typeof pts.value)[number]) => number | null) => pts.value.some((p) => f(p) != null)
const charts = computed(() => [
  // CPU 只有一张图（修订第 177 条）：使用率与被抢占（steal，高说明宿主机超售）、IO 等待（高说明在等磁盘，设计 4.4）画在一起；
  // 旧版 Agent 没有占比数据时只画使用率，长时间范围有峰值时补上峰值
  {
    title: 'CPU', max: 100, format: (v: number) => `${v.toFixed(v < 10 ? 1 : 0)}%`,
    series: hasData((p) => p.steal ?? p.iowait)
      ? [line('使用率', (p) => p.cpu), line('被抢占', (p) => p.steal), line('IO 等待', (p) => p.iowait)]
      : coarse.value ? [line('平均', (p) => p.cpu), line('峰值', (p) => p.cpu_max)] : [line('使用率', (p) => p.cpu)],
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
  ...(hasData((p) => p.tcp)
    ? [{
        title: 'TCP 连接数', format: (v: number) => String(Math.round(v)),
        series: coarse.value ? [line('平均', (p) => p.tcp), line('峰值', (p) => p.tcp_max)] : [line('TCP', (p) => p.tcp)],
      }]
    : []),
])
</script>

<template>
  <main class="page">
    <div class="nav-row">
      <RouterLink to="/" class="back muted small"><Icon name="arrow-left" :size="14" />仪表板</RouterLink>
      <div v-if="s && !isViewer" class="actions-row">
        <RouterLink :to="`/servers/${s.id}/install`" class="btn secondary"><Icon name="terminal" />{{ s.status === 'pending' ? '安装命令' : '重新安装' }}</RouterLink>
        <RouterLink :to="`/servers/${s.id}/edit`" class="btn secondary"><Icon name="edit" />编辑</RouterLink>
      </div>
    </div>

    <p v-if="!state.loaded" class="muted">加载中…</p>
    <p v-else-if="!s" class="muted">节点不存在或已删除。<RouterLink to="/">返回列表</RouterLink></p>

    <template v-else>
      <div v-if="s.status === 'pending'" class="banner warn">
        该节点尚未安装 Agent。<RouterLink v-if="!isViewer" :to="`/servers/${s.id}/install`">查看安装命令</RouterLink>
      </div>
      <!-- 只在离线 / 未知时提示；资源与流量的问题见下方健康摘要，不重复显示（修订第 177 条） -->
      <div v-else-if="s.status !== 'online' && problems.length" class="banner" :class="{ warn: problems[0].level === 'warn' }">
        {{ problems.map((p) => p.text).join(' · ') }}
        <template v-if="s.status === 'offline' && s.last_seen_at">，最后上报 {{ fmtTime(s.last_seen_at) }}</template>
      </div>

      <!-- 静音与维护（设计 16.6） -->
      <SilenceControls v-if="s.status !== 'pending' && !isViewer && alertsOn" :server="s" class="section-tight" />

      <!-- 概况（设计 11.1） -->
      <ServerSummary :server="liveServer ?? s" :live="live" />

      <!-- 健康摘要（设计 1.5.7） -->
      <HealthCard v-if="s.status !== 'pending'" :server-id="s.id" class="section" />

      <!-- 分页（修订第 172 条）：概况与健康摘要之下按主题分页，地址带 ?tab=，刷新与分享后保持；未打开的页不加载 -->
      <nav class="tabs" role="tablist" aria-label="节点详情">
        <button v-for="t in tabs" :key="t.key" type="button" role="tab" :aria-selected="tab === t.key" :class="{ active: tab === t.key }"
          @click="setTab(t.key)">
          {{ t.name }}<span v-if="t.badge" class="badge num" :class="t.badgeLevel">{{ t.badge }}</span><i v-else-if="t.dot" class="dot" />
        </button>
      </nav>

      <!-- 监控：速览 → CPU → 内存 → 磁盘 → 网络 → 历史；离线时不显示旧数值（设计 43.6） -->
      <template v-if="tab === 'monitor'">
        <template v-if="live && liveServer?.latest">
          <section class="section">
            <LivePanels :server="liveServer" :samples="samples" />
          </section>
          <!-- 部分采集项失败：其余指标照常显示（设计 43.5） -->
          <section v-if="collectErrors.length" class="section collect-errors small">
            <strong>部分指标本轮未采集到</strong>
            <ul>
              <li v-for="(e, i) in collectErrors" :key="i"><span class="muted">{{ e.item }}</span>{{ e.message }}</li>
            </ul>
          </section>
        </template>
        <p v-else-if="s.status !== 'pending'" class="muted section">节点离线，不显示实时数值；历史曲线见下方。</p>

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
      </template>

      <!-- 流量 -->
      <section v-else-if="tab === 'traffic'" class="section">
        <TrafficCard :server="s" />
        <TrafficHistory :server="s" class="section-tight" />
        <CloudInstanceCard v-if="!isViewer" :server="s" class="section-tight" />
      </section>

      <!-- 网络：测速（设计 42.3.2）与国内可达性（设计 42.2，修订第 155 条） -->
      <template v-else-if="tab === 'network'">
        <LatencyCard :server-id="s.id" class="section" />
        <ReachCard :server-id="s.id" class="section" />
      </template>

      <!-- 容器与代理（设计 33.4、4.9.3）：代理只给管理员 -->
      <template v-else-if="tab === 'apps' && liveServer">
        <section v-if="liveServer.latest?.containers?.length || liveServer.containers_missing?.length" class="section">
          <ContainersCard :server="liveServer" />
        </section>
        <section v-if="liveServer.latest?.proxies?.length || liveServer.proxies_missing?.length" class="section">
          <ProxiesCard :server="liveServer" />
        </section>
      </template>

      <!-- 安全（只给管理员）：端口基线（修订第 170、171 条）与防火墙（设计 4.9.2） -->
      <template v-else-if="tab === 'security' && liveServer">
        <section v-if="liveServer.latest?.ports" class="section">
          <PortBaselineCard :server="liveServer" />
        </section>
        <section v-if="liveServer.latest?.firewall" class="section">
          <FirewallCard :server="liveServer" />
        </section>
      </template>

      <!-- 告警记录（设计 16） -->
      <section v-else-if="tab === 'alerts'" class="section">
        <AlertHistory :server-id="s.id" :version="`${s.alerts.map((a) => a.event_id).join(',')}|${s.maintenance?.id ?? ''}`" />
      </section>

    </template>
  </main>
</template>

<style scoped>
.section-tight { margin-bottom: var(--space-3); }
.nav-row { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); margin-bottom: var(--space-3); }
.actions-row { display: flex; gap: var(--space-2); }
.back { display: inline-flex; align-items: center; gap: var(--space-1); }
.title .row { gap: var(--space-3); }
.section-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: var(--space-3); gap: var(--space-3); flex-wrap: wrap; }
.charts { display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); gap: var(--space-3); }
@media (max-width: 600px) {
  .charts { grid-template-columns: 1fr; }
}
.tabs { display: flex; gap: var(--space-1); margin: var(--space-4) 0 var(--space-3); border-bottom: 1px solid var(--border);
  overflow-x: auto; scrollbar-width: none; }
.tabs button { background: none; border: 0; border-bottom: 2px solid transparent; border-radius: 0; color: var(--text-muted);
  padding: var(--space-2) var(--space-3); white-space: nowrap; display: inline-flex; align-items: center; gap: var(--space-1); }
.tabs button:hover { color: var(--text); }
.tabs button.active { color: var(--text); border-bottom-color: var(--accent); font-weight: var(--weight-strong); }
.badge { min-width: 16px; height: 16px; padding: 0 4px; border-radius: var(--radius-full); font-size: 10px; line-height: 16px; text-align: center;
  color: var(--on-accent); background: var(--warn); }
.badge.bad { background: var(--bad); }
.dot { width: 6px; height: 6px; border-radius: 50%; background: var(--warn); display: inline-block; }
@media (max-width: 600px) {
  .tabs button { padding: var(--space-2); }
}
.collect-errors { padding: var(--space-3); background: var(--surface-2); border-radius: var(--radius-sm); }
.collect-errors ul { margin: var(--space-2) 0 0; padding-left: var(--space-4); }
.collect-errors li span { margin-right: var(--space-2); }
</style>
