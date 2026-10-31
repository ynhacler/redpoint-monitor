<script setup lang="ts">
// 变更记录（修订第 273 条，设计 4.9.5）：节点上与安全有关的配置的变化，最新在前，保留 180 天。
// 比较防火墙、账号与权限组、重要文件（修订第 274 条）；第一次看到时只记录现状，不算变化。只给管理员看（安全页）。
import { onMounted, ref, watch } from 'vue'
import { errorText, listServerChanges, type SystemEvent } from '../api'
import { fmtDateTime } from '../format'
import { t } from '../i18n'

const props = defineProps<{ serverId: number }>()
const items = ref<SystemEvent[]>([])
const cursor = ref('')
const loading = ref(false)
const error = ref('')

async function load(more = false) {
  loading.value = true
  try {
    const p = await listServerChanges(props.serverId, more ? cursor.value : undefined)
    items.value = more ? [...items.value, ...p.items] : p.items
    cursor.value = p.next_cursor
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t('变更记录加载失败'))
  } finally {
    loading.value = false
  }
}
onMounted(() => load())
watch(() => props.serverId, () => load())
</script>

<template>
  <section class="panel changes">
    <div class="head">
      <h3>{{ t('变更记录') }}</h3>
      <span class="muted small">{{ t('仅管理员可见') }}</span>
    </div>
    <p class="muted small intro">{{ t('防火墙（启用与停用、入站默认策略、规则增删）、可登录账号与 sudo 等管理员组、SSH / sudo / 定时任务等重要文件（只比较摘要，不上传内容）；有变化时记一条并按告警设置通知（通知中不含细节）。保留 180 天。') }}</p>
    <p v-if="error" class="banner">{{ error }}</p>
    <p v-else-if="!loading && !items.length" class="muted small">{{ t('还没有变更。') }}</p>
    <ul v-if="items.length" class="list">
      <li v-for="e in items" :key="e.id" :class="e.severity">
        <div class="row">
          <span class="title">{{ e.title }}</span>
          <span class="when muted small num">{{ fmtDateTime(e.created_at) }}</span>
        </div>
        <span v-if="e.message" class="msg small">{{ e.message }}</span>
      </li>
    </ul>
    <button v-if="cursor" type="button" class="secondary small more" :disabled="loading" @click="load(true)">{{ t('加载更多') }}</button>
  </section>
</template>

<style scoped>
.head { display: flex; align-items: baseline; gap: var(--space-2); }
.head h3 { margin: 0; }
.intro { margin: var(--space-1) 0 var(--space-3); }
.list { list-style: none; margin: 0; padding: 0; }
.list li { padding: var(--space-2) 0 var(--space-2) var(--space-3); border-left: 3px solid var(--border); }
.list li + li { margin-top: var(--space-2); }
.list li.warning { border-left-color: var(--warn); }
.list li.critical { border-left-color: var(--bad); }
.row { display: flex; justify-content: space-between; gap: var(--space-3); align-items: baseline; flex-wrap: wrap; }
.title { font-weight: var(--weight-strong); }
.msg { display: block; margin-top: 2px; white-space: pre-line; color: var(--text-muted); overflow-wrap: anywhere; }
.more { margin-top: var(--space-3); }
</style>
