<script setup lang="ts">
// 节点详情顶部的概况卡片（设计 11.1）：名称与状态、CPU 型号，以及系统、负载、运行时间、容量、本周期流量等信息块。
// 只显示读一眼就能用的静态或慢变信息；实时指标在下方的指标块中。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { DASH, fmtBytes, fmtTraffic, fmtUptime } from '../format'
import Flag from './Flag.vue'
import StatusDot from './StatusDot.vue'

const props = defineProps<{
  /** 节点（latest 可能不存在：尚未上报或离线很久） */
  server: ServerView
  /** 是否在线；离线时不显示负载等实时值 */
  live: boolean
}>()

const s = computed(() => props.server)
const r = computed(() => s.value.latest)
const sys = computed(() => r.value?.system)
const diskTotal = computed(() => r.value?.disk.reduce((a, d) => a + d.total, 0))
const subtitle = computed(() =>
  [s.value.ipv4 || s.value.expected_ipv4, s.value.region, s.value.provider, s.value.group].filter(Boolean).join(' · '),
)

// 信息块：图标为 Lucide 路径（ISC 许可），线宽 1.5（设计 41.4.3）
const tiles = computed(() => [
  { icon: 'M4 6h16M4 12h16M4 18h10', label: '操作系统', value: [sys.value?.os, sys.value?.os_version].filter(Boolean).join(' ') || DASH },
  { icon: 'M9 3v2M15 3v2M9 19v2M15 19v2M3 9h2M3 15h2M19 9h2M19 15h2M7 5h10v14H7z', label: '架构', value: sys.value?.arch || DASH },
  {
    icon: 'M3 3v18h18M7 15l4-4 3 3 5-6', label: '负载 1 / 5 / 15',
    value: props.live && r.value
      ? [r.value.cpu.load1, r.value.cpu.load5, r.value.cpu.load15].filter((v) => v != null).map((v) => v!.toFixed(2)).join(' / ')
      : DASH,
  },
  { icon: 'M12 6v6l4 2M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20Z', label: '运行时间', value: props.live ? fmtUptime(sys.value?.uptime) : DASH },
  {
    icon: 'M12 2 2 7l10 5 10-5-10-5ZM2 17l10 5 10-5M2 12l10 5 10-5', label: '内存',
    value: r.value ? fmtBytes(r.value.memory.total) : DASH,
    extra: r.value?.swap.total ? `Swap ${fmtBytes(r.value.swap.total)}` : '',
  },
  {
    icon: 'M22 12H2M5.5 5h13L22 12v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-6L5.5 5ZM6 16h.01M10 16h.01', label: '磁盘',
    value: diskTotal.value ? fmtBytes(diskTotal.value) : DASH,
    extra: r.value && r.value.disk.length > 1 ? `${r.value.disk.length} 个挂载点` : '',
  },
  { icon: 'M12 19V5M5 12l7-7 7 7', label: '本周期上传', value: fmtTraffic(s.value.traffic.tx, s.value.traffic.unit), tone: 'up' },
  { icon: 'M12 5v14M19 12l-7 7-7-7', label: '本周期下载', value: fmtTraffic(s.value.traffic.rx, s.value.traffic.unit), tone: 'down' },
])
</script>

<template>
  <section class="panel summary">
    <div class="head">
      <div class="name-row">
        <Flag :code="s.country" />
        <h1>{{ s.name }}</h1>
      </div>
      <span class="badge" :class="s.status"><StatusDot :status="s.status" /></span>
    </div>
    <p v-if="subtitle" class="muted small sub">{{ subtitle }}</p>

    <div v-if="sys?.cpu_model" class="tile cpu">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 3v2M15 3v2M9 19v2M15 19v2M3 9h2M3 15h2M19 9h2M19 15h2M7 5h10v14H7zM10 9h4v6h-4z" /></svg>
      <div><div class="label">CPU</div><div class="value ellipsis" :title="sys.cpu_model">{{ sys.cpu_model }}<span v-if="r" class="muted"> · {{ r.cpu.cores }} 核</span></div></div>
    </div>
    <div class="tiles">
      <div v-for="t in tiles" :key="t.label" class="tile" :class="t.tone">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path :d="t.icon" /></svg>
        <div class="body">
          <div class="label">{{ t.label }}</div>
          <div class="value num">{{ t.value }}<span v-if="t.extra" class="muted extra">{{ t.extra }}</span></div>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.summary { padding: var(--space-4); }
.head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
.name-row { display: flex; align-items: center; gap: var(--space-2); min-width: 0; font-size: var(--font-xl); }
.name-row h1 { margin: 0; font-size: var(--font-xl); line-height: var(--line-xl); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.badge { padding: 2px var(--space-2); border-radius: var(--radius-full); background: var(--surface-2); flex: none; }
.badge.online { background: color-mix(in srgb, var(--ok) 14%, transparent); color: var(--ok); }
.badge.offline { background: color-mix(in srgb, var(--bad) 14%, transparent); color: var(--bad); }
.badge.unknown { background: color-mix(in srgb, var(--warn) 14%, transparent); color: var(--warn); }
.sub { margin: 2px 0 0; }
.head + .tile, .head + .tiles, .sub + .tile, .sub + .tiles { margin-top: var(--space-3); }
.tiles { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: var(--space-2); }
.tile { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-2) var(--space-3); border-radius: var(--radius-sm);
  background: var(--surface-2); min-width: 0; }
.tile.cpu { margin-bottom: var(--space-2); }
.tile.cpu > div { min-width: 0; }
.tile svg { width: 18px; height: 18px; flex: none; fill: none; stroke: var(--accent); stroke-width: 1.5; stroke-linecap: round; stroke-linejoin: round; }
.tile.up svg { stroke: var(--ok); }
.tile.down svg { stroke: var(--accent); }
.body { min-width: 0; }
.label { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); }
.value { font-size: var(--font-md); line-height: var(--line-md); font-weight: var(--weight-strong); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.extra { display: block; font-size: var(--font-xs); line-height: var(--line-xs); font-weight: var(--weight-regular); }
.ellipsis { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
@media (max-width: 600px) {
  .tile { padding: var(--space-2); gap: var(--space-2); }
  .value { font-size: var(--font-sm); line-height: var(--line-sm); }
}
</style>
