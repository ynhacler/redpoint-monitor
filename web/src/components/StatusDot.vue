<script setup lang="ts">
// 状态点（设计 41.3 StatusDot）：8px 圆点 + 文字。
// 【无障碍】状态必须同时有文字，不能只靠颜色区分（设计 41.2.1）。
import { computed } from 'vue'

const props = defineProps<{
  /** 节点状态 */
  status: 'online' | 'unknown' | 'offline' | 'pending' | 'maintenance'
  /** 为 true 时只显示圆点（文字放在 title 中，用于空间很小的位置） */
  dotOnly?: boolean
}>()

const labels = { online: '在线', unknown: '未知', offline: '离线', pending: '待安装', maintenance: '维护中' } as const
const label = computed(() => labels[props.status])
</script>

<template>
  <span class="status" :class="status" :title="label" :aria-label="label">
    <span class="dot" />
    <span v-if="!dotOnly" class="label">{{ label }}</span>
  </span>
</template>

<style scoped>
.status { display: inline-flex; align-items: center; gap: var(--space-1); font-size: var(--font-sm); line-height: var(--line-sm); }
.dot { width: 8px; height: 8px; border-radius: var(--radius-full); background: var(--muted-state); flex: none; }
.online .dot { background: var(--ok); }
.unknown .dot { background: var(--warn); }
.offline .dot { background: var(--bad); }
.pending .dot { background: transparent; border: 2px solid var(--muted-state); }
.maintenance .dot { background: var(--muted-state); }
.maintenance { color: var(--text-muted); }
.online .label { color: var(--ok); }
.unknown .label { color: var(--warn); }
.offline .label { color: var(--bad); }
.pending .label { color: var(--text-muted); }
</style>
