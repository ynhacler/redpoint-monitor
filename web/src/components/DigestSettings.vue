<script setup lang="ts">
// 定期报告（修订第 277 条，设计 16.8）：每周一或每月 1 日把概况发到全部已启用的通知渠道；下方是按当前时间生成的预览。
import { computed, onMounted, reactive, ref } from 'vue'
import { ApiError, errorText, getDigest, saveDigest, sendDigest, type DigestView } from '../api'
import { t } from '../i18n'
import SaveState, { saveStatus } from './SaveState.vue'

const view = ref<DigestView | null>(null)
const form = reactive({ enabled: false, period: 'weekly' as 'weekly' | 'monthly', time: '09:00', timezone: '' })
const errors = ref<Record<string, string>>({})
const snap = ref('')
const saving = ref(false)
const saved = ref(false)
const sending = ref(false)
const sent = ref(false)
const error = ref('')
const dirty = computed(() => !!snap.value && JSON.stringify(form) !== snap.value)
const fieldError = computed(() => (Object.keys(errors.value).length ? t('请检查标出的项') : ''))

const zones = computed<string[]>(() => {
  const f = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf
  return f ? f('timeZone') : ['Asia/Shanghai', 'Asia/Tokyo', 'Asia/Hong_Kong', 'Asia/Singapore', 'Europe/London', 'America/Los_Angeles', 'UTC']
})

function fill(v: DigestView) {
  view.value = v
  Object.assign(form, { enabled: v.enabled, period: v.period, time: v.time, timezone: v.timezone })
  snap.value = JSON.stringify(form)
}

onMounted(async () => {
  try {
    fill(await getDigest())
  } catch (e) {
    error.value = errorText(e, t('加载失败'))
  }
})

async function save() {
  saving.value = true
  saved.value = false
  sent.value = false
  errors.value = {}
  error.value = ''
  try {
    fill(await saveDigest({ ...form }))
    saved.value = true
  } catch (e) {
    if (e instanceof ApiError && e.details.length) errors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
    else error.value = errorText(e, t('保存失败'))
  } finally {
    saving.value = false
  }
}

async function send() {
  sending.value = true
  sent.value = false
  error.value = ''
  try {
    await sendDigest()
    sent.value = true
  } catch (e) {
    error.value = errorText(e, t('发送失败'))
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <form class="panel digest" novalidate @submit.prevent="save">
    <label class="check title"><input v-model="form.enabled" type="checkbox" />{{ t('定期报告') }}</label>
    <p class="muted small">{{ t('把节点、告警、离线、流量、到期、订阅扣费与云费用的概况定期发到全部已启用的通知渠道（不受渠道最低级别与免打扰限制）。') }}</p>
    <div class="fields" :class="{ off: !form.enabled }">
      <label>{{ t('频率') }}
        <select v-model="form.period">
          <option value="weekly">{{ t('每周一（前 7 天）') }}</option>
          <option value="monthly">{{ t('每月 1 日（上个月）') }}</option>
        </select>
      </label>
      <label>{{ t('发送时间') }}
        <input v-model="form.time" type="time" required />
        <small v-if="errors.time" class="err">{{ errors.time }}</small>
      </label>
      <label>{{ t('时区') }}
        <select v-model="form.timezone">
          <option value="">{{ t('面板本地时区') }}<template v-if="view && !view.timezone">（{{ view.effective_timezone }}）</template></option>
          <option v-for="z in zones" :key="z" :value="z">{{ z }}</option>
        </select>
        <small v-if="errors.timezone" class="err">{{ errors.timezone }}</small>
      </label>
    </div>
    <details v-if="view?.preview" class="preview">
      <summary class="small">{{ t('预览（按现在的时间生成）') }}</summary>
      <pre>{{ view.preview }}</pre>
    </details>
    <p v-if="error" class="err small">{{ error }}</p>
    <div class="actions">
      <button type="submit" :disabled="saving || !dirty" :title="!dirty ? t('没有修改') : undefined">{{ saving ? t('保存中…') : t('保存') }}</button>
      <button type="button" class="secondary" :disabled="sending || dirty" :title="dirty ? t('先保存修改') : undefined" @click="send">
        {{ sending ? t('发送中…') : t('立即发送一次') }}
      </button>
      <SaveState :state="saveStatus({ saving, error: error || fieldError, dirty, saved })" :error="(error || fieldError) || undefined" />
      <span v-if="sent" class="ok small">{{ t('已发送，请到渠道中查看') }}</span>
    </div>
  </form>
</template>

<style scoped>
.digest { padding: var(--space-4); margin-bottom: var(--space-4); }
.title { font-weight: var(--weight-strong); }
.check { display: inline-flex; align-items: center; gap: var(--space-2); }
.check input { width: auto; }
.digest > p { margin: var(--space-2) 0 var(--space-3); }
.fields { display: grid; grid-template-columns: repeat(auto-fill, minmax(170px, 1fr)); gap: var(--space-3); }
.fields.off { opacity: .55; }
.fields label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.preview { margin-top: var(--space-3); }
.preview summary { cursor: pointer; color: var(--text-muted); min-height: 32px; display: flex; align-items: center; }
.preview pre { margin: var(--space-2) 0 0; padding: var(--space-3); border-radius: var(--radius-sm); background: var(--surface-2);
  font: inherit; font-size: var(--font-sm); white-space: pre-wrap; overflow-wrap: anywhere; }
.actions { display: flex; align-items: center; gap: var(--space-3); margin-top: var(--space-3); flex-wrap: wrap; }
.err { color: var(--bad); }
.ok { color: var(--ok); }
</style>
