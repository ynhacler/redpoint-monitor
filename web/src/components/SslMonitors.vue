<script setup lang="ts">
// SSL 证书到期监控（设计 33.3）：面板每 12 小时连接一次并校验证书，30 / 14 / 7 / 3 / 1 天、当天与已过期各提醒一次。
import { onMounted, ref } from 'vue'
import { ApiError, checkSSLMonitor, createSSLMonitor, deleteSSLMonitor, errorText, listSSLMonitors, type SSLMonitor } from '../api'
import { DASH, fmtDate, fmtTime } from '../format'

const items = ref<SSLMonitor[]>([])
const error = ref('')
const host = ref('')
const note = ref('')
const fieldError = ref('')
const adding = ref(false)
const busy = ref<number | null>(null)

async function load() {
  try {
    items.value = await listSSLMonitors()
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '证书监控加载失败')
  }
}
onMounted(load)

async function add() {
  fieldError.value = ''
  adding.value = true
  try {
    await createSSLMonitor(host.value.trim(), note.value.trim())
    host.value = ''
    note.value = ''
    await load()
  } catch (e) {
    fieldError.value = e instanceof ApiError && e.details.length ? e.details.map((d) => d.message).join('；') : errorText(e)
  } finally {
    adding.value = false
  }
}

async function recheck(m: SSLMonitor) {
  busy.value = m.id
  try {
    const r = await checkSSLMonitor(m.id)
    items.value = items.value.map((x) => (x.id === r.id ? r : x))
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = null
  }
}

async function remove(m: SSLMonitor) {
  if (!confirm(`不再监控 ${m.host}${m.port !== 443 ? ':' + m.port : ''} 的证书？`)) return
  try {
    await deleteSSLMonitor(m.id)
    await load()
  } catch (e) {
    error.value = errorText(e)
  }
}

/** 剩余天数（按日历日）与配色 */
function remain(m: SSLMonitor): { text: string; tone: string } {
  if (!m.not_after) return { text: DASH, tone: 'muted' }
  const end = new Date(m.not_after * 1000)
  const today = new Date()
  const days = Math.round((new Date(end.getFullYear(), end.getMonth(), end.getDate()).getTime() -
    new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime()) / 86400000)
  if (days < 0) return { text: `已过期 ${-days} 天`, tone: 'bad' }
  return { text: days === 0 ? '今天到期' : `剩 ${days} 天`, tone: days <= 7 ? 'bad' : days <= 30 ? 'warn' : 'ok' }
}
</script>

<template>
  <div>
    <p class="muted small intro">
      面板每 12 小时连接一次并校验证书（证书链、域名、有效期），剩 30 / 14 / 7 / 3 / 1 天、当天与已过期各提醒一次，
      证书无效时提醒一次；提醒经“通知”中的渠道发送。
    </p>
    <p v-if="error" class="banner">{{ error }}</p>
    <form class="panel add" novalidate @submit.prevent="add">
      <input v-model="host" spellcheck="false" placeholder="example.com 或 example.com:8443（也可以粘贴网址）" aria-label="域名" />
      <input v-model="note" maxlength="100" placeholder="备注（可选）" aria-label="备注" />
      <button type="submit" :disabled="adding || !host.trim()">{{ adding ? '检查中…' : '添加' }}</button>
      <small v-if="fieldError" class="bad full">{{ fieldError }}</small>
    </form>

    <ul v-if="items.length" class="panel list">
      <li v-for="m in items" :key="m.id">
        <div class="main">
          <div>
            <strong>{{ m.host }}<span v-if="m.port !== 443" class="muted">:{{ m.port }}</span></strong>
            <span v-if="m.note" class="muted small"> · {{ m.note }}</span>
          </div>
          <div class="small">
            <span :class="remain(m).tone">{{ remain(m).text }}</span>
            <template v-if="m.not_after"> · {{ fmtDate(m.not_after) }} 到期 · {{ m.issuer }}</template>
          </div>
          <div v-if="m.last_error" class="small bad">{{ m.last_error }}</div>
          <div class="small muted">
            <template v-if="m.sans.length">{{ m.sans.slice(0, 4).join('、') }}{{ m.sans.length > 4 ? ` 等 ${m.sans.length} 个` : '' }} · </template>
            检查于 {{ fmtTime(m.checked_at) }}
          </div>
        </div>
        <div class="ops">
          <button type="button" class="secondary small" :disabled="busy === m.id" @click="recheck(m)">{{ busy === m.id ? '检查中…' : '重新检查' }}</button>
          <button type="button" class="text small" @click="remove(m)">删除</button>
        </div>
      </li>
    </ul>
    <p v-else class="muted small">还没有监控的证书。</p>
  </div>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-3); max-width: 80ch; }
.add { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-bottom: var(--space-4); }
.add input:first-child { flex: 2 1 240px; }
.add input:nth-child(2) { flex: 1 1 140px; }
.full { flex-basis: 100%; }
.list { list-style: none; margin: 0; padding: 0 var(--space-4); }
.list li { display: flex; gap: var(--space-3); justify-content: space-between; align-items: center; padding: var(--space-3) 0; flex-wrap: wrap; }
.list li + li { border-top: 1px solid var(--border); }
.main { min-width: 0; flex: 1 1 260px; display: flex; flex-direction: column; gap: 2px; overflow-wrap: anywhere; }
.ops { display: flex; gap: var(--space-2); align-items: center; }
.ok { color: var(--ok); }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
</style>
