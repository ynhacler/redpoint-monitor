<script setup lang="ts">
// 线性图标（设计 41.4.3：线宽 1.5，只用于高频操作和状态）。
// 只内置用到的少数几个，路径取自 Lucide（ISC 许可）；不引入图标库依赖。
defineProps<{
  /** 图标名 */
  name: 'plus' | 'edit' | 'terminal' | 'arrow-left' | 'sun' | 'moon' | 'monitor' | 'search' | 'logout'
  /** 尺寸，px */
  size?: number
}>()

const paths: Record<string, string> = {
  plus: 'M5 12h14M12 5v14',
  edit: 'M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z',
  terminal: 'm4 17 6-6-6-6M12 19h8',
  'arrow-left': 'm12 19-7-7 7-7M19 12H5',
  sun: 'M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8ZM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M6.3 17.7l-1.4 1.4M19.1 4.9l-1.4 1.4',
  moon: 'M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z',
  monitor: 'M3 4h18v12H3zM8 20h8M12 16v4',
  search: 'm21 21-4.3-4.3M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16Z',
  logout: 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9',
}
</script>

<template>
  <svg :width="size ?? 16" :height="size ?? 16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path :d="paths[name]" />
  </svg>
</template>
