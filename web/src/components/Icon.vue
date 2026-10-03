<script setup lang="ts">
// 线性图标（设计 41.4.3：线宽 1.5，只用于高频操作和状态）。
// 只内置用到的少数几个，路径取自 Lucide（ISC 许可）；不引入图标库依赖。
defineProps<{
  /** 图标名 */
  name: 'plus' | 'edit' | 'terminal' | 'arrow-left' | 'sun' | 'moon' | 'monitor' | 'search' | 'logout' | 'user'
    | 'thermometer' | 'power' | 'activity' | 'arrow-up' | 'arrow-down'
    | 'cpu' | 'memory' | 'network' | 'hard-drive' | 'plug'
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
  user: 'M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z',
  thermometer: 'M14 4v10.54a4 4 0 1 1-4 0V4a2 2 0 0 1 4 0Z',
  power: 'M12 2v10M18.4 6.6a9 9 0 1 1-12.77.04',
  activity: 'M3 3h18v18H3zM7 14l2.5-3 3 4 2.5-5 2 4',
  'arrow-up': 'M12 19V5M5 12l7-7 7 7',
  'arrow-down': 'M12 5v14M19 12l-7 7-7-7',
  cpu: 'M6 4h12a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2ZM9 9h6v6H9zM15 2v2M15 20v2M2 15h2M2 9h2M20 15h2M20 9h2M9 2v2M9 20v2',
  memory: 'M6 19v-3M10 19v-3M14 19v-3M18 19v-3M8 11V9M16 11V9M12 11V9M2 15h20M2 7a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v1.1a2 2 0 0 0 0 3.84V17a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-5.1a2 2 0 0 0 0-3.84Z',
  network: 'm3 16 4 4 4-4M7 20V4M21 8l-4-4-4 4M17 4v16',
  'hard-drive': 'M22 12H2M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11ZM6 16h.01M10 16h.01',
  plug: 'M12 22v-5M9 8V2M15 8V2M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8Z',
}
</script>

<template>
  <svg :width="size ?? 16" :height="size ?? 16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path :d="paths[name]" />
  </svg>
</template>
