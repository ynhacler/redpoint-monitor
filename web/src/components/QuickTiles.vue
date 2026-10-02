<script setup lang="ts">
// 详情页的指标速览（设计 11.1）：CPU、磁盘、内存、连接与进程四块，以及最近几分钟的实时网速。
// 迷你折线与网速图来自详情页维护的实时采样（打开页面时用 1 小时历史的最近 10 分钟预填）。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { fmtBytes, fmtBytesShort, fmtPct } from '../format'
import { cpu, fullestDisk, gaugeLevel, mem, rx, tx, type LiveSample } from '../metrics'
import Chart, { type Series } from './Chart.vue'
import Ring from './Ring.vue'
import Sparkline from './Sparkline.vue'

const props = defineProps<{
  /** 节点；调用方保证 latest 存在且在线 */
  server: ServerView
  /** 按时间顺序的实时采样 */
  samples: LiveSample[]
}>()

const r = computed(() => props.server.latest!)
const disk = computed(() => fullestDisk(props.server))
const swapPct = computed(() => (r.value.swap.total ? (r.value.swap.used / r.value.swap.total) * 100 : undefined))
const cpuLv = computed(() => gaugeLevel(cpu(props.server), 'cpu'))
const memLv = computed(() => gaugeLevel(mem(props.server), 'mem'))
const color = (lv: string) => (lv === 'ok' ? 'var(--accent)' : `var(--${lv})`)

const speedSeries = computed<Series[]>(() => [
  { name: '下行', data: props.samples.map((p) => [p.ts * 1000, p.rx]) },
  { name: '上行', data: props.samples.map((p) => [p.ts * 1000, p.tx]) },
])
const minutes = computed(() => {
  const n = props.samples.length
  return n > 1 ? Math.max(1, Math.round((props.samples[n - 1].ts - props.samples[0].ts) / 60)) : 0
})
</script>

<template>
  <div class="quick">
    <div class="tile">
      <div class="label"><span>CPU</span><span class="muted num">{{ r.cpu.cores }} 核</span></div>
      <div class="big num" :class="cpuLv">{{ fmtPct(cpu(server)) }}</div>
      <Sparkline :values="samples.map((p) => p.cpu)" :max="100" :color="color(cpuLv)" />
    </div>

    <div class="tile ring-tile">
      <div>
        <div class="label"><span>磁盘{{ disk && disk.mount !== '/' ? ` ${disk.mount}` : '' }}</span></div>
        <div class="big num" :class="gaugeLevel(disk?.usage, 'disk')">{{ fmtPct(disk?.usage) }}</div>
        <div class="muted small num">{{ fmtBytesShort(disk?.used) }} / {{ fmtBytesShort(disk?.total) }}</div>
      </div>
      <Ring :pct="disk?.usage" :level="gaugeLevel(disk?.usage, 'disk')" :size="52" tinted label="磁盘" />
    </div>

    <div class="tile">
      <div class="label"><span>内存</span><span v-if="swapPct != null" class="muted num">Swap {{ fmtPct(swapPct) }}</span></div>
      <div class="big num" :class="memLv">{{ fmtPct(mem(server)) }}</div>
      <Sparkline :values="samples.map((p) => p.mem)" :max="100" :color="color(memLv)" />
    </div>

    <div class="tile">
      <div class="label"><span>连接与进程</span></div>
      <div class="counts num">
        <div><b>{{ r.conns?.tcp ?? '—' }}</b><span><i style="background: var(--accent)" />TCP</span></div>
        <div><b>{{ r.conns?.udp ?? '—' }}</b><span><i style="background: var(--series-2)" />UDP</span></div>
        <div><b>{{ r.processes?.total ?? '—' }}</b><span><i style="background: var(--ok)" />进程</span></div>
      </div>
      <div v-if="r.conns || r.processes" class="muted small num sub">
        <template v-if="r.conns">TIME_WAIT {{ r.conns.time_wait }}</template>
        <template v-if="r.conns && r.processes"> · </template>
        <template v-if="r.processes">运行 {{ r.processes.running }}</template>
      </div>
    </div>

    <div class="speed">
      <div class="speed-head">
        <span class="muted small title">实时网速<template v-if="minutes">（最近 {{ minutes }} 分钟）</template></span>
        <span class="values">
          <span class="num"><i style="background: var(--accent)" />下行 {{ fmtBytes(rx(server), true) }}</span>
          <span class="num"><i style="background: var(--ok)" />上行 {{ fmtBytes(tx(server), true) }}</span>
        </span>
      </div>
      <Chart title="" :series="speedSeries" :format="(v: number) => fmtBytes(v, true)" hide-legend />
    </div>
  </div>
</template>

<style scoped>
.quick { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--space-3); }
.tile { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md); padding: var(--space-3) var(--space-4);
  display: flex; flex-direction: column; gap: var(--space-1); min-width: 0; overflow: hidden; }
.ring-tile { flex-direction: row; align-items: center; justify-content: space-between; gap: var(--space-2); }
.label { display: flex; justify-content: space-between; gap: var(--space-2); font-size: var(--font-sm); line-height: var(--line-sm); color: var(--text-muted); white-space: nowrap; }
.big { font-size: var(--font-num); line-height: var(--line-num); font-weight: var(--weight-strong); }
.big.warn { color: var(--warn); }
.big.bad { color: var(--bad); }
.counts { display: flex; gap: var(--space-4); }
.counts div { display: flex; flex-direction: column; }
.counts b { font-size: var(--font-xl); line-height: var(--line-xl); }
.counts span, .speed-head span { display: inline-flex; align-items: center; gap: var(--space-1); font-size: var(--font-xs); color: var(--text-muted); }
.speed-head span.num { color: var(--text); font-size: var(--font-sm); }
i { width: 8px; height: 3px; border-radius: 2px; display: inline-block; }
.speed { grid-column: 1 / -1; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md); padding: var(--space-3) var(--space-4) var(--space-1); }
.speed-head { display: flex; gap: var(--space-2) var(--space-4); align-items: center; justify-content: space-between; flex-wrap: wrap; }
.values { display: flex; gap: var(--space-4); }
.sub { margin-top: auto; }
/* Chart 自带面板样式，这里嵌在网速卡片中，去掉边框与内边距 */
.speed :deep(.chart) { border: 0; padding: 0; background: none; }
.speed :deep(.chart h3) { display: none; }
@media (max-width: 900px) {
  .quick { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 600px) {
  .tile { padding: var(--space-3); }
  .counts { gap: var(--space-3); }
  .counts b { font-size: var(--font-lg); line-height: var(--line-lg); }
}
</style>
