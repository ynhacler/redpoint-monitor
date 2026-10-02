<script setup lang="ts">
// 数值 + 单位（设计 41.3 Metric 的拆分形式）：单位缩小一档并用次要色，数字用等宽数字，便于纵向对齐。
// 传 text 时按最后一个空格拆分（“387 M”“785 K/s”）；也可直接传 v 与 u。
import { computed } from 'vue'

const props = defineProps<{
  /** 已格式化的完整文字，如 “387 M” */
  text?: string
  /** 数值部分 */
  v?: string | number
  /** 单位部分，如 “%”“M”“天” */
  u?: string
}>()

const parts = computed(() => {
  if (props.text != null) {
    const i = props.text.lastIndexOf(' ')
    return i > 0 ? { v: props.text.slice(0, i), u: props.text.slice(i + 1) } : { v: props.text, u: '' }
  }
  return { v: props.v ?? '—', u: props.u ?? '' }
})
</script>

<template>
  <span class="qty"><span class="v">{{ parts.v }}</span><span v-if="parts.u" class="u">{{ parts.u }}</span></span>
</template>

<style scoped>
.qty { font-variant-numeric: tabular-nums; white-space: nowrap; }
.u { font-size: .62em; color: var(--text-muted); margin-left: .18em; font-weight: var(--weight-regular); letter-spacing: 0; }
</style>
