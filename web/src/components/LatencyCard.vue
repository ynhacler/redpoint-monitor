<script setup lang="ts">
// 三网延迟（设计 42.3.2、42.3.3）：节点 Agent 到三大运营商各地目标的延迟与丢包。
// 表格每行一个地区、每列一个运营商（窄屏也放得下）；数值为最近 1 小时的中位数与平均丢包。
// 点击一行查看该地区三网的 24 小时 / 7 天曲线。Agent 每 5 分钟测一轮，这里每分钟刷新。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, getLatency, type LatencyTarget, type LatencyView } from '../api'
import { DASH, fmtLatency, fmtPct, fmtTime } from '../format'
import Chart, { type Series } from './Chart.vue'

const props = defineProps<{ serverId: number }>()

const isps = [
  { key: 'ct', name: '电信' },
  { key: 'cu', name: '联通' },
  { key: 'cm', name: '移动' },
] as const
const errorNames: Record<string, string> = {
  timeout: '超时', refused: '连接被拒绝', unreachable: '网络不可达', icmp_unavailable: '本机不允许 ICMP', error: '失败',
}

const view = ref<LatencyView | null>(null)
const error = ref('')
const range = ref<'24h' | '7d'>('24h')
const selected = ref('')
let timer: ReturnType<typeof setTimeout> | undefined

async function load() {
  clearTimeout(timer)
  try {
    view.value = await getLatency(props.serverId, range.value)
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '三网延迟加载失败')
  }
  timer = setTimeout(load, 60_000)
}
watch([() => props.serverId, range], load, { immediate: true })
onBeforeUnmount(() => clearTimeout(timer))

// 按地区分行，保持目标列表的顺序
const rows = computed(() => {
  const out: { province: string; cells: Partial<Record<string, LatencyTarget>> }[] = []
  for (const t of view.value?.targets ?? []) {
    let r = out.find((x) => x.province === t.province)
    if (!r) out.push((r = { province: t.province, cells: {} }))
    if (!r.cells[t.isp]) r.cells[t.isp] = t
  }
  return out
})
const hasData = computed(() => (view.value?.targets ?? []).some((t) => t.last_at > 0))
watch(rows, (rs) => {
  if (!rs.some((r) => r.province === selected.value)) selected.value = rs[0]?.province ?? ''
})

// 颜色分级（设计 42.3.3）：< 100 ms 好；> 200 ms 或丢包 > 5% 警告；全部失败为不可达
function level(t?: LatencyTarget): string {
  if (!t || t.loss == null) return ''
  if (t.latency_ms == null) return 'bad'
  if (t.latency_ms > 200 || t.loss > 5) return 'warn'
  if (t.latency_ms < 100) return 'ok'
  return ''
}
function cellTitle(t?: LatencyTarget): string {
  if (!t) return ''
  const where = `${t.proto === 'icmp' ? 'ICMP' : `TCP ${t.port}`} → ${t.address}`
  if (!t.last_at) return `${where}\n还没有数据`
  const err = t.latency_ms == null && t.last_error ? `\n最近一轮：${errorNames[t.last_error] ?? t.last_error}` : ''
  return `${where}\n最近 1 小时中位数，最近一轮 ${fmtTime(t.last_at)}${err}`
}

const series = computed<{ lat: Series[]; loss: Series[] }>(() => {
  const r = rows.value.find((x) => x.province === selected.value)
  const lat: Series[] = []
  const loss: Series[] = []
  for (const isp of isps) {
    const t = r?.cells[isp.key]
    if (!t) continue
    const pts = view.value?.series[String(t.id)] ?? []
    lat.push({ name: isp.name, data: pts.map((p) => [p.ts * 1000, p.latency_ms ?? null]) })
    loss.push({ name: isp.name, data: pts.map((p) => [p.ts * 1000, p.loss]) })
  }
  return { lat, loss }
})
</script>

