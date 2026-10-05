<script setup lang="ts">
// 健康摘要（设计 1.5.7）：面板把最近 24 小时 / 30 天的指标归纳成几句话，避免逐张图判断。
// 结论在面板计算（GET /servers/{id}/health），App 显示同样的文字；摘要变化慢，每分钟刷新一次。
import { onBeforeUnmount, ref, watch } from 'vue'
import { errorText, getHealth, type HealthSummary } from '../api'

const props = defineProps<{ serverId: number }>()
const health = ref<HealthSummary | null>(null)
const error = ref('')
let timer: ReturnType<typeof setTimeout> | undefined

async function load() {
  clearTimeout(timer)
  try {
    health.value = await getHealth(props.serverId)
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '健康摘要加载失败')
  }
  timer = setTimeout(load, 60_000)
}
watch(() => props.serverId, () => {
  health.value = null
  load()
}, { immediate: true })
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <section class="panel health">
    <div class="head">
      <h3>健康摘要</h3>
      <span v-if="health" class="status small" :class="health.level">{{ health.status }}</span>
    </div>
    <p v-if="error" class="small bad">{{ error }}</p>
    <dl v-if="health?.items.length" class="items">
      <template v-for="it in health.items" :key="it.key">
        <dt class="small"><span class="dot" :class="it.level" />{{ it.title }}</dt>
        <dd class="small">{{ it.text }}</dd>
      </template>
    </dl>
    <p v-else-if="health" class="small muted">还没有足够的历史数据。</p>
  </section>
</template>

<style scoped>
.health { padding: var(--space-4) var(--space-5); }
.head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); flex-wrap: wrap; margin-bottom: var(--space-3); }
.head h3 { margin: 0; font-size: var(--font-lg); }
.status { font-weight: var(--weight-strong); }
.status.ok { color: var(--ok); }
.status.warn { color: var(--warn); }
.status.bad { color: var(--bad); }
.status.muted { color: var(--text-muted); }
.items { display: grid; grid-template-columns: max-content 1fr; gap: var(--space-2) var(--space-4); margin: 0; }
.items dt { display: flex; align-items: center; gap: var(--space-2); color: var(--text-muted); }
.items dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
.dot { width: 8px; height: 8px; border-radius: var(--radius-full); background: var(--ok); flex: none; }
.dot.warn { background: var(--warn); }
.dot.bad { background: var(--bad); }
</style>
