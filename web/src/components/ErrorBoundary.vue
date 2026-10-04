<script setup lang="ts">
// 全局错误边界（设计 43.6）：页面渲染出错时显示占位，不让整个页面白屏；不向用户展示原始错误与堆栈。
// 切换页面后自动恢复。
import { onErrorCaptured, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import EmptyState from './EmptyState.vue'

const failed = ref(false)
const route = useRoute()
watch(() => route.fullPath, () => (failed.value = false))

onErrorCaptured((err) => {
  console.error(err) // 只在浏览器控制台，便于排查
  failed.value = true
  return false
})

function reload() {
  location.reload()
}
</script>

<template>
  <div v-if="failed" class="page">
    <EmptyState text="页面出错了，请刷新重试。若反复出现，请把浏览器控制台中的错误附在问题反馈里。">
      <button type="button" @click="reload">刷新页面</button>
    </EmptyState>
  </div>
  <slot v-else />
</template>
