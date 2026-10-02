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
import Icon from './Icon.vue'
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
const load1 = computed(() => (r.value ? r.value.cpu.load1.toFixed(r.value.cpu.load1 < 10 ? 2 : 1) : DASH))
const tempLv = computed(() => {
  const t = r.value?.cpu.temp_c ?? 0
  return t >= 90 ? 'bad' : t >= 75 ? 'warn' : 'ok'
})

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
const tlv = computed(() => (traffic.value == null ? 'ok' : traffic.value >= 95 ? 'bad' : traffic.value >= 80 ? 'warn' : ''))
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
      <div v-if="live && r" class="meta num">
        <span v-if="r.cpu.temp_c" :class="tempLv" title="CPU 温度"><Icon name="thermometer" :size="15" />{{ Math.round(r.cpu.temp_c) }}℃</span>
        <span title="运行时间"><Icon name="power" :size="15" />{{ uptime }}</span>
        <span title="1 分钟负载" :class="{ warn: r.cpu.cores > 0 && r.cpu.load1 > r.cpu.cores }"><Icon name="activity" :size="15" />{{ load1 }}</span>
      </div>
      <span v-else class="meta" :class="s.status === 'offline' ? 'bad' : ''">{{ offlineFor }}</span>
    </div>

    <!-- 离线时不显示旧指标：旧数值看起来像实时数据，会误导（设计 43.6） -->
    <div v-if="live && r" class="metrics num">
      <div class="col">
        <span class="label">CPU</span>
        <Ring :pct="cpu(s)" :level="gaugeLevel(cpu(s), 'cpu')" :size="52" tinted label="CPU" />
        <span class="cap">{{ r.cpu.cores }} 核</span>
      </div>
      <div class="col">
        <span class="label">内存</span>
        <Ring :pct="mem(s)" :level="gaugeLevel(mem(s), 'mem')" :size="52" tinted label="内存" />
        <span class="cap">{{ fmtBytesShort(r.memory.total) }}</span>
      </div>
      <div class="col" :title="diskTip">
        <span class="label">{{ disk && disk.mount !== '/' ? disk.mount : '磁盘' }}</span>
        <Ring :pct="disk?.usage" :level="gaugeLevel(disk?.usage, 'disk')" :size="52" tinted label="磁盘" />
        <span class="cap">{{ fmtBytesShort(disk?.total) }}</span>
      </div>
      <div class="col" :title="`上行 ${fmtBytes(tx(s), true)}，开机以来 ${fmtBytes(netTotal.tx)}\n下行 ${fmtBytes(rx(s), true)}，开机以来 ${fmtBytes(netTotal.rx)}`">
        <span class="label">网络</span>
        <div class="flow">
          <span class="v"><Icon name="arrow-up" :size="12" />{{ fmtBytesShort(tx(s)) }}</span>
          <span class="t">{{ fmtBytesShort(netTotal.tx) }}</span>
          <span class="v"><Icon name="arrow-down" :size="12" />{{ fmtBytesShort(rx(s)) }}</span>
          <span class="t">{{ fmtBytesShort(netTotal.rx) }}</span>
        </div>
      </div>
      <div class="col" :title="io ? `写 ${fmtBytes(io.write, true)}，开机以来 ${fmtBytes(io.writeTotal)}\n读 ${fmtBytes(io.read, true)}，开机以来 ${fmtBytes(io.readTotal)}` : '旧版 Agent 未上报磁盘 IO'">
        <span class="label">IO</span>
        <div v-if="io" class="flow">
          <span class="v"><Icon name="arrow-up" :size="12" />{{ fmtBytesShort(io.write) }}</span>
          <span class="t">{{ fmtBytesShort(io.writeTotal) }}</span>
          <span class="v"><Icon name="arrow-down" :size="12" />{{ fmtBytesShort(io.read) }}</span>
          <span class="t">{{ fmtBytesShort(io.readTotal) }}</span>
        </div>
        <div v-else class="flow empty"><span class="t">{{ DASH }}</span></div>
      </div>
    </div>

    <div class="traffic num" :title="`本周期 ${s.traffic.cycle_start} 起`">
      <span class="muted">本周期</span>
      <span>{{ fmtTraffic(s.traffic.used, s.traffic.unit) }}<span class="muted"> / {{ s.traffic.limit ? fmtTraffic(s.traffic.limit, s.traffic.unit) : '不限' }}</span></span>
      <span v-if="traffic != null" class="tbar"><span :class="tlv" :style="{ width: `${Math.min(100, traffic)}%` }" /></span>
      <span v-if="traffic != null" :class="tlv">{{ fmtPct(traffic) }}</span>
    </div>
  </RouterLink>
</template>

<style scoped>
.card {
  display: block; color: inherit; text-decoration: none;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md);
  padding: var(--space-3) var(--space-4); transition: border-color .15s; container-type: inline-size;
  /* 环 52px + 间距 4px + 说明 18px：网络与 IO 的四行均分这个高度，与环和说明上下对齐 */
  --body-h: 74px;
}
.card:hover { border-color: var(--text-muted); text-decoration: none; }
.card.offline { border-color: color-mix(in srgb, var(--bad) 45%, var(--border)); }
.card.unknown { border-color: color-mix(in srgb, var(--warn) 45%, var(--border)); }
.ok { color: var(--ok); }
.warn { color: var(--warn); }
.bad { color: var(--bad); }

.head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3);
  padding-bottom: var(--space-3); border-bottom: 1px solid var(--border); }
.title { display: flex; align-items: center; gap: var(--space-2); min-width: 0; }
.name { font-size: var(--font-lg); line-height: var(--line-lg); font-weight: var(--weight-strong); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.meta { display: flex; align-items: center; gap: var(--space-3); color: var(--text-muted); font-size: var(--font-md); line-height: var(--line-md); white-space: nowrap; flex: none; }
.meta span { display: inline-flex; align-items: center; gap: 3px; }
/* 窄卡片（网格最小列宽 320px）：右侧信息缩小一档，把空间留给名称 */
@container (max-width: 380px) {
  .meta { gap: var(--space-2); font-size: var(--font-sm); line-height: var(--line-sm); }
  .meta :deep(svg) { width: 13px; height: 13px; }
}

.metrics { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); column-gap: var(--space-1); padding: var(--space-3) 0; }
.col { display: flex; flex-direction: column; align-items: center; gap: var(--space-1); min-width: 0; }
.label { font-size: var(--font-md); line-height: var(--line-md); color: var(--text); max-width: 100%; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; margin-bottom: 2px; }
.cap { font-size: var(--font-sm); line-height: 18px; color: var(--text); white-space: nowrap; }
.flow { height: var(--body-h); display: flex; flex-direction: column; justify-content: space-between; align-items: center;
  font-size: var(--font-sm); line-height: 18px; white-space: nowrap; }
.flow.empty { justify-content: center; }
.flow .v { display: inline-flex; align-items: center; gap: 1px; color: var(--text); }
.flow .v svg { color: var(--text-muted); }
.flow .t { color: var(--text-muted); }

.traffic { display: flex; align-items: center; gap: var(--space-2); padding-top: var(--space-2); border-top: 1px solid var(--border);
  font-size: var(--font-sm); line-height: var(--line-sm); white-space: nowrap; }
.tbar { flex: 1; height: 4px; border-radius: var(--radius-full); background: var(--track); overflow: hidden; min-width: 24px; }
.tbar span { display: block; height: 100%; background: var(--ok); }
.tbar span.warn { background: var(--warn); }
.tbar span.bad { background: var(--bad); }
.tbar span.ok { background: var(--ok); }
</style>
