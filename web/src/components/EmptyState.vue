<script setup lang="ts">
// 空状态（设计 41.3 EmptyState）：一句说明 + 一个主操作。
// 修订第 338 条（美化）：上方加一个装饰性的图标圆（默认为空盒子，可用 icon 指定 Icon 中的名称），背景浅渐变。
import Icon, { type IconName } from './Icon.vue'

withDefaults(defineProps<{
  /** 说明文字 */
  text: string
  /** 装饰图标（Icon 的名称）；不传时画一个空盒子 */
  icon?: IconName
}>(), { icon: undefined })
</script>

<template>
  <div class="empty">
    <span class="art" aria-hidden="true">
      <Icon v-if="icon" :name="icon" :size="26" />
      <svg v-else width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
        <path d="M3 9l2.5-5h13L21 9" /><path d="M3 9h5l1.5 2.5h5L16 9h5v9.5a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 18.5V9z" />
      </svg>
    </span>
    <p class="muted">{{ text }}</p>
    <slot />
  </div>
</template>

<style scoped>
.empty { display: flex; flex-direction: column; align-items: center; text-align: center; padding: var(--space-8) var(--space-4);
  border: 1px dashed var(--border); border-radius: var(--radius-md);
  background: radial-gradient(80% 120% at 50% 0%, color-mix(in srgb, var(--accent) 5%, var(--surface)), var(--surface) 70%); }
.art { display: grid; place-items: center; width: 64px; height: 64px; margin-bottom: var(--space-4); border-radius: 50%; color: var(--accent);
  background: color-mix(in srgb, var(--accent) 10%, transparent); box-shadow: 0 0 0 8px color-mix(in srgb, var(--accent) 5%, transparent); }
.empty p { margin: 0 0 var(--space-4); max-width: 460px; }
</style>
