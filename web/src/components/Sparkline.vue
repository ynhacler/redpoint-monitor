<script setup lang="ts">
// 迷你折线（设计 41.3 Sparkline）：卡片内的近期趋势，无坐标轴、无交互。
// 宽度随容器伸缩（viewBox + preserveAspectRatio=none），线宽不随拉伸变化。
import { computed } from 'vue'

const props = defineProps<{
  /** 按时间顺序的数值；null 表示该点没有数据，折线在此断开 */
  values: (number | null)[]
  /** 纵轴上限；不填时取数据最大值的 1.3 倍（留出余量，曲线落在中部；至少为 1，避免全 0 时除零） */
  max?: number
  /** CSS 颜色，只用设计令牌 */
  color?: string
  /** 高度 px，默认 32 */
  height?: number
}>()

const W = 100
const h = computed(() => props.height ?? 32)
const top = computed(() => props.max ?? Math.max(1, Math.max(...props.values.map((v) => v ?? 0)) * 1.3))
// 折线按 null 断成多段；每段同时生成面积
const parts = computed(() => {
  const n = props.values.length
  if (n < 2) return []
  const out: { line: string; area: string }[] = []
  let cur: [number, number][] = []
  const flush = () => {
    if (cur.length > 1) {
      const line = cur.map(([x, y]) => `${x.toFixed(2)},${y.toFixed(2)}`).join(' ')
      out.push({ line, area: `${cur[0][0]},${h.value} ${line} ${cur[cur.length - 1][0]},${h.value}` })
    }
    cur = []
  }
  props.values.forEach((v, i) => {
    if (v == null) return flush()
    const x = (i / (n - 1)) * W
    const y = h.value - 1 - (Math.min(v, top.value) / top.value) * (h.value - 2)
    cur.push([x, y])
  })
  flush()
  return out
})
</script>

<template>
  <svg class="spark" :viewBox="`0 0 ${W} ${h}`" preserveAspectRatio="none" :style="{ height: `${h}px` }" aria-hidden="true">
    <g v-for="(p, i) in parts" :key="i">
      <polygon :points="p.area" :fill="color ?? 'var(--accent)'" fill-opacity="0.1" />
      <polyline :points="p.line" fill="none" :stroke="color ?? 'var(--accent)'" stroke-width="1.5" vector-effect="non-scaling-stroke"
        stroke-linejoin="round" stroke-linecap="round" />
    </g>
  </svg>
</template>

<style scoped>
.spark { display: block; width: 100%; }
</style>
