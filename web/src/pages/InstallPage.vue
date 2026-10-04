<script setup lang="ts">
// 安装命令页（设计 27.3.4）。刚新建的节点带着完整注册码进入；从其他地方进入时只有脱敏提示，需重新生成。
import { useRouter } from 'vue-router'
import Icon from '../components/Icon.vue'
import InstallCommand from '../components/InstallCommand.vue'
import { refresh, takeCreated } from '../store'

const props = defineProps<{
  /** 节点 ID */
  id: string
}>()
const router = useRouter()
const initial = takeCreated(Number(props.id))

function back() {
  refresh()
  router.push(`/servers/${props.id}`)
}
</script>

<template>
  <main class="page">
    <RouterLink :to="`/servers/${id}`" class="back muted small"><Icon name="arrow-left" :size="14" />节点详情</RouterLink>
    <InstallCommand :key="id" :server-id="Number(id)" :initial="initial" @back="back" />
  </main>
</template>

<style scoped>
.back { display: inline-flex; align-items: center; gap: var(--space-1); margin-bottom: var(--space-3); }
</style>
