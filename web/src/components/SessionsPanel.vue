<script setup lang="ts">
// 登录中的设备（设计 24.8、17.4）：当前账号的会话列表，可踢出其他浏览器。踢出记入登录日志。
import { onMounted, ref } from 'vue'
import { errorText, listSessions, revokeSession, type LoginSession } from '../api'
import { DASH, fmtDateTime, fmtTime, uaSummary } from '../format'
import { t } from '../i18n'
import Icon from './Icon.vue'
import SettingsCard from './SettingsCard.vue'

const sessions = ref<LoginSession[]>([])
const error = ref('')
const busy = ref<number | null>(null)

async function load() {
  try {
    sessions.value = await listSessions()
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t('登录会话加载失败'))
  }
}
onMounted(load)

async function kick(s: LoginSession) {
  if (!confirm(t('踢出 {device}（{ip}）？该浏览器需要重新登录。', { device: uaSummary(s.user_agent) || t('该设备'), ip: s.client_ip || t('未知 IP') }))) return
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
  <SettingsCard icon="smartphone" :title="t('登录中的设备')" :desc="t('发现不认识的设备时先踢出，再修改密码（修改密码会让其他设备全部退出）。')">
    <template #badge><span v-if="sessions.length" class="pill">{{ sessions.length }}</span></template>
    <p v-if="error" class="small bad">{{ error }}</p>
    <ul v-if="sessions.length" class="sc-list">
      <li v-for="s in sessions" :key="s.id">
        <span class="dev" aria-hidden="true"><Icon name="monitor" :size="16" /></span>
        <div class="who">
          <div class="who-line">
            <strong>{{ uaSummary(s.user_agent) || t('未知浏览器') }}</strong>
            <span v-if="s.current" class="pill on">{{ t('当前浏览器') }}</span>
          </div>
          <div class="small muted">
            {{ s.client_ip || DASH }} · {{ t('最近活动 {time}', { time: fmtTime(s.last_seen_at) }) }} · {{ t('登录于 {time}', { time: fmtDateTime(s.created_at) }) }}
          </div>
        </div>
        <button v-if="!s.current" type="button" class="secondary small" :disabled="busy === s.id" @click="kick(s)">
          {{ busy === s.id ? t('踢出中…') : t('踢出') }}
        </button>
      </li>
    </ul>
  </SettingsCard>
</template>

<style scoped>
.dev { flex: none; width: 32px; height: 32px; border-radius: 50%; display: grid; place-items: center; background: var(--surface-2); color: var(--text-muted); }
.who { flex: 1; min-width: 0; }
.who-line { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.bad { color: var(--bad); }
</style>
