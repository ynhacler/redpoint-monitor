<script setup lang="ts">
// 测速（设计 42.3.2、42.3.3）：节点 Agent 到三大运营商各地目标的延迟与丢包。
// 表格每行一个地区、每列一个运营商（窄屏也放得下）；数值为最近 1 小时的中位数与平均丢包。
// 境外目标（修订第 122 条）单独一张表：每行一个城市。点击一行查看该地区三网（或该境外城市）的 24 小时 / 7 天曲线。Agent 按“系统 → 监测”中的频率测量（默认 5 分钟），这里每分钟刷新。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, getLatency, type LatencyTarget, type LatencyView } from '../api'
import { DASH, fmtLatency, fmtLoss, fmtTime } from '../format'
import { t } from '../i18n'
import { isViewer } from '../store'
import Chart, { type Series } from './Chart.vue'

const props = defineProps<{ serverId: number }>()

const isps = [
  { key: 'ct', name: '电信' },
  { key: 'cu', name: '联通' },
  { key: 'cm', name: '移动' },
] as const
const errorNames: Record<string, string> = {
  timeout: '超时', refused: '连接被拒绝', unreachable: '网络不可达', icmp_unavailable: '本机不允许 ICMP', dns: '域名解析失败',
  blocked: '域名只解析到内网地址，已拒绝', error: '失败',
}

const view = ref<LatencyView | null>(null)
const error = ref('')
const range = ref<'24h' | '7d'>('24h')
const selected = ref('')
const modeNames: Record<string, string> = { default: '跟随全局', custom: '自定义', off: '已关闭' }
let timer: ReturnType<typeof setTimeout> | undefined

async function load() {
  clearTimeout(timer)
  try {
    view.value = await getLatency(props.serverId, range.value)
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t('测速加载失败'))
  }
  timer = setTimeout(load, 60_000)
}
watch([() => props.serverId, range], load, { immediate: true })
onBeforeUnmount(() => clearTimeout(timer))

// 三网按地区分行，保持目标列表的顺序；选中的行以 “p:地区” 或 “i:目标 ID” 表示
const rows = computed(() => {
  const out: { province: string; cells: Partial<Record<string, LatencyTarget>> }[] = []
  for (const tg of view.value?.targets ?? []) {
    if (tg.isp === 'intl') continue
    let r = out.find((x) => x.province === tg.province)
    if (!r) out.push((r = { province: tg.province, cells: {} }))
    if (!r.cells[tg.isp]) r.cells[tg.isp] = tg
  }
  return out
})
const intl = computed(() => (view.value?.targets ?? []).filter((tg) => tg.isp === 'intl'))
const hasData = computed(() => (view.value?.targets ?? []).some((tg) => tg.last_at > 0))
watch([rows, intl], ([rs, it]) => {
  const keys = [...rs.map((r) => `p:${r.province}`), ...it.map((tg) => `i:${tg.id}`)]
  if (!keys.includes(selected.value)) selected.value = keys[0] ?? ''
})
const selectedName = computed(() => {
  if (selected.value.startsWith('i:')) return intl.value.find((tg) => `i:${tg.id}` === selected.value)?.province ?? ''
  return selected.value.slice(2)
})

// 颜色分级（设计 42.3.3）：< 100 ms 好；> 200 ms 或丢包 > 5% 警告；全部失败为不可达
function level(tg?: LatencyTarget): string {
  if (!tg || tg.loss == null) return ''
  if (tg.latency_ms == null) return 'bad'
  if (tg.latency_ms > 200 || tg.loss > 5) return 'warn'
  if (tg.latency_ms < 100) return 'ok'
  return ''
}
function cellTitle(tg?: LatencyTarget): string {
  if (!tg) return ''
  const where = `${tg.proto === 'icmp' ? 'ICMP' : `TCP ${tg.port}`} → ${tg.address}`
  if (!tg.enabled) return `${where}\n${t('这台节点已不再测这个目标，显示的是之前的数据')}`
  if (!tg.last_at) return `${where}\n${t('还没有数据')}`
  const err = tg.latency_ms == null && tg.last_error ? '\n' + t('最近一轮：{error}', { error: errorNames[tg.last_error] ? t(errorNames[tg.last_error]) : tg.last_error }) : ''
  return `${where}\n${t('最近 1 小时中位数，最近一轮 {time}', { time: fmtTime(tg.last_at) })}${err}`
}

const series = computed<{ lat: Series[]; loss: Series[] }>(() => {
  const lat: Series[] = []
  const loss: Series[] = []
  const add = (name: string, tg?: LatencyTarget) => {
    if (!tg) return
    const pts = view.value?.series[String(tg.id)] ?? []
    lat.push({ name, data: pts.map((p) => [p.ts * 1000, p.latency_ms ?? null]) })
    loss.push({ name, data: pts.map((p) => [p.ts * 1000, p.loss]) })
  }
  if (selected.value.startsWith('i:')) {
    const tg = intl.value.find((x) => `i:${x.id}` === selected.value)
    add(tg?.note || tg?.province || '', tg)
  } else {
    const r = rows.value.find((x) => `p:${x.province}` === selected.value)
    for (const isp of isps) add(isp.name, r?.cells[isp.key])
  }
  return { lat, loss }
})
</script>

