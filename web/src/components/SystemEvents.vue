<script setup lang="ts">
// 系统事件（修订第 163 条）：Agent 升级结果、到期提醒、面板自身告警与自检，最新在前，保留 180 天。
// 与告警不同，这些事件只是一条记录（没有触发 / 恢复）；通知照常经渠道发送。查看者同样可以看（读取权限）。
import { onMounted, ref } from 'vue'
import { errorText, listSystemEvents, type SystemEvent } from '../api'
import { fmtDateTime } from '../format'
import EmptyState from './EmptyState.vue'

const kindNames: Record<SystemEvent['kind'], string> = { upgrade: '升级', reminder: '提醒', panel: '面板', change: '变更' }
const items = ref<SystemEvent[]>([])
const cursor = ref('')
const loading = ref(false)
const error = ref('')

async function load(more = false) {
  loading.value = true
  try {
    const p = await listSystemEvents(more ? cursor.value : undefined)
    items.value = more ? [...items.value, ...p.items] : p.items
    cursor.value = p.next_cursor
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '系统事件加载失败')
  } finally {
    loading.value = false
  }
}
onMounted(() => load())
</script>

<template>
  <p class="muted small intro">Agent 升级结果、节点 / 证书 / 云账户到期提醒、面板自身异常、节点配置变更；保留 180 天。</p>
  <p v-if="error" class="banner">{{ error }}</p>
  <EmptyState v-else-if="!loading && !items.length" text="还没有系统事件。" />
  <ul v-if="items.length" class="panel list">
    <li v-for="e in items" :key="e.id" :class="[e.severity, { ok: e.title.startsWith('✅') }]">
      <span class="kind">{{ kindNames[e.kind] }}</span>
      <div class="main">
        <span class="title">{{ e.title }}</span>
        <RouterLink v-if="e.server_id && e.server_name" :to="`/servers/${e.server_id}`" class="small">节点详情</RouterLink>
        <span v-if="e.message" class="msg muted small">{{ e.message }}</span>
      </div>
      <span class="when muted small num">{{ fmtDateTime(e.created_at) }}</span>
    </li>
  </ul>
  <div v-if="cursor" class="more"><button type="button" class="secondary" :disabled="loading" @click="load(true)">加载更多</button></div>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-2); }
.list { list-style: none; margin: 0; padding: 0; }
.list li { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; gap: var(--space-3); align-items: baseline; padding: var(--space-3) var(--space-4); }
.list li + li { border-top: 1px solid var(--border); }
.main { min-width: 0; display: flex; gap: var(--space-1) var(--space-2); flex-wrap: wrap; align-items: baseline; }
.title { font-weight: var(--weight-strong); }
.msg { flex-basis: 100%; white-space: pre-line; }
.kind { font-size: var(--font-xs); line-height: var(--line-xs); padding: 1px var(--space-2); border-radius: var(--radius-full);
  border: 1px solid currentColor; color: var(--text-muted); white-space: nowrap; }
.critical .kind { color: var(--bad); }
.warning .kind { color: var(--warn); }
.ok .kind { color: var(--ok); }
.more { display: flex; justify-content: center; margin-top: var(--space-4); }
@media (max-width: 600px) {
  .list li { grid-template-columns: auto minmax(0, 1fr); }
  .when { grid-column: 2; }
}
</style>
