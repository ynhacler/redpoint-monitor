<script setup lang="ts">
// 登录记录（修订第 276 条，设计 4.9.5）：Agent 从 /var/log/wtmp 读到的登录，最新在前，保留 180 天。
// “新地址”为当时该节点第一次出现的来源地址（会记一条变更并按“变更通知”通知）。只给管理员看（安全页）。
import { onMounted, ref, watch } from 'vue'
import { errorText, listServerLogins, type LoginRecord } from '../api'
import { fmtDateTime } from '../format'
import { t } from '../i18n'

const props = defineProps<{ serverId: number }>()
const items = ref<LoginRecord[]>([])
const cursor = ref('')
const loading = ref(false)
const error = ref('')

async function load(more = false) {
  loading.value = true
  try {
    const p = await listServerLogins(props.serverId, more ? cursor.value : undefined)
    items.value = more ? [...items.value, ...p.items] : p.items
    cursor.value = p.next_cursor
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t('登录记录加载失败'))
  } finally {
    loading.value = false
  }
}
onMounted(() => load())
watch(() => props.serverId, () => load())
</script>

<template>
  <section class="panel logins">
    <div class="head">
      <h3>{{ t('登录记录') }}</h3>
      <span class="muted small">{{ t('仅管理员可见') }}</span>
    </div>
    <p class="muted small intro">{{ t('来自 /var/log/wtmp 的登录（SSH、控制台），保留 180 天；来自新地址的登录会记一条变更。不含失败的登录；不写 wtmp 的系统（如 Debian 13）没有记录。') }}</p>
    <p v-if="error" class="banner">{{ error }}</p>
    <p v-else-if="!loading && !items.length" class="muted small">{{ t('还没有登录记录。') }}</p>
    <ul v-if="items.length" class="list">
      <li v-for="l in items" :key="l.id">
        <span class="who">
          <span class="user">{{ l.user }}</span>
          <span class="host num">{{ l.host || t('本地控制台') }}</span>
          <span v-if="l.new_host" class="tag">{{ t('首次出现') }}</span>
        </span>
        <span class="meta muted small num">{{ l.line }} · {{ fmtDateTime(l.at) }}</span>
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
.list li { display: flex; justify-content: space-between; align-items: baseline; gap: var(--space-2) var(--space-3); flex-wrap: wrap;
  padding: var(--space-2) 0; border-top: 1px solid var(--border); }
.list li:first-child { border-top: 0; }
.who { display: inline-flex; align-items: baseline; gap: var(--space-2); flex-wrap: wrap; min-width: 0; }
.user { font-weight: var(--weight-strong); }
.host { overflow-wrap: anywhere; }
.tag { font-size: var(--font-xs); line-height: var(--line-xs); padding: 1px var(--space-2); border-radius: var(--radius-full);
  color: var(--warn); background: color-mix(in srgb, var(--warn) 12%, transparent); }
.more { margin-top: var(--space-3); }
</style>
