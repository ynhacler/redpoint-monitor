<script setup lang="ts">
// 用量条（设计 41.3 UsageBar）：6px；≥80% warn，≥95% bad。
import { computed } from 'vue'

const props = defineProps<{
  /** 0～100；超过 100 按 100 绘制 */
  pct: number
}>()
const width = computed(() => `${Math.min(100, Math.max(0, props.pct))}%`)
const level = computed(() => (props.pct >= 95 ? 'bad' : props.pct >= 80 ? 'warn' : ''))
</script>

<template>
  <div class="bar" role="progressbar" :aria-valuenow="Math.round(pct)" aria-valuemin="0" aria-valuemax="100">
    <div class="fill" :class="level" :style="{ width }" />
  </div>
</template>

<style scoped>
.bar { height: 6px; background: var(--border); border-radius: var(--radius-full); overflow: hidden; }
.fill { height: 100%; background: var(--ok); border-radius: var(--radius-full); transition: width .3s; }
.fill.warn { background: var(--warn); }
.fill.bad { background: var(--bad); }
</style>
