<script setup lang="ts">
// 命令展示块（设计 41.3 CommandBlock）：等宽字体展示安装命令，右上角“复制”。
import { ref } from 'vue'

const props = defineProps<{
  /** 要展示与复制的命令 */
  command: string
}>()

const copied = ref(false)
const pre = ref<HTMLElement | null>(null)

// 优先使用剪贴板 API（需要 HTTPS 或 localhost）；不可用时选中文本，让用户手动复制。
async function copy() {
  try {
    await navigator.clipboard.writeText(props.command)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  } catch {
    const sel = window.getSelection()
    if (pre.value && sel) {
      const range = document.createRange()
      range.selectNodeContents(pre.value)
      sel.removeAllRanges()
      sel.addRange(range)
    }
  }
}
</script>

<template>
  <div class="cmd">
    <pre ref="pre">{{ command }}</pre>
    <button type="button" class="secondary" @click="copy">{{ copied ? '已复制' : '复制' }}</button>
  </div>
</template>

<style scoped>
.cmd { position: relative; background: var(--code-bg); border: 1px solid var(--border); border-radius: 8px; }
pre {
  margin: 0; padding: 14px 84px 14px 14px; overflow-x: auto; white-space: pre-wrap; overflow-wrap: anywhere; /* 优先在空格处换行，不把注册码拆开 */
  font: 13px/1.6 ui-monospace, SFMono-Regular, Menlo, monospace;
}
button { position: absolute; top: 8px; right: 8px; padding: 4px 12px; font-size: 12px; }
</style>
