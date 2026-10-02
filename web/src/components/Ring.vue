<script setup lang="ts">
// 环形指标（设计 41.3 Ring）：节点卡片与详情页中的 CPU / 内存 / 磁盘使用率。
// 单值时按健康程度着色（ok / warn / bad）；分段时依次绘制，如内存的“已用 + 缓存”。
// 中间显示百分比，下方可带一行说明（如 “4 核”“2.1 GB”）。
import { computed } from 'vue'
import { fmtPct } from '../format'

export interface RingSegment {
  /** 0～100 */
  value: number
  /** CSS 颜色，只用设计令牌，如 'var(--series-2)' */
  color: string
}

const props = defineProps<{
  /** 中间显示的百分比，0～100；没有数据时为 undefined，显示 “—” */
  pct: number | undefined
  /** 单值着色 */
  level?: 'ok' | 'warn' | 'bad'
  /** 分段（覆盖 pct 的单段绘制，中间仍显示 pct） */
  segments?: RingSegment[]
  /** 直径 px，默认 52 */
  size?: number
  /** 环下方的说明 */
  caption?: string
  /** 无障碍名称，如 “CPU 使用率” */
  label?: string
  /** 着色风格：底轨用当前状态色的淡色、百分比文字同色（节点列表的紧凑卡片） */
  tinted?: boolean
}>()

const levelColor = computed(() => `var(--${props.level ?? 'ok'})`)
const trackColor = computed(() =>
  props.tinted && !props.segments ? `color-mix(in srgb, ${levelColor.value} 20%, transparent)` : 'var(--track)',
)

const d = computed(() => props.size ?? 52)
const stroke = computed(() => Math.max(4, Math.round(d.value / 10)))
const r = computed(() => (d.value - stroke.value) / 2)
const circ = computed(() => 2 * Math.PI * r.value)
const clamp = (v: number) => Math.max(0, Math.min(100, v))

// 每段的 dasharray / dashoffset：依次首尾相接，从 12 点方向顺时针
const arcs = computed(() => {
  const segs = props.segments ?? (props.pct == null ? [] : [{ value: props.pct, color: levelColor.value }])
  let start = 0
  return segs.map((s) => {
    const len = (clamp(s.value) / 100) * circ.value
    const arc = { color: s.color, dash: `${len} ${circ.value - len}`, offset: -start }
    start += len
    return arc
  })
})
</script>

<template>
  <figure class="ring" :style="{ width: `${d}px` }" role="img" :aria-label="`${label ?? ''} ${fmtPct(pct)}`">
    <svg :width="d" :height="d" :viewBox="`0 0 ${d} ${d}`">
      <g :transform="`rotate(-90 ${d / 2} ${d / 2})`" fill="none" :stroke-width="stroke">
        <circle :cx="d / 2" :cy="d / 2" :r="r" :stroke="trackColor" />
        <circle v-for="(a, i) in arcs" :key="i" :cx="d / 2" :cy="d / 2" :r="r" :stroke="a.color"
          :stroke-dasharray="a.dash" :stroke-dashoffset="a.offset" :stroke-linecap="segments ? 'butt' : 'round'" />
      </g>
      <text v-if="pct != null || !segments" :x="d / 2" :y="d / 2" text-anchor="middle" dominant-baseline="central" class="pct"
        :class="[segments ? '' : level, { tinted: tinted && !segments }]" :style="{ fontSize: `${Math.round(d / 4.6)}px` }">{{ fmtPct(pct) }}</text>
    </svg>
    <figcaption v-if="caption" class="caption num">{{ caption }}</figcaption>
  </figure>
</template>

<style scoped>
.ring { margin: 0; display: flex; flex-direction: column; align-items: center; gap: var(--space-1); min-width: 0; }
.ring svg { display: block; }
.pct { fill: var(--text); font-family: var(--font-mono); font-weight: var(--weight-strong); }
.pct.warn { fill: var(--warn); }
.pct.bad { fill: var(--bad); }
.pct.tinted.ok { fill: var(--ok); }
.caption { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); white-space: nowrap; max-width: 100%; overflow: hidden; text-overflow: ellipsis; }
</style>
