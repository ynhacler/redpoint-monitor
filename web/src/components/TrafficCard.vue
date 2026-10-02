<script setup lang="ts">
// 流量套餐卡片（设计 41.3 TrafficCard、1.5.8）：已用 / 总量、剩余、距离重置、日均、预计。
// “预计”按本周期日均线性估算；正式的流量预测见设计第 32 章（TODO(A4)）。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { countModeNames, DASH, fmtBytes, fmtPct } from '../format'
import { daysToReset, trafficPct } from '../metrics'
import UsageBar from './UsageBar.vue'

const props = defineProps<{
  /** 节点 */
  server: ServerView
}>()

const t = computed(() => props.server.traffic)
const pct = computed(() => trafficPct(props.server))
const left = computed(() => daysToReset(props.server))
// 周期已过天数（至少按 1 天算，避免周期第一天日均被放大）
const elapsedDays = computed(() => {
  const start = new Date(t.value.cycle_start + 'T00:00:00')
  return Math.max(1, (Date.now() - start.getTime()) / 86400000)
})
const daily = computed(() => t.value.used / elapsedDays.value)
const forecast = computed(() => t.value.used + daily.value * left.value)
const over = computed(() => t.value.limit > 0 && forecast.value > t.value.limit)
</script>

<template>
  <div class="panel traffic">
    <div class="top">
      <h3>本周期流量</h3>
      <span class="muted small">{{ countModeNames[server.traffic_count_mode] ?? server.traffic_count_mode }} · 每月 {{ server.traffic_reset_day }} 日重置</span>
    </div>
    <div class="big num">
      {{ fmtBytes(t.used) }}<span class="muted"> / {{ t.limit ? fmtBytes(t.limit) : '不限' }}</span>
      <span v-if="pct != null" class="pct" :class="{ warn: pct >= 80, bad: pct >= 95 }">{{ fmtPct(pct) }}</span>
    </div>
    <UsageBar v-if="pct != null" :pct="pct" />
    <dl class="facts">
      <div><dt>剩余</dt><dd class="num">{{ t.limit ? fmtBytes(Math.max(0, t.limit - t.used)) : DASH }}</dd></div>
      <div><dt>距离重置</dt><dd class="num">{{ left }} 天</dd></div>
      <div><dt>日均</dt><dd class="num">{{ fmtBytes(daily) }}</dd></div>
      <div :title="'按本周期日均估算'">
        <dt>预计周期结束</dt>
        <dd class="num" :class="{ bad: over }">{{ fmtBytes(forecast) }}</dd>
      </div>
      <div><dt>入站 / 出站</dt><dd class="num">{{ fmtBytes(t.rx) }} / {{ fmtBytes(t.tx) }}</dd></div>
    </dl>
  </div>
</template>

<style scoped>
.top { display: flex; justify-content: space-between; align-items: baseline; gap: var(--space-2); flex-wrap: wrap; }
.big { font-size: var(--font-num); line-height: var(--line-num); margin: var(--space-3) 0; display: flex; align-items: baseline; gap: var(--space-2); flex-wrap: wrap; }
.big .muted { font-size: var(--font-lg); }
.pct { font-size: var(--font-md); margin-left: auto; }
.facts { display: grid; grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); gap: var(--space-3); margin: var(--space-4) 0 0; }
.facts dt { font-size: var(--font-xs); color: var(--text-muted); }
.facts dd { margin: 0; }
</style>
