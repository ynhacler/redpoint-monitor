<script setup lang="ts">
// 指标（设计 41.3 Metric）：标签（font-xs，muted）+ 数值（等宽数字）+ 单位（muted）。
defineProps<{
  /** 标签，如 “CPU” */
  label: string
  /** 已格式化的数值，如 “43%” “2.5 MB/s” */
  value: string
  /** 状态着色：超过阈值时 warn / bad */
  level?: 'warn' | 'bad'
  /** 悬停说明 */
  hint?: string
}>()
</script>

<template>
  <div class="metric" :title="hint">
    <div class="label">{{ label }}</div>
    <div class="value num" :class="level">{{ value }}</div>
  </div>
</template>

<style scoped>
.metric { min-width: 0; }
.label { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
/* 不自动换行；数值中的 \n 保留为换行（如网速的下行 / 上行分两行） */
.value { white-space: pre; }
.value.warn { color: var(--warn); }
.value.bad { color: var(--bad); }
</style>
