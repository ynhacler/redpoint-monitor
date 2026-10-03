<script setup lang="ts">
// 节点的告警记录（设计 1.5.7、16.3）：进行中的在前，其后是最近已恢复的，显示触发时间与持续时长。
// 每分钟刷新一次；进行中的告警的当前值随节点列表每 3 秒更新（见节点卡片与横幅）。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { listAlerts, UnauthorizedError, type AlertEvent } from '../api'
import { fmtDateTime, fmtDuration } from '../format'

const props = defineProps<{
  /** 节点 ID */
  serverId: number
  /** 节点活动告警与维护状态的摘要；变化时立即刷新（告警触发 / 恢复、开始维护后不必等 1 分钟） */
  version?: string
}>()
const emit = defineEmits<{ unauthorized: [] }>()

const items = ref<AlertEvent[]>([])
const loaded = ref(false)
const error = ref('')
let timer: number | undefined

async function load() {
  try {
    items.value = (await listAlerts({ state: 'all', server_id: props.serverId, limit: 20 })).items
    error.value = ''
  } catch (e) {
    if (e instanceof UnauthorizedError) emit('unauthorized')
    else error.value = '告警记录加载失败'
  } finally {
    loaded.value = true
  }
}
watch(() => props.serverId, () => {
  load()
  if (timer) clearInterval(timer)
  timer = window.setInterval(load, 60_000)
}, { immediate: true })
watch(() => props.version, (v, old) => old !== undefined && load())
onBeforeUnmount(() => timer && clearInterval(timer))

// 进行中的在前，其余按触发时间倒序（接口已按 ID 倒序）
const sorted = computed(() => [...items.value].sort((a, b) => (a.state === b.state ? 0 : a.state === 'firing' ? -1 : 1)))
const severityName = { critical: '严重', warning: '警告', info: '提示' } as const
const duration = (e: AlertEvent) => fmtDuration((e.resolved_at || Date.now() / 1000) - e.fired_at)
</script>

<template>
  <div class="panel alerts">
    <div class="head">
      <h3>告警记录</h3>
      <span class="muted small">最近 20 条 · 保留 180 天</span>
    </div>
    <p v-if="error" class="muted small">{{ error }}</p>
    <p v-else-if="loaded && !items.length" class="muted small empty">还没有告警。</p>
    <ul v-else class="list">
      <li v-for="e in sorted" :key="e.id" :class="[e.severity, e.state]">
        <span class="sev">{{ severityName[e.severity] }}</span>
        <span class="msg">{{ e.message }}</span>
        <span class="when muted small num">
          {{ fmtDateTime(e.fired_at) }}
          <template v-if="e.state === 'firing'"> · <b class="ongoing">进行中 {{ duration(e) }}</b></template>
          <template v-else> · 持续 {{ duration(e) }} 后恢复</template>
        </span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.head { display: flex; justify-content: space-between; align-items: baseline; gap: var(--space-3); }
.empty { margin: var(--space-3) 0 0; }
.list { list-style: none; margin: var(--space-3) 0 0; padding: 0; }
.list li { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; gap: var(--space-3); align-items: baseline; padding: var(--space-2) 0; }
.list li + li { border-top: 1px solid var(--border); }
.sev { font-size: var(--font-xs); line-height: var(--line-xs); padding: 1px var(--space-2); border-radius: var(--radius-full);
  border: 1px solid currentColor; color: var(--text-muted); white-space: nowrap; }
.critical .sev { color: var(--bad); }
.warning .sev { color: var(--warn); }
.resolved .sev { opacity: .6; }
.msg { min-width: 0; overflow-wrap: anywhere; }
.resolved .msg { color: var(--text-muted); }
.ongoing { color: var(--bad); font-weight: var(--weight-strong); }
.warning .ongoing { color: var(--warn); }
.info .ongoing { color: var(--text); }
@media (max-width: 600px) {
  .list li { grid-template-columns: auto minmax(0, 1fr); }
  .when { grid-column: 2; }
}
</style>
