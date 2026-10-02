<script setup lang="ts">
// 节点卡片（设计 41.3 ServerCard）：名称、状态、系统；CPU / 内存 / 磁盘 / 网速四项指标；本周期流量条。
// 整张卡片可点击进入详情（设计 11）。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { DASH, fmtBytes, fmtDuration, fmtPct } from '../format'
import { cpu, fullestDisk, isLive, issues, mem, rx, thresholds, trafficPct, tx } from '../metrics'
import Metric from './Metric.vue'
import StatusDot from './StatusDot.vue'
import UsageBar from './UsageBar.vue'

const props = defineProps<{
  /** 节点 */
  server: ServerView
}>()

const s = computed(() => props.server)
const live = computed(() => isLive(s.value))
const disk = computed(() => fullestDisk(s.value))
const traffic = computed(() => trafficPct(s.value))
const problems = computed(() => issues(s.value))
const level = (v: number | undefined, warn: number, bad = 101) =>
  v == null ? undefined : v >= bad ? 'bad' : v >= warn ? 'warn' : undefined
const offlineFor = computed(() =>
  s.value.last_seen_at ? `离线 ${fmtDuration(Date.now() / 1000 - s.value.last_seen_at)}` : '尚未上报',
)
const subtitle = computed(() =>
  [s.value.region, s.value.provider].filter(Boolean).join(' · ') ||
  [s.value.latest?.system.os, s.value.latest?.system.os_version].filter(Boolean).join(' '),
)
</script>

<template>
  <RouterLink :to="`/servers/${s.id}`" class="card" :class="[s.status, { attention: problems.length }]">
    <div class="head">
      <h2 class="name">{{ s.name }}</h2>
      <StatusDot :status="s.status" />
    </div>
    <div class="sub muted small">{{ subtitle || DASH }}</div>

    <!-- 离线时隐藏旧指标：旧数值看起来像实时数据，会误导（设计 43.6） -->
    <div v-if="live" class="metrics">
      <Metric label="CPU" :value="fmtPct(cpu(s))" :level="level(cpu(s), thresholds.cpu)" />
      <Metric label="内存" :value="fmtPct(mem(s))" :level="level(mem(s), thresholds.mem)" />
      <Metric :label="disk && disk.mount !== '/' ? `磁盘 ${disk.mount}` : '磁盘'" :value="fmtPct(disk?.usage)"
        :level="level(disk?.usage, thresholds.disk, thresholds.diskBad)"
        :hint="(s.latest?.disk ?? []).map((d) => `${d.mount}  ${fmtPct(d.usage)}  ${fmtBytes(d.used)} / ${fmtBytes(d.total)}`).join('\n')" />
      <Metric label="网络" :value="`↓${fmtBytes(rx(s), true)} ↑${fmtBytes(tx(s), true)}`" />
    </div>
    <div v-else class="offline-note" :class="{ bad: s.status === 'offline' }">{{ offlineFor }}</div>

    <div class="traffic">
      <div class="traffic-label small">
        <span class="muted">本周期流量</span>
        <span class="num">
          {{ fmtBytes(s.traffic.used) }}<span class="muted"> / {{ s.traffic.limit ? fmtBytes(s.traffic.limit) : '不限' }}</span>
        </span>
      </div>
      <UsageBar v-if="traffic != null" :pct="traffic" />
    </div>
  </RouterLink>
</template>

<style scoped>
.card {
  display: block; color: inherit; text-decoration: none;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md);
  padding: var(--space-4); transition: border-color .15s;
}
.card:hover { border-color: var(--text-muted); text-decoration: none; }
.card.offline { border-color: color-mix(in srgb, var(--bad) 45%, var(--border)); }
.card.unknown { border-color: color-mix(in srgb, var(--warn) 45%, var(--border)); }
.head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); }
.name { font-size: var(--font-lg); line-height: var(--line-lg); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sub { margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.metrics { display: grid; grid-template-columns: repeat(3, auto) 1fr; gap: var(--space-4); margin: var(--space-4) 0; }
.offline-note { margin: var(--space-4) 0; color: var(--text-muted); }
.offline-note.bad { color: var(--bad); }
.traffic-label { display: flex; justify-content: space-between; margin-bottom: var(--space-1); }
</style>
