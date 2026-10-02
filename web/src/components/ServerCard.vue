<script setup lang="ts">
// 节点卡片（设计 41.3 ServerCard）：紧凑的一行五列，便于在一屏里扫过很多台节点。
//   头部：国旗、名称、状态；右侧温度、运行时间、1 分钟负载
//   主体：CPU / 内存 / 磁盘三个环（下方为核数、总量），网络与 IO 两列（速率 + 开机以来累计）
//   底部：本周期流量（流量是本产品的核心，保留在卡片上）
// 整张卡片可点击进入详情（设计 11）。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { DASH, fmtBytes, fmtBytesShort, fmtDuration, fmtPct, fmtTraffic } from '../format'
import { cpu, fullestDisk, gaugeLevel, isLive, issues, mem, rx, trafficPct, tx } from '../metrics'
import Flag from './Flag.vue'
import Ring from './Ring.vue'
import StatusDot from './StatusDot.vue'

const props = defineProps<{
  /** 节点 */
  server: ServerView
}>()

const s = computed(() => props.server)
const r = computed(() => s.value.latest)
const live = computed(() => isLive(s.value))
const disk = computed(() => fullestDisk(s.value))
const traffic = computed(() => trafficPct(s.value))
const problems = computed(() => issues(s.value))
const offlineFor = computed(() =>
  s.value.last_seen_at ? `离线 ${fmtDuration(Date.now() / 1000 - s.value.last_seen_at)}` : '尚未上报',
)
const os = computed(() => [r.value?.system.os, r.value?.system.os_version].filter(Boolean).join(' '))
// 运行时间只取最大单位，与列表的紧凑风格一致：“10天”“8小时”
const uptime = computed(() => fmtDuration(r.value?.system.uptime).replace(' ', ''))

// 网络：速率 + 开机以来累计；IO：读写速率 + 累计（各磁盘之和）
const netTotal = computed(() => ({
  rx: r.value?.network.reduce((a, n) => a + n.rx_bytes, 0) ?? 0,
  tx: r.value?.network.reduce((a, n) => a + n.tx_bytes, 0) ?? 0,
}))
const io = computed(() => {
  const list = r.value?.disk_io
  if (!list?.length) return undefined
  return list.reduce(
    (a, d) => ({
      read: a.read + d.read_speed, write: a.write + d.write_speed,
      readTotal: a.readTotal + (d.read_bytes ?? 0), writeTotal: a.writeTotal + (d.write_bytes ?? 0),
    }),
    { read: 0, write: 0, readTotal: 0, writeTotal: 0 },
  )
})
const diskTip = computed(() =>
  (r.value?.disk ?? []).map((d) => `${d.mount}  ${fmtPct(d.usage)}  ${fmtBytes(d.used)} / ${fmtBytes(d.total)}`).join('\n'),
)
</script>