<template>
  <section class="panel latency">
    <div class="head">
      <h3>{{ t('测速') }}</h3>
      <span v-if="view" class="small muted mode">{{ modeNames[view.mode] ? t(modeNames[view.mode]) : view.mode }}<template v-if="view.mode !== 'off'"> · {{ t('每 {n} 分钟', { n: view.interval_s / 60 }) }}</template></span>
      <div class="tools">
        <div v-if="hasData" class="segmented" role="group" :aria-label="t('时间范围')">
          <button v-for="r in (['24h', '7d'] as const)" :key="r" type="button" :class="{ active: range === r }" @click="range = r">{{ r.toUpperCase() }}</button>
        </div>
        <!-- 这台节点的设置在“编辑”页中（修订第 123 条） -->
        <RouterLink v-if="!isViewer" :to="`/servers/${serverId}/edit#probe`" class="btn secondary small">{{ t('设置') }}</RouterLink>
      </div>
    </div>
    <p v-if="error" class="small bad">{{ error }}</p>
    <template v-else-if="view">
      <p v-if="view.mode === 'off'" class="small muted note">{{ t('这台节点已关闭测速，在“编辑 → 测速”中开启。') }}</p>
      <p v-else-if="view.status === 'off'" class="small muted note">{{ t('节点本机已关闭网络探测（/etc/vpsmon-agent/no-probe 存在），删除该文件后开启。') }}</p>
      <p v-else-if="view.status === 'unsupported' && !hasData" class="small muted note">{{ t('当前 Agent 不支持测速，升级 Agent 后显示（面板刚重启时，等下一次上报）。') }}</p>
      <p v-else-if="!view.targets.length" class="small muted note">{{ t('没有启用的探测目标，在“系统 → 监测”中启用。') }}</p>
      <p v-else-if="!hasData" class="small muted note">{{ t('Agent 收到目标后约 20 秒测第一轮，之后按“系统 → 监测”中的频率测量。') }}</p>

      <div v-if="hasData && rows.length" class="table-wrap">
        <table>
          <thead>
            <tr><th scope="col">{{ t('地区') }}</th><th v-for="isp in isps" :key="isp.key" scope="col">{{ t(isp.name) }}</th></tr>
          </thead>
          <tbody>
            <tr v-for="r in rows" :key="r.province" :class="{ sel: `p:${r.province}` === selected }" tabindex="0"
              @click="selected = `p:${r.province}`" @keydown.enter="selected = `p:${r.province}`">
              <th scope="row">{{ r.province }}</th>
              <td v-for="isp in isps" :key="isp.key" :class="[level(r.cells[isp.key]), { stale: r.cells[isp.key] && !r.cells[isp.key]!.enabled }]"
                :title="cellTitle(r.cells[isp.key])">
                <template v-if="r.cells[isp.key]?.loss != null">
                  <b class="num">{{ r.cells[isp.key]!.latency_ms == null ? t('不可达') : fmtLatency(r.cells[isp.key]!.latency_ms) }}</b>
                  <small>{{ fmtLoss(r.cells[isp.key]!.loss) }}</small>
                </template>
                <span v-else class="muted">{{ DASH }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <!-- 境外（修订第 122 条）：每行一个城市 -->
      <div v-if="hasData && intl.length" class="table-wrap">
        <table>
          <thead>
            <tr><th scope="col">{{ t('境外') }}</th><th scope="col">{{ t('提供商') }}</th><th scope="col">{{ t('延迟') }}</th></tr>
          </thead>
          <tbody>
            <tr v-for="tg in intl" :key="tg.id" :class="{ sel: `i:${tg.id}` === selected }" tabindex="0"
              @click="selected = `i:${tg.id}`" @keydown.enter="selected = `i:${tg.id}`">
              <th scope="row">{{ tg.province }}</th>
              <td class="muted">{{ tg.note || DASH }}</td>
              <td :class="[level(tg), { stale: !tg.enabled }]" :title="cellTitle(tg)">
                <template v-if="tg.loss != null">
                  <b class="num">{{ tg.latency_ms == null ? t('不可达') : fmtLatency(tg.latency_ms) }}</b>
                  <small>{{ fmtLoss(tg.loss) }}</small>
                </template>
                <span v-else class="muted">{{ DASH }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="hasData && series.lat.length" class="charts">
        <Chart :title="`${selectedName} · ${t('延迟')}`" :series="series.lat" :format="fmtLatency" />
        <Chart :title="`${selectedName} · ${t('丢包率')}`" :series="series.loss" :format="fmtLoss" :max="100" />
      </div>
      <p v-if="hasData" class="small muted foot">{{ t('数值为最近 1 小时的中位数与丢包率；从这台 VPS 出发测量，点击一行查看曲线。') }}</p>
    </template>
  </section>
</template>

<style scoped>
.head { display: flex; justify-content: space-between; align-items: center; gap: var(--space-3); margin-bottom: var(--space-3); flex-wrap: wrap; }
.head h3 { margin: 0; }
.head .mode { margin-right: auto; }
.tools { display: flex; align-items: center; gap: var(--space-2); }
.note { margin: 0; }
.table-wrap { overflow-x: auto; }
.table-wrap + .table-wrap { margin-top: var(--space-4); }
td.muted { color: var(--text-muted); }
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
td.stale { opacity: 0.45; } /* 这台节点已不再测：只是之前的数据 */
.charts { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: var(--space-3); margin-top: var(--space-4); }
.foot { margin: var(--space-3) 0 0; }
@media (max-width: 600px) {
  th, td { padding: var(--space-2); }
  td small { display: block; margin-left: 0; }
  .charts { grid-template-columns: 1fr; }
}
</style>
