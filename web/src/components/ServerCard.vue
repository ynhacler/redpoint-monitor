<script setup lang="ts">
// 节点卡片（设计 41.3 ServerCard）：名称、状态、系统与运行信息；CPU / 内存 / 磁盘三个环形图，网速与磁盘 IO；
// 本周期流量条。整张卡片可点击进入详情（设计 11）。
// 卡片宽度 ≥ 400px 时五列一行；更窄时环形图一行、网速与 IO 另起一行（容器查询，与所在网格列宽有关）。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { DASH, fmtBytes, fmtBytesShort, fmtDuration, fmtPct, fmtTraffic } from '../format'
import { cpu, fullestDisk, gaugeLevel, ioTotal, isLive, issues, mem, rx, trafficPct, tx } from '../metrics'
import Flag from './Flag.vue'
import Ring from './Ring.vue'
import StatusDot from './StatusDot.vue'
import UsageBar from './UsageBar.vue'

const props = defineProps<{
  /** 节点 */
  server: ServerView
}>()

const s = computed(() => props.server)
const r = computed(() => s.value.latest)
const live = computed(() => isLive(s.value))
const disk = computed(() => fullestDisk(s.value))
const io = computed(() => ioTotal(s.value))
const traffic = computed(() => trafficPct(s.value))
const problems = computed(() => issues(s.value))
const offlineFor = computed(() =>
  s.value.last_seen_at ? `离线 ${fmtDuration(Date.now() / 1000 - s.value.last_seen_at)}` : '尚未上报',
)
const subtitle = computed(() =>
  [[r.value?.system.os, r.value?.system.os_version].filter(Boolean).join(' '), s.value.provider || s.value.region]
    .filter(Boolean).join(' · '),
)
</script>

<template>
  <RouterLink :to="`/servers/${s.id}`" class="card" :class="[s.status, { attention: problems.length }]">
    <div class="head">
      <h2 class="name"><Flag :code="s.country" /> {{ s.name }}</h2>
      <StatusDot :status="s.status" />
    </div>
    <div class="sub small">
      <span class="muted ellipsis">{{ subtitle || DASH }}</span>
      <span v-if="live && r" class="chips muted num">
        <span v-if="r.cpu.temp_c" :class="{ warn: r.cpu.temp_c >= 75, bad: r.cpu.temp_c >= 90 }" title="CPU 温度">{{ Math.round(r.cpu.temp_c) }}℃</span>
        <span title="运行时间">⏻ {{ fmtDuration(r.system.uptime) }}</span>
        <span title="1 分钟负载">⚖ {{ r.cpu.load1.toFixed(2) }}</span>
      </span>
    </div>

    <!-- 离线时隐藏旧指标：旧数值看起来像实时数据，会误导（设计 43.6） -->
    <div v-if="live && r" class="metrics">
      <div class="col ring-col">
        <span class="label">CPU</span>
        <Ring :pct="cpu(s)" :level="gaugeLevel(cpu(s), 'cpu')" :size="48" :caption="`${r.cpu.cores} 核`" label="CPU" />
      </div>
      <div class="col ring-col">
        <span class="label">内存</span>
        <Ring :pct="mem(s)" :level="gaugeLevel(mem(s), 'mem')" :size="48" :caption="fmtBytesShort(r.memory.total)" label="内存" />
      </div>
      <div class="col ring-col" :title="(r.disk ?? []).map((d) => `${d.mount}  ${fmtPct(d.usage)}  ${fmtBytes(d.used)} / ${fmtBytes(d.total)}`).join('\n')">
        <span class="label ellipsis">{{ disk && disk.mount !== '/' ? disk.mount : '磁盘' }}</span>
        <Ring :pct="disk?.usage" :level="gaugeLevel(disk?.usage, 'disk')" :size="48" :caption="fmtBytesShort(disk?.total)" label="磁盘" />
      </div>
      <div class="col text-col" title="上行 / 下行速率">
        <span class="label">网络</span>
        <span class="num">↑{{ fmtBytesShort(tx(s), true) }}</span>
        <span class="num">↓{{ fmtBytesShort(rx(s), true) }}</span>
      </div>
      <div class="col text-col" title="磁盘读 / 写速率">
        <span class="label">IO</span>
        <template v-if="io">
          <span class="num">R {{ fmtBytesShort(io.read, true) }}</span>
          <span class="num">W {{ fmtBytesShort(io.write, true) }}</span>
        </template>
        <span v-else class="muted">{{ DASH }}</span>
      </div>
    </div>
    <div v-else class="offline-note" :class="{ bad: s.status === 'offline' }">{{ offlineFor }}</div>

    <div class="traffic">
      <div class="traffic-label small">
        <span class="muted">本周期流量</span>
        <span class="num">
          {{ fmtTraffic(s.traffic.used, s.traffic.unit) }}<span class="muted"> / {{ s.traffic.limit ? fmtTraffic(s.traffic.limit, s.traffic.unit) : '不限' }}</span>
        </span>
      </div>
      <UsageBar v-if="traffic != null" :pct="traffic" />
    </div>
  </RouterLink>
</template>

<style scoped>
.card {
  display: block; color: inherit; text-decoration: none; container-type: inline-size;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md);
  padding: var(--space-4); transition: border-color .15s;
}
.card:hover { border-color: var(--text-muted); text-decoration: none; }
.card.offline { border-color: color-mix(in srgb, var(--bad) 45%, var(--border)); }
.card.unknown { border-color: color-mix(in srgb, var(--warn) 45%, var(--border)); }
.head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); }
.name { font-size: var(--font-lg); line-height: var(--line-lg); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sub { margin-top: 2px; display: flex; gap: var(--space-2); align-items: baseline; justify-content: space-between; min-width: 0; }
.ellipsis { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
.chips { display: flex; gap: var(--space-2); white-space: nowrap; flex-shrink: 0; }
.chips .warn { color: var(--warn); }
.chips .bad { color: var(--bad); }

/* 窄卡片：三个环一行，网络与 IO 一行 */
.metrics {
  display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: var(--space-3) var(--space-2);
  margin: var(--space-4) 0; padding: var(--space-3) 0; border-top: 1px solid var(--border); border-bottom: 1px solid var(--border);
}
.col { display: flex; flex-direction: column; align-items: center; gap: var(--space-1); min-width: 0; font-size: var(--font-sm); line-height: var(--line-sm); }
.ring-col { grid-column: span 2; }
.text-col { grid-column: span 3; display: grid; grid-template-columns: auto auto; justify-content: center; column-gap: var(--space-3); }
.text-col .label { grid-column: 1 / -1; text-align: center; }
.label { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); max-width: 100%; }
/* 宽卡片：五列一行，网络与 IO 竖排 */
@container (min-width: 400px) {
  .metrics { grid-template-columns: repeat(3, minmax(0, 1fr)) minmax(0, 1.3fr) minmax(0, 1.3fr); }
  .ring-col, .text-col { grid-column: auto; }
  .text-col { display: flex; align-items: flex-start; }
  .text-col .label { align-self: center; }
}
.offline-note { margin: var(--space-4) 0; color: var(--text-muted); }
.offline-note.bad { color: var(--bad); }
.traffic-label { display: flex; justify-content: space-between; margin-bottom: var(--space-1); }
</style>
