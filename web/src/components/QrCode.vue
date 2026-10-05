<script setup lang="ts">
// 二维码（App 配对，设计 8.4.4、12.4）：在浏览器本地生成，内容不发送到任何第三方。
// 两种主题都是黑色模块 + 白色背景与 4 格静区，深色模式下也能被扫码识别。
import qrcode from 'qrcode-generator'
import { computed } from 'vue'

const props = defineProps<{ value: string; label?: string }>()

const qr = computed(() => {
  const q = qrcode(0, 'M') // 自动选择版本；M 级纠错（约 15%），屏幕显示足够
  q.addData(props.value, 'Byte')
  q.make()
  const n = q.getModuleCount()
  let d = ''
  for (let r = 0; r < n; r++) {
    for (let c = 0; c < n; c++) {
      if (q.isDark(r, c)) d += `M${c + 4} ${r + 4}h1v1h-1z`
    }
  }
  return { size: n + 8, d }
})
</script>

<template>
  <svg class="qr" :viewBox="`0 0 ${qr.size} ${qr.size}`" role="img" :aria-label="label ?? '二维码'" shape-rendering="crispEdges">
    <rect :width="qr.size" :height="qr.size" class="bg" />
    <path :d="qr.d" class="fg" />
  </svg>
</template>

<style scoped>
.qr { display: block; width: 100%; max-width: 240px; height: auto; border-radius: var(--radius-sm); }
.bg { fill: var(--qr-light); }
.fg { fill: var(--qr-dark); }
</style>
