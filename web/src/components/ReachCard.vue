<script setup lang="ts">
// 国内可达性（设计 42.2、42.3.1，修订第 155 条）：国内探针到这台节点的结论、各探针的状态与最近一轮、24 小时国内方向延迟曲线。
// 结论都是“疑似”：连续 3 轮异常才判定，探针对所有节点都失败的轮次不计。没有设置国内探针时只显示一行入口提示。每分钟刷新。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, getServerReach, type ServerReach } from '../api'
import { DASH, fmtLatency, fmtPct, fmtTime } from '../format'
import { t } from '../i18n'
import { isViewer } from '../store'
import Chart, { type Series } from './Chart.vue'

const props = defineProps<{ serverId: number }>()

const view = ref<ServerReach | null>(null)
const error = ref('')
let timer: ReturnType<typeof setTimeout> | undefined

async function load() {
  clearTimeout(timer)
  try {
    view.value = await getServerReach(props.serverId)
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t('国内可达性加载失败'))
  }
  timer = setTimeout(load, 60_000)
}
watch(() => props.serverId, load, { immediate: true })
onBeforeUnmount(() => clearTimeout(timer))

const ispNames: Record<string, string> = { ct: '电信', cu: '联通', cm: '移动' }
const statusNames: Record<string, string> = { ok: '可达', unstable: '不稳定', port: '端口疑似被封', blocked: '疑似被墙' }
const errorNames: Record<string, string> = {
  timeout: '超时', refused: '连接被拒绝', unreachable: '网络不可达', icmp_unavailable: '探针没有 Ping 权限', error: '失败',
}
// 结论的颜色：疑似被墙 / 部分不可达 / 端口为红，不稳定为黄，可达为绿
const tone = (st: string) => (['blocked', 'partial', 'port'].includes(st) ? 'bad' : st === 'unstable' ? 'warn' : st === 'ok' ? 'ok' : 'muted')
const checkName = (c: { family: number; port: number }) => `${c.port ? `TCP ${c.port}` : 'Ping'}${c.family === 6 ? ' · IPv6' : ''}`

const series = computed<{ lat: Series[]; loss: Series[] }>(() => {
  const lat: Series[] = []
  const loss: Series[] = []
  for (const p of view.value?.probes ?? []) {
    const name = ispNames[p.isp] ? `${t(ispNames[p.isp])}（${p.name}）` : p.name
    const pts = view.value?.series[String(p.probe_id)] ?? []
    lat.push({ name, data: pts.map((x) => [x.ts * 1000, x.latency_ms ?? null]) })
    loss.push({ name, data: pts.map((x) => [x.ts * 1000, x.loss]) })
  }
  return { lat, loss }
})
const hasSeries = computed(() => series.value.lat.some((s) => s.data.length > 1))
</script>

<template>
  <!-- 没有国内探针时不显示（修订第 173 条）：设置入口与说明只在“系统 → 监测”中 -->
  <section v-if="error || view?.configured" class="panel reach">
    <div class="head">
      <h3>{{ t('国内可达性') }}</h3>
      <span v-if="view?.text" class="verdict" :class="tone(view.status)">{{ view.text }}</span>
      <RouterLink v-if="!isViewer" to="/monitor#reach" class="btn secondary small set">{{ t('国内探针') }}</RouterLink>
    </div>
    <p v-if="error" class="small bad">{{ error }}</p>
    <template v-else-if="view">
      <p v-if="!view.probes.length" class="small muted note">{{ t('国内探针还没有测到这台节点：探针收到列表后约 20 秒测第一轮。') }}</p>
      <template v-else>
        <table class="probes">
          <thead>
            <tr><th>{{ t('探针') }}</th><th>{{ t('状态') }}</th><th>{{ t('最近一轮') }}</th></tr>
          </thead>
          <tbody>
            <tr v-for="p in view.probes" :key="p.probe_id">
              <th scope="row">
                <span>{{ ispNames[p.isp] ? t(ispNames[p.isp]) : '' }}</span>
                <small class="muted">{{ p.name }}</small>
              </th>
              <td>
                <span :class="tone(p.status)">{{ statusNames[p.status] ? t(statusNames[p.status]) : p.status }}<template v-if="p.status === 'port'"> {{ p.bad_ports.join(t('、')) }}</template></span>
                <small v-if="p.status !== 'ok'" class="muted">{{ t('自 {time}', { time: fmtTime(p.since) }) }}</small>
              </td>
              <td>
                <ul class="checks">
                  <li v-for="c in p.checks ?? []" :key="`${c.family}-${c.port}`" :title="c.error ? (errorNames[c.error] ? t(errorNames[c.error]) : c.error) : ''">
                    <span class="muted">{{ checkName(c) }}</span>
                    <span class="num" :class="c.loss >= 100 && c.error !== 'icmp_unavailable' ? 'bad' : ''">
                      {{ c.latency_ms != null ? fmtLatency(c.latency_ms) : c.error === 'icmp_unavailable' ? DASH : t('失败') }}
                    </span>
                    <span v-if="c.loss > 0 && c.loss < 100" class="muted num">{{ t('丢包') }} {{ fmtPct(c.loss) }}</span>
                  </li>
                </ul>
                <small class="muted">{{ fmtTime(p.last_at) }}</small>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="hasSeries" class="charts">
          <Chart :title="t('国内 → 这台节点 · 延迟（24 小时）')" :series="series.lat" :format="fmtLatency" />
          <Chart :title="t('国内 → 这台节点 · 丢包率')" :series="series.loss" :format="fmtPct" :max="100" />
        </div>
        <p class="small muted note">{{ t('结论为“疑似”：连续 3 轮异常才判定，可能是封锁，也可能是运营商故障或路由问题；探针自身网络异常的轮次不计。') }}</p>
      </template>
    </template>
  </section>
</template>

<style scoped>
.head { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; margin-bottom: var(--space-3); }
.head h3 { margin: 0; }
.set { margin-left: auto; }
.verdict { font-weight: var(--weight-strong); }
.ok { color: var(--ok); }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
.note { margin: 0 0 var(--space-2); }
.probes { width: 100%; border-collapse: collapse; font-size: var(--font-sm); }
.probes th, .probes td { text-align: left; vertical-align: top; padding: var(--space-2); border-bottom: 1px solid var(--border); }
.probes thead th { color: var(--text-muted); font-weight: var(--weight-regular); }
.probes tbody tr:last-child th, .probes tbody tr:last-child td { border-bottom: none; }
.probes th small, .probes td small { display: block; }
.checks { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: var(--space-1) var(--space-3); }
.checks li { display: flex; gap: var(--space-1); white-space: nowrap; }
.charts { display: grid; gap: var(--space-3); margin-top: var(--space-3); }
</style>
