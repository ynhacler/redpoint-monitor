<script setup lang="ts">
// 线性图标（设计 41.4.3：Lucide，线宽 1.5，只用于高频操作和状态）。
// 按名称映射到 @lucide/vue 的组件：只导入用到的图标，打包时其余图标被摇树去掉。
import {
  Activity, AlignLeft, ArrowDown, ArrowDownUp, ArrowLeft, ArrowUp, ChartLine, Clock, Cpu, HardDrive,
  Layers, LogOut, MemoryStick, Monitor, Moon, Pencil, Plug, Plus, Power, Radio, Search, SquareActivity,
  Sun, Terminal, Thermometer, User,
} from '@lucide/vue'

const icons = {
  plus: Plus, edit: Pencil, terminal: Terminal, 'arrow-left': ArrowLeft, sun: Sun, moon: Moon,
  monitor: Monitor, search: Search, logout: LogOut, user: User, thermometer: Thermometer, power: Power,
  activity: SquareActivity, pulse: Activity, 'arrow-up': ArrowUp, 'arrow-down': ArrowDown, cpu: Cpu,
  memory: MemoryStick, network: ArrowDownUp, 'hard-drive': HardDrive, plug: Plug, layers: Layers,
  radio: Radio, list: AlignLeft, chart: ChartLine, clock: Clock,
}

export type IconName = keyof typeof icons

defineProps<{
  /** 图标名 */
  name: IconName
  /** 尺寸，px */
  size?: number
}>()
</script>

<template>
  <component :is="icons[name]" :size="size ?? 16" :stroke-width="1.5" aria-hidden="true" />
</template>
