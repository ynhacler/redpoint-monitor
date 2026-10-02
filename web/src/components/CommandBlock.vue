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
    <!-- 每个词整体不换行，只在空格处折行：注册码、URL 中的连字符不会被拆开 -->
    <pre ref="pre"><template v-for="(w, i) in command.split(' ')" :key="i"><span class="word">{{ w }}</span>{{ ' ' }}</template></pre>
    <button type="button" class="secondary" @click="copy">{{ copied ? '已复制' : '复制' }}</button>
  </div>
</template>

<style scoped>
.cmd { position: relative; background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius-sm); }
pre {
  margin: 0; padding: var(--space-4) 84px var(--space-4) var(--space-4); /* 右侧 84px 留给“复制”按钮 */ overflow-x: auto; white-space: pre-wrap;
  font-family: var(--font-mono); font-size: var(--font-sm); line-height: var(--line-md);
}
.word { white-space: nowrap; }
button { position: absolute; top: var(--space-2); right: var(--space-2); padding: var(--space-1) var(--space-3); font-size: var(--font-sm); }
</style>