<template>
  <RouterLink :to="`/servers/${s.id}`" class="card" :class="[s.status, { attention: problems.length }]">
    <div class="head">
      <div class="title">
        <Flag :code="s.country" />
        <h2 class="name" :title="[s.name, os, s.provider, s.region].filter(Boolean).join(' · ')">{{ s.name }}</h2>
        <StatusDot :status="s.status" dot-only />
      </div>
      <div v-if="live && r" class="meta num small">
        <span v-if="r.cpu.temp_c" class="temp" :class="{ warn: r.cpu.temp_c >= 75, bad: r.cpu.temp_c >= 90 }" title="CPU 温度">
          {{ Math.round(r.cpu.temp_c) }}℃
        </span>
        <span title="运行时间"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3v8M6.3 6.3a8 8 0 1 0 11.4 0" /></svg>{{ uptime }}</span>
        <span title="1 分钟负载" :class="{ warn: r.cpu.cores > 0 && r.cpu.load1 > r.cpu.cores }">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 4h18v16H3zM6 15l3-4 3 3 3-5 3 4" /></svg>{{ r.cpu.load1.toFixed(r.cpu.load1 < 10 ? 2 : 1) }}
        </span>
      </div>
      <span v-else class="small" :class="s.status === 'offline' ? 'bad' : 'muted'">{{ offlineFor }}</span>
    </div>

    <!-- 离线时不显示旧指标：旧数值看起来像实时数据，会误导（设计 43.6） -->
    <div v-if="live && r" class="metrics">
      <div class="col">
        <span class="label">CPU</span>
        <Ring :pct="cpu(s)" :level="gaugeLevel(cpu(s), 'cpu')" :size="46" tinted :caption="`${r.cpu.cores} 核`" label="CPU" />
      </div>
      <div class="col">
        <span class="label">内存</span>
        <Ring :pct="mem(s)" :level="gaugeLevel(mem(s), 'mem')" :size="46" tinted :caption="fmtBytesShort(r.memory.total)" label="内存" />
      </div>
      <div class="col" :title="diskTip">
        <span class="label">{{ disk && disk.mount !== '/' ? disk.mount : '磁盘' }}</span>
        <Ring :pct="disk?.usage" :level="gaugeLevel(disk?.usage, 'disk')" :size="46" tinted :caption="fmtBytesShort(disk?.total)" label="磁盘" />
      </div>
      <div class="col text" :title="`上行 ${fmtBytes(tx(s), true)}，开机以来 ${fmtBytes(netTotal.tx)}\n下行 ${fmtBytes(rx(s), true)}，开机以来 ${fmtBytes(netTotal.rx)}`">
        <span class="label">网络</span>
        <span class="v">↑{{ fmtBytesShort(tx(s)) }}</span>
        <span class="t">{{ fmtBytesShort(netTotal.tx) }}</span>
        <span class="v">↓{{ fmtBytesShort(rx(s)) }}</span>
        <span class="t">{{ fmtBytesShort(netTotal.rx) }}</span>
      </div>
      <div v-if="io" class="col text" :title="`写 ${fmtBytes(io.write, true)}，开机以来 ${fmtBytes(io.writeTotal)}\n读 ${fmtBytes(io.read, true)}，开机以来 ${fmtBytes(io.readTotal)}`">
        <span class="label">IO</span>
        <span class="v">W {{ fmtBytesShort(io.write) }}</span>
        <span class="t">{{ fmtBytesShort(io.writeTotal) }}</span>
        <span class="v">R {{ fmtBytesShort(io.read) }}</span>
        <span class="t">{{ fmtBytesShort(io.readTotal) }}</span>
      </div>
      <div v-else class="col text"><span class="label">IO</span><span class="t">{{ DASH }}</span></div>
    </div>

    <div class="traffic small" :title="`本周期 ${s.traffic.cycle_start} 起`">
      <span class="muted">本周期</span>
      <span class="num">{{ fmtTraffic(s.traffic.used, s.traffic.unit) }}<span class="muted"> / {{ s.traffic.limit ? fmtTraffic(s.traffic.limit, s.traffic.unit) : '不限' }}</span></span>
      <span v-if="traffic != null" class="tbar"><span :class="{ warn: traffic >= 80, bad: traffic >= 95 }" :style="{ width: `${Math.min(100, traffic)}%` }" /></span>
      <span v-if="traffic != null" class="num" :class="{ warn: traffic >= 80, bad: traffic >= 95 }">{{ fmtPct(traffic) }}</span>
    </div>
  </RouterLink>
</template>

<style scoped>
.card {
  display: block; color: inherit; text-decoration: none;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md);
  padding: var(--space-3) var(--space-4); transition: border-color .15s;
}
.card:hover { border-color: var(--text-muted); text-decoration: none; }
.card.offline { border-color: color-mix(in srgb, var(--bad) 45%, var(--border)); }
.card.unknown { border-color: color-mix(in srgb, var(--warn) 45%, var(--border)); }
.warn { color: var(--warn); }
.bad { color: var(--bad); }

.head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); padding-bottom: var(--space-2); border-bottom: 1px solid var(--border); }
.title { display: flex; align-items: center; gap: var(--space-2); min-width: 0; }
.name { font-size: var(--font-md); line-height: var(--line-md); font-weight: var(--weight-strong); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.meta { display: flex; gap: var(--space-2); color: var(--text-muted); white-space: nowrap; flex: none; }
.meta span { display: inline-flex; align-items: center; gap: 2px; }
.meta svg { width: 12px; height: 12px; fill: none; stroke: currentColor; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
.temp { color: var(--ok); }

/* 五列固定：卡片最窄约 320px（节点列表网格最小列宽），每列约 58px */
.metrics { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: var(--space-1); padding: var(--space-3) 0 var(--space-2); }
.col { display: flex; flex-direction: column; align-items: center; gap: var(--space-1); min-width: 0; }
.label { font-size: var(--font-sm); line-height: var(--line-sm); color: var(--text); max-width: 100%; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.text { gap: 0; font-family: var(--font-mono); font-size: var(--font-xs); line-height: 17px; white-space: nowrap; }
.text .label { font-family: var(--font-family); margin-bottom: var(--space-1); }
.text .v { color: var(--text); }
.text .t { color: var(--text-muted); }

.traffic { display: flex; align-items: center; gap: var(--space-2); padding-top: var(--space-2); border-top: 1px solid var(--border); white-space: nowrap; }
.tbar { flex: 1; height: 4px; border-radius: var(--radius-full); background: var(--track); overflow: hidden; min-width: 24px; }
.tbar span { display: block; height: 100%; background: var(--ok); }
.tbar span.warn { background: var(--warn); }
.tbar span.bad { background: var(--bad); }
</style>
