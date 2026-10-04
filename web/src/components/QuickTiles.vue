<script setup lang="ts">
// 详情页的指标速览（设计 11.1）：CPU、磁盘、内存、连接与进程四块，以及最近几分钟的实时网速。
// 迷你折线贴着卡片底边铺满（参考 Monito），数据来自详情页维护的实时采样（打开时用 1 小时历史预填）。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { fmtBytes, fmtBytesShort, fmtPct } from '../format'
import { cpu, diskSummary, gaugeLevel, mem, rx, tx, type LiveSample } from '../metrics'
import Chart, { type Series } from './Chart.vue'
import Qty from './Qty.vue'
import Ring from './Ring.vue'
import Icon from './Icon.vue'
import Sparkline from './Sparkline.vue'

const props = defineProps<{
  /** 节点；调用方保证 latest 存在且在线 */
  server: ServerView
  /** 按时间顺序的实时采样 */
  samples: LiveSample[]
}>()

const r = computed(() => props.server.latest!)
const disk = computed(() => diskSummary(props.server))
const swapPct = computed(() => (r.value.swap.total ? (r.value.swap.used / r.value.swap.total) * 100 : undefined))
const cpuLv = computed(() => gaugeLevel(cpu(props.server), 'cpu'))
const memLv = computed(() => gaugeLevel(mem(props.server), 'mem'))
const diskLv = computed(() => disk.value?.level ?? 'ok')
// 正常时用强调色（与参考一致的蓝），接近阈值时随状态变色
const color = (lv: string) => (lv === 'ok' ? 'var(--accent)' : `var(--${lv})`)
const pct1 = (v: number | undefined) => (v == null ? '—' : v >= 10 ? String(Math.round(v * 10) / 10) : v.toFixed(1))

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
    <div class="tile spark-tile">
      <div class="label"><Icon name="cpu" :size="15" />CPU<span class="extra num">{{ r.cpu.cores }} 核</span></div>
      <div class="big" :class="cpuLv"><Qty :v="pct1(cpu(server))" u="%" /></div>
      <Sparkline class="edge" :values="samples.map((p) => p.cpu)" :color="color(cpuLv)" :height="36" />
    </div>

    <div class="tile">
      <div class="label"><Icon name="hard-drive" :size="15" />磁盘<span v-if="disk && disk.level !== 'ok' && gaugeLevel(disk.usage, 'disk') !== disk.level" class="extra" :class="disk.level">{{ disk.fullest.mount }} {{ Math.round(disk.fullest.usage) }}%</span><span v-else-if="disk && disk.count > 1" class="extra">{{ disk.count }} 块</span></div>
      <div class="ring-row">
        <div>
          <div class="big" :class="diskLv"><Qty :v="disk ? Math.round(disk.usage) : '—'" u="%" /></div>
          <div class="muted small num nowrap">{{ fmtBytesShort(disk?.used) }} / {{ fmtBytesShort(disk?.total) }}</div>
        </div>
        <Ring class="disk-ring" :pct="disk?.usage" :level="diskLv" :size="56" tinted bare label="磁盘" />
      </div>
    </div>

    <div class="tile spark-tile">
      <div class="label"><Icon name="layers" :size="15" />内存<span v-if="swapPct != null" class="extra num">Swap {{ fmtPct(swapPct) }}</span></div>
      <div class="big" :class="memLv"><Qty :v="pct1(mem(server))" u="%" /></div>
      <Sparkline class="edge" :values="samples.map((p) => p.mem)" :color="color(memLv)" :height="36" />
    </div>

    <div class="tile">
      <div class="label"><Icon name="radio" :size="15" />连接与进程</div>
      <div class="counts num">
        <div><b>{{ r.conns?.tcp ?? '—' }}</b><span><i style="background: var(--accent)" />TCP</span></div>
        <div><b>{{ r.conns?.udp ?? '—' }}</b><span><i style="background: var(--series-2)" />UDP</span></div>
        <div><b>{{ r.processes?.total ?? '—' }}</b><span><i style="background: var(--ok)" />进程</span></div>
      </div>
      <div v-if="!r.conns && !r.processes" class="muted small foot">升级 Agent 后显示</div>
      <div v-else class="muted small num foot">
        <template v-if="r.conns">TIME_WAIT {{ r.conns.time_wait }}</template>
        <template v-if="r.conns && r.processes"> · </template>
        <template v-if="r.processes">运行 {{ r.processes.running }}</template>
      </div>
    </div>

    <div class="tile speed">
      <div class="speed-head">
        <div class="label" :title="minutes ? `最近 ${minutes} 分钟` : ''"><Icon name="pulse" :size="15" />上传下载速率</div>
        <span class="values num small">
          <span><i style="background: var(--accent)" />下载 {{ fmtBytes(rx(server), true) }}</span>
          <span><i style="background: var(--ok)" />上传 {{ fmtBytes(tx(server), true) }}</span>
        </span>
      </div>
      <Chart title="" :series="speedSeries" :format="(v: number) => fmtBytes(v, true)" hide-legend />
    </div>
  </div>
