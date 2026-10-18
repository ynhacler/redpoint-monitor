<script lang="ts">
import { ref } from 'vue'
// 各页面共用的缓存：启用的通知渠道数（null 表示还没读到）；1 分钟内切换页面不重复请求
const enabledCount = ref<number | null>(null)
let readAt = 0

/** 渠道变化后更新提醒（通知渠道页保存、删除、启停后调用） */
export function setNotifyChannels(n: number) {
  enabledCount.value = n
  readAt = Date.now()
}
</script>

<script setup lang="ts">
// 告警闭环提醒（修订第 173 条）：没有启用的通知渠道时，告警触发了也发不出去。在仪表板与告警页常驻提示，直到配置好渠道。
// 只给管理员（查看者不能管理渠道）。
import { onMounted } from 'vue'
import { listChannels } from '../api'
import { isViewer } from '../store'

onMounted(async () => {
  if (isViewer.value || Date.now() - readAt < 60_000) return
  try {
    setNotifyChannels((await listChannels()).items.filter((c) => c.enabled).length)
  } catch {
    /* 读取失败时不提示，避免误报 */
  }
})
</script>

<template>
  <p v-if="!isViewer && enabledCount === 0" class="banner warn notify-warning" role="alert">
    还没有启用的通知渠道：告警触发后只显示在面板里，不会通知任何人（已配对并开启推送的 App 仍会收到）。
    <RouterLink to="/alerts?tab=notify">添加通知渠道</RouterLink>
  </p>
</template>

<style scoped>
.notify-warning { margin-bottom: var(--space-3); }
</style>
