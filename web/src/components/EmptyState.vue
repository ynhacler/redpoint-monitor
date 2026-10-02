<script setup lang="ts">
// 空状态（设计 41.3 EmptyState）：一句说明 + 一个主操作。
defineProps<{
  /** 说明文字 */
  text: string
}>()
</script>

<template>
  <div class="empty">
    <p class="muted">{{ text }}</p>
    <slot />
  </div>
</template>

<style scoped>
.empty { text-align: center; padding: var(--space-8) var(--space-4); border: 1px dashed var(--border); border-radius: var(--radius-md); }
.empty p { margin: 0 0 var(--space-4); }
</style>
