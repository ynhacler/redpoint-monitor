<script setup lang="ts">
// 详情页的指标速览（设计 11.1）：CPU、磁盘、内存、连接与进程四块，以及最近几分钟的实时网速。
// 迷你折线贴着卡片底边铺满（参考 Monito），数据来自详情页维护的实时采样（打开时用 1 小时历史预填）。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { fmtBytes, fmtBytesShort, fmtPct } from '../format'
import { cpu, fullestDisk, gaugeLevel, mem, rx, tx, type LiveSample } from '../metrics'
import Chart, { type Series } from './Chart.vue'
import Qty from './Qty.vue'
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
const diskLv = computed(() => gaugeLevel(disk.value?.usage, 'disk'))
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

// 图标：Lucide 路径（ISC），线宽 1.5（设计 41.4.3）
const icons = {
  cpu: 'M9 3v2M15 3v2M9 19v2M15 19v2M3 9h2M3 15h2M19 9h2M19 15h2M7 5h10v14H7zM10 9h4v6h-4z',
  disk: 'M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2ZM17 21v-8H7v8M7 3v5h8',
  mem: 'M12 2 2 7l10 5 10-5-10-5ZM2 17l10 5 10-5M2 12l10 5 10-5',
  conn: 'M4.9 19.1a10 10 0 0 1 0-14.2M7.8 16.2a6 6 0 0 1 0-8.4M16.2 7.8a6 6 0 0 1 0 8.4M19.1 4.9a10 10 0 0 1 0 14.2M12 13a1 1 0 1 0 0-2 1 1 0 0 0 0 2Z',
  speed: 'M22 12h-4l-3 9L9 3l-3 9H2',
}
</script>

<template>
  <div class="quick">
    <div class="tile spark-tile">
      <div class="label"><svg viewBox="0 0 24 24" aria-hidden="true"><path :d="icons.cpu" /></svg>CPU<span class="extra num">{{ r.cpu.cores }} 核</span></div>
      <div class="big" :class="cpuLv"><Qty :v="pct1(cpu(server))" u="%" /></div>
      <Sparkline class="edge" :values="samples.map((p) => p.cpu)" :color="color(cpuLv)" :height="36" />
    </div>

    <div class="tile">
      <div class="label"><svg viewBox="0 0 24 24" aria-hidden="true"><path :d="icons.disk" /></svg>磁盘<span v-if="disk && disk.mount !== '/'" class="extra">{{ disk.mount }}</span></div>
      <div class="ring-row">
        <div>
          <div class="big" :class="diskLv"><Qty :v="disk ? Math.round(disk.usage) : '—'" u="%" /></div>
          <div class="muted small num nowrap">{{ fmtBytesShort(disk?.used) }} / {{ fmtBytesShort(disk?.total) }}</div>
        </div>
        <Ring class="disk-ring" :pct="disk?.usage" :level="diskLv" :size="56" tinted bare label="磁盘" />
      </div>
    </div>

    <div class="tile spark-tile">
      <div class="label"><svg viewBox="0 0 24 24" aria-hidden="true"><path :d="icons.mem" /></svg>内存<span v-if="swapPct != null" class="extra num">Swap {{ fmtPct(swapPct) }}</span></div>
      <div class="big" :class="memLv"><Qty :v="pct1(mem(server))" u="%" /></div>
      <Sparkline class="edge" :values="samples.map((p) => p.mem)" :color="color(memLv)" :height="36" />
    </div>

    <div class="tile">
      <div class="label"><svg viewBox="0 0 24 24" aria-hidden="true"><path :d="icons.conn" /></svg>连接与进程</div>
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
        <div class="label" :title="minutes ? `最近 ${minutes} 分钟` : ''"><svg viewBox="0 0 24 24" aria-hidden="true"><path :d="icons.speed" /></svg>上传下载速率</div>
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
.label svg { width: 15px; height: 15px; flex: none; fill: none; stroke: currentColor; stroke-width: 1.5; stroke-linecap: round; stroke-linejoin: round; }
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