</template>

<style scoped>
.quick { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--space-3); }
.tile { position: relative; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md);
  padding: var(--space-3) var(--space-4); display: flex; flex-direction: column; gap: var(--space-2); min-width: 0; overflow: hidden; }
.label { display: flex; align-items: center; gap: 6px; font-size: var(--font-sm); line-height: var(--line-sm); color: var(--text-muted); white-space: nowrap; min-width: 0; }
.label svg { flex: none; }
.extra { margin-left: auto; font-size: var(--font-xs); overflow: hidden; text-overflow: ellipsis; }
.big { font-size: 30px; line-height: 36px; font-weight: 700; letter-spacing: -0.01em; }
.big :deep(.u) { font-size: .6em; font-weight: var(--weight-strong); margin-left: 1px; color: inherit; opacity: .85; }
.nowrap { white-space: nowrap; }
.big.warn { color: var(--warn); }
.big.bad { color: var(--bad); }
/* 迷你折线贴底铺满：抵消卡片左右与底部内边距 */
.spark-tile { padding-bottom: 0; }
.edge { margin: auto calc(-1 * var(--space-4)) 0; width: calc(100% + 2 * var(--space-4)); }
.ring-row { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); flex: 1; }
.counts { display: flex; justify-content: space-between; gap: var(--space-2); }
.counts div { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.counts b { font-size: 24px; line-height: 30px; font-weight: 700; }
.counts span, .values span { display: inline-flex; align-items: center; gap: 4px; font-size: var(--font-xs); color: var(--text-muted); white-space: nowrap; }
.values span { font-size: var(--font-sm); color: var(--text); }
i { width: 6px; height: 6px; border-radius: 50%; display: inline-block; flex: none; }
.foot { margin-top: auto; }
.speed { grid-column: 1 / -1; padding-bottom: var(--space-1); }
.speed-head { display: flex; gap: var(--space-2) var(--space-4); align-items: center; justify-content: space-between; flex-wrap: wrap; }
.speed-head .label { flex: 1; }
.extra.warn { color: var(--warn); }
.extra.bad { color: var(--bad); }
.speed-head .extra { margin-left: var(--space-2); }
.values { display: flex; gap: var(--space-4); }
/* Chart 自带面板样式，这里嵌在卡片中，去掉边框与内边距 */
.speed :deep(.chart) { border: 0; padding: 0; background: none; }
.speed :deep(.chart h3) { display: none; }
@media (max-width: 900px) {
  .quick { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 600px) {
  .tile { padding: var(--space-3); }
  .edge { margin-left: calc(-1 * var(--space-3)); margin-right: calc(-1 * var(--space-3)); width: calc(100% + 2 * var(--space-3)); }
  .big { font-size: 26px; line-height: 32px; }
  .counts b { font-size: 20px; line-height: 26px; }
  .disk-ring { transform: scale(.86); transform-origin: right center; margin-left: -8px; }
}
</style>