<template>
  <section class="panel latency">
    <div class="head">
      <h3>三网延迟</h3>
      <div v-if="hasData" class="segmented" role="group" aria-label="时间范围">
        <button v-for="r in (['24h', '7d'] as const)" :key="r" type="button" :class="{ active: range === r }" @click="range = r">{{ r.toUpperCase() }}</button>
      </div>
    </div>
    <p v-if="error" class="small bad">{{ error }}</p>
    <template v-else-if="view">
      <p v-if="view.status === 'off'" class="small muted note">节点本机已关闭网络探测（/etc/vpsmon-agent/no-probe 存在），删除该文件后开启。</p>
      <p v-else-if="view.status === 'unsupported' && !hasData" class="small muted note">当前 Agent 不支持三网延迟，升级 Agent 后显示（面板刚重启时，等下一次上报）。</p>
      <p v-else-if="!view.targets.length" class="small muted note">没有启用的探测目标，在“告警 → 三网”中启用。</p>
      <p v-else-if="!hasData" class="small muted note">Agent 每 5 分钟测一轮，第一轮结果几分钟内出现。</p>

      <div v-if="hasData" class="table-wrap">
        <table>
          <thead>
            <tr><th scope="col">地区</th><th v-for="isp in isps" :key="isp.key" scope="col">{{ isp.name }}</th></tr>
          </thead>
          <tbody>
            <tr v-for="r in rows" :key="r.province" :class="{ sel: r.province === selected }" tabindex="0"
              @click="selected = r.province" @keydown.enter="selected = r.province">
              <th scope="row">{{ r.province }}</th>
              <td v-for="isp in isps" :key="isp.key" :class="level(r.cells[isp.key])" :title="cellTitle(r.cells[isp.key])">
                <template v-if="r.cells[isp.key]?.loss != null">
                  <b class="num">{{ r.cells[isp.key]!.latency_ms == null ? '不可达' : fmtLatency(r.cells[isp.key]!.latency_ms) }}</b>
                  <small>{{ fmtPct(r.cells[isp.key]!.loss) }}</small>
                </template>
                <span v-else class="muted">{{ DASH }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="hasData && series.lat.length" class="charts">
        <Chart :title="`${selected} · 延迟`" :series="series.lat" :format="fmtLatency" />
        <Chart :title="`${selected} · 丢包率`" :series="series.loss" :format="fmtPct" :max="100" />
      </div>
      <p v-if="hasData" class="small muted foot">数值为最近 1 小时的中位数与丢包率；从这台 VPS 出发测量（设计 42.3.2），点击一行查看曲线。</p>
    </template>
  </section>
</template>

<style scoped>
.head { display: flex; justify-content: space-between; align-items: center; gap: var(--space-3); margin-bottom: var(--space-3); flex-wrap: wrap; }
.head h3 { margin: 0; }
.note { margin: 0; }
.table-wrap { overflow-x: auto; }
table { width: 100%; border-collapse: collapse; font-size: var(--font-sm); }
th, td { padding: var(--space-2) var(--space-3); text-align: right; white-space: nowrap; border-bottom: 1px solid var(--border); }
thead th { color: var(--text-muted); font-weight: var(--weight-regular); }
th:first-child { text-align: left; padding-left: 0; }
tbody th { font-weight: var(--weight-regular); }
tbody tr { cursor: pointer; }
tbody tr:hover, tbody tr.sel { background: var(--surface-2); }
tbody tr:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
td b { font-weight: var(--weight-regular); font-family: var(--font-mono); }
td small { margin-left: var(--space-2); color: var(--text-muted); font-family: var(--font-mono); }
td.ok b { color: var(--ok); }
td.warn b, td.warn small { color: var(--warn); }
td.bad b { color: var(--bad); }
.charts { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: var(--space-3); margin-top: var(--space-4); }
.foot { margin: var(--space-3) 0 0; }
@media (max-width: 600px) {
  th, td { padding: var(--space-2); }
  td small { display: block; margin-left: 0; }
  .charts { grid-template-columns: 1fr; }
}
</style>
