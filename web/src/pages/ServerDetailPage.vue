<script setup lang="ts">
// 节点详情（设计 11）。指标顺序与 App 一致：状态 → CPU → 内存 → 磁盘 → 网络 → 流量 → 资产（设计 41.6）。
// 实时数据来自全局轮询；历史曲线按所选范围单独请求（设计 19.7、21）。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ApiError, getHistory, UnauthorizedError, type HistoryRange, type HistoryView } from '../api'
import Chart, { type Series } from '../components/Chart.vue'
import Flag from '../components/Flag.vue'
import Icon from '../components/Icon.vue'
import { countryName } from '../countries'
import Metric from '../components/Metric.vue'
import StatusDot from '../components/StatusDot.vue'
import TrafficCard from '../components/TrafficCard.vue'
import UsageBar from '../components/UsageBar.vue'
import { DASH, fmtBytes, fmtDuration, fmtPct, fmtPrice, fmtTime, periodNames } from '../format'
import { cpu, fullestDisk, isLive, issues, mem, rx, thresholds, tx } from '../metrics'
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
const level = (v: number | undefined, warn: number, bad = 101) =>
  v == null ? undefined : v >= bad ? 'bad' : v >= warn ? 'warn' : undefined
</script>

<template>
  <main class="page">
    <RouterLink to="/servers" class="back muted small"><Icon name="arrow-left" :size="14" />节点</RouterLink>

    <p v-if="!state.loaded" class="muted">加载中…</p>
    <p v-else-if="!s" class="muted">节点不存在或已删除。<RouterLink to="/servers">返回列表</RouterLink></p>

    <template v-else>
      <!-- 状态 -->
      <div class="page-head">
        <div class="title">
          <div class="row"><h1><Flag :code="s.country" /> {{ s.name }}</h1><StatusDot :status="s.status" /></div>
          <div class="muted small">
            {{ [s.ipv4 || s.expected_ipv4, s.region, s.provider, s.group].filter(Boolean).join(' · ') || DASH }}
          </div>
        </div>
        <div class="row">
          <RouterLink :to="`/servers/${s.id}/install`" class="btn secondary"><Icon name="terminal" />{{ s.status === 'pending' ? '安装命令' : '重新安装' }}</RouterLink>
          <RouterLink :to="`/servers/${s.id}/edit`" class="btn secondary"><Icon name="edit" />编辑</RouterLink>
        </div>
      </div>

      <div v-if="s.status === 'pending'" class="banner warn">
        该节点尚未安装 Agent。<RouterLink :to="`/servers/${s.id}/install`">查看安装命令</RouterLink>
      </div>
      <div v-else-if="problems.length" class="banner" :class="{ warn: problems[0].level === 'warn' }">
        {{ problems.map((p) => p.text).join(' · ') }}
        <template v-if="s.status === 'offline' && s.last_seen_at">，最后上报 {{ fmtTime(s.last_seen_at) }}</template>
      </div>

      <!-- CPU → 内存 → 磁盘 → 网络（实时） -->
      <div v-if="live && s.latest" class="panel live">
        <Metric label="CPU" :value="fmtPct(cpu(s))" :level="level(cpu(s), thresholds.cpu)"
          :hint="`${s.latest.cpu.cores} 核`" />
        <Metric label="负载" :value="[s.latest.cpu.load1, s.latest.cpu.load5, s.latest.cpu.load15].filter((v) => v != null).map((v) => v!.toFixed(2)).join(' / ')" />
        <Metric label="内存" :value="`${fmtPct(mem(s))}  ${fmtBytes(s.latest.memory.used)} / ${fmtBytes(s.latest.memory.total)}`"
          :level="level(mem(s), thresholds.mem)" />
        <Metric label="磁盘" :value="fmtPct(fullestDisk(s)?.usage)" :level="level(fullestDisk(s)?.usage, thresholds.disk, thresholds.diskBad)" />
        <Metric label="网络" :value="`↓${fmtBytes(rx(s), true)}  ↑${fmtBytes(tx(s), true)}`" />
        <Metric label="运行时间" :value="fmtDuration(sys?.uptime)" />
      </div>

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

      <!-- 流量 -->
      <section class="section">
        <TrafficCard :server="s" />
      </section>

      <!-- 磁盘（设计 4.6） -->
      <section v-if="s.latest?.disk.length" class="section">
        <h3>磁盘</h3>
        <div class="panel disks">
          <div v-for="d in s.latest.disk" :key="d.mount" class="disk">
            <div class="disk-head">
              <span class="mount">{{ d.mount }}</span>
              <span class="muted small">{{ [d.fstype, d.device].filter(Boolean).join(' · ') }}</span>
              <span class="num small disk-num">{{ fmtBytes(d.used) }} / {{ fmtBytes(d.total) }} · {{ fmtPct(d.usage) }}</span>
            </div>
            <UsageBar :pct="d.usage" />
          </div>
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
            <dt>系统</dt><dd>{{ [sys?.os, sys?.os_version].filter(Boolean).join(' ') || DASH }}</dd>
            <dt>内核</dt><dd>{{ sys?.kernel || DASH }}</dd>
            <dt>架构 / CPU</dt><dd>{{ sys?.arch || DASH }}<template v-if="s.latest"> · {{ s.latest.cpu.cores }} 核</template></dd>
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
            <dt>国家 / 地区</dt><dd>
              <template v-if="s.country"><Flag :code="s.country" /> {{ countryName(s.country) }}</template>
              <template v-else>{{ DASH }}</template>
            </dd>
            <dt>地区</dt><dd>{{ s.region || DASH }}</dd>
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
.back { display: inline-flex; align-items: center; gap: var(--space-1); margin-bottom: var(--space-3); }
.title .row { gap: var(--space-3); }
.live { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: var(--space-4); }
.live :deep(.value) { font-size: var(--font-lg); line-height: var(--line-lg); }
.section-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: var(--space-3); gap: var(--space-3); flex-wrap: wrap; }
.charts { display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); gap: var(--space-3); }
.disks { display: flex; flex-direction: column; gap: var(--space-4); }
.disk-head { display: flex; gap: var(--space-3); align-items: baseline; margin-bottom: var(--space-1); flex-wrap: wrap; }
.mount { font-weight: var(--weight-strong); }
.disk-num { margin-left: auto; }
.info-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: var(--space-3); }
.kv { display: grid; grid-template-columns: auto 1fr; gap: var(--space-2) var(--space-4); margin: var(--space-3) 0 0; }
.kv dt { color: var(--text-muted); font-size: var(--font-sm); }
.kv dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
@media (max-width: 600px) {
  .charts { grid-template-columns: 1fr; }
}
</style>
