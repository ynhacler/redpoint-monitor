<script setup lang="ts">
// 免打扰时段（设计 16.5）：严重告警默认仍通知；警告暂存，结束后汇总为一条；提示不发送。
import { computed, onMounted, reactive, ref } from 'vue'
import { ApiError, errorText, getQuietHours, saveQuietHours, type QuietHoursView } from '../api'


const view = ref<QuietHoursView | null>(null)
const form = reactive({ enabled: false, start: '23:00', end: '08:00', timezone: '', critical: 'notify' as 'notify' | 'summary' })
const errors = ref<Record<string, string>>({})
const saving = ref(false)
const saved = ref(false)
const error = ref('')

// 浏览器支持时列出全部 IANA 时区；空表示面板本地时区
const zones = computed<string[]>(() => {
  const f = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf
  return f ? f('timeZone') : ['Asia/Shanghai', 'Asia/Tokyo', 'Asia/Hong_Kong', 'Asia/Singapore', 'Europe/London', 'America/Los_Angeles', 'UTC']
})

function fill(v: QuietHoursView) {
  view.value = v
  Object.assign(form, { enabled: v.enabled, start: v.start, end: v.end, timezone: v.timezone, critical: v.critical })
}

onMounted(async () => {
  try {
    fill(await getQuietHours())
  } catch (e) {
    error.value = errorText(e, '加载失败')
  }
})

async function save() {
  saving.value = true
  saved.value = false
  errors.value = {}
  error.value = ''
  try {
    fill(await saveQuietHours({ ...form }))
    saved.value = true
  } catch (e) {
    if (e instanceof ApiError && e.details.length) errors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
    else error.value = errorText(e, '保存失败')
  } finally {
    saving.value = false
  }
}

const crossesMidnight = computed(() => form.start > form.end)
</script>

<template>
  <form class="panel quiet" novalidate @submit.prevent="save">
    <div class="head">
      <label class="check title"><input v-model="form.enabled" type="checkbox" />免打扰时段</label>
      <span v-if="view?.active" class="badge">🌙 免打扰中<template v-if="view.held"> · 已暂存 {{ view.held }} 条</template></span>
    </div>
    <p class="muted small">严重告警默认仍然通知；警告暂存，结束后汇总为一条发出；提示不发送。面板自检与测试通知不受影响。</p>
    <div class="fields" :class="{ off: !form.enabled }">
      <label>开始
        <input v-model="form.start" type="time" required />
        <small v-if="errors.start" class="err">{{ errors.start }}</small>
      </label>
      <label>结束
        <input v-model="form.end" type="time" required />
        <small v-if="errors.end" class="err">{{ errors.end }}</small>
        <small v-else-if="crossesMidnight" class="muted">跨午夜，至次日 {{ form.end }}</small>
      </label>
      <label>时区
        <select v-model="form.timezone">
          <option value="">面板本地时区<template v-if="view && !view.timezone">（{{ view.effective_timezone }}）</template></option>
          <option v-for="z in zones" :key="z" :value="z">{{ z }}</option>
        </select>
        <small v-if="errors.timezone" class="err">{{ errors.timezone }}</small>
      </label>
      <label>严重告警
        <select v-model="form.critical">
          <option value="notify">仍然立即通知</option>
          <option value="summary">也一起汇总</option>
        </select>
      </label>
    </div>
    <p v-if="error" class="err small">{{ error }}</p>
    <div class="actions">
      <button type="submit" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
      <span v-if="saved" class="ok small">已保存</span>
    </div>
  </form>
</template>

<style scoped>
.quiet { padding: var(--space-4); margin-bottom: var(--space-4); }
.head { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; }
.title { font-weight: var(--weight-strong); }
.check { display: inline-flex; align-items: center; gap: var(--space-2); }
.check input { width: auto; }
.badge { font-size: var(--font-xs); padding: 1px var(--space-2); border-radius: var(--radius-full);
  background: color-mix(in srgb, var(--accent) 12%, transparent); color: var(--accent); }
.quiet > p { margin: var(--space-2) 0 var(--space-3); }
.fields { display: grid; grid-template-columns: repeat(auto-fill, minmax(170px, 1fr)); gap: var(--space-3); }
.fields.off { opacity: .55; }
.fields label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.actions { display: flex; align-items: center; gap: var(--space-3); margin-top: var(--space-3); }
.err { color: var(--bad); }
.ok { color: var(--ok); }
</style>
