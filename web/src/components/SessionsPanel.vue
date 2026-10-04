<script setup lang="ts">
// 登录中的设备（设计 24.8、17.4）：当前账号的会话列表，可踢出其他浏览器。踢出记入登录日志。
import { onMounted, ref } from 'vue'
import { errorText, listSessions, revokeSession, type LoginSession } from '../api'
import { DASH, fmtDateTime, fmtTime, uaSummary } from '../format'

const sessions = ref<LoginSession[]>([])
const error = ref('')
const busy = ref<number | null>(null)

async function load() {
  try {
    sessions.value = await listSessions()
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '登录会话加载失败')
  }
}
onMounted(load)

async function kick(s: LoginSession) {
  if (!confirm(`踢出 ${uaSummary(s.user_agent) || '该设备'}（${s.client_ip || '未知 IP'}）？该浏览器需要重新登录。`)) return
  busy.value = s.id
  try {
    await revokeSession(s.id)
    await load()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = null
  }
}
</script>

<template>
  <section class="panel sessions">
    <h2>登录中的设备</h2>
    <p class="small muted">发现不认识的设备时先踢出，再修改密码（修改密码会让其他设备全部退出）。</p>
    <p v-if="error" class="small bad">{{ error }}</p>
    <ul class="list">
      <li v-for="s in sessions" :key="s.id">
        <div class="who">
          <strong>{{ uaSummary(s.user_agent) || '未知浏览器' }}</strong>
          <span v-if="s.current" class="tag">当前浏览器</span>
          <div class="small muted">
            {{ s.client_ip || DASH }} · 最近活动 {{ fmtTime(s.last_seen_at) }} · 登录于 {{ fmtDateTime(s.created_at) }}
          </div>
        </div>
        <button v-if="!s.current" type="button" class="secondary small" :disabled="busy === s.id" @click="kick(s)">
          {{ busy === s.id ? '踢出中…' : '踢出' }}
        </button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.sessions { max-width: 420px; margin: var(--space-4) auto 0; }
.sessions h2 { margin: 0 0 var(--space-1); font-size: var(--font-lg); }
.sessions p { margin: 0; }
.list { list-style: none; margin: var(--space-3) 0 0; padding: 0; }
.list li { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); padding: var(--space-3) 0; }
.list li + li { border-top: 1px solid var(--border); }
.who { min-width: 0; }
.tag { margin-left: var(--space-2); font-size: var(--font-xs); color: var(--accent); border: 1px solid var(--accent);
  border-radius: var(--radius-sm); padding: 0 var(--space-1); }
</style>
