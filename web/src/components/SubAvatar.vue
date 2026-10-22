<script setup lang="ts">
// 订阅图标（设计 47.2）：上传了图标的显示上传的图片；有标志的服务显示品牌色方块上的单色标志（brandIcons.ts，取自 Simple Icons），
// 其他显示色块加首字母。品牌色很浅时标志用深色，避免看不清。
import { computed } from 'vue'
import { brandIcons } from '../brandIcons'
import { avatarOf } from '../subscriptions'

const props = withDefaults(defineProps<{
  /** 目录键（可为空） */
  brand?: string
  /** 名称：没有标志时取首字母 */
  name: string
  /** 边长，像素 */
  size?: number
  /** 上传的图标地址（优先显示） */
  src?: string
}>(), { brand: '', size: 40, src: '' })

const icon = computed(() => (props.brand ? brandIcons[props.brand] : undefined))
const fallback = computed(() => avatarOf({ brand: props.brand, name: props.name }))
const bg = computed(() => (icon.value ? `#${icon.value.hex}` : fallback.value.color))
// 相对亮度高于 0.6 时（如白色、浅黄的品牌色）用深色标志
const light = computed(() => {
  const h = bg.value.replace('#', '')
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16) / 255)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.6
})
</script>

<template>
  <img v-if="src" class="sub-avatar img" :src="src" alt="" :width="size" :height="size" :style="{ borderRadius: `${Math.round(size / 4)}px` }" />
  <span v-else class="sub-avatar" :class="{ light }" :style="{ width: `${size}px`, height: `${size}px`, background: bg, borderRadius: `${Math.round(size / 4)}px` }">
    <svg v-if="icon" viewBox="0 0 24 24" :width="Math.round(size * 0.56)" :height="Math.round(size * 0.56)" aria-hidden="true">
      <path :d="icon.path" />
    </svg>
    <span v-else :style="{ fontSize: `${Math.round(size * 0.45)}px` }">{{ fallback.letter }}</span>
  </span>
</template>

<style scoped>
.sub-avatar { display: inline-grid; place-items: center; flex: none; color: var(--on-accent); font-weight: var(--weight-strong);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--text) 8%, transparent); }
.sub-avatar svg { fill: currentColor; }
.sub-avatar.light { color: var(--text); }
.sub-avatar.img { display: block; object-fit: cover; background: var(--surface-2); }
</style>
