<script setup lang="ts">
// 订阅推送（修订第 316 条）：“推送”页的“订阅”标签。订阅的扣费、结束、预算提醒单独开关，不受告警推送开关影响；
// 可选发送到哪些通知渠道（不选为全部启用的渠道）与 App 推送；新增订阅时“提醒”的默认天数。
// 打开推送、打开 App 推送或改变渠道时面板要求重新输入密码（withReauth）。
import { computed, onMounted, reactive, ref } from 'vue'
import { ApiError, errorText, getSubPush, listChannels, putSubPush, type NotifyChannel, type SubPush } from '../api'
import { t } from '../i18n'
import { ReauthCancelled, withReauth } from '../reauth'
import { remindOptions } from '../subscriptions'
import SaveState, { saveStatus } from './SaveState.vue'

const form = reactive<SubPush>({ enabled: false, charge: true, end: true, budget: true, channel_ids: [], app: true, default_remind_days: 1 })
const channels = ref<NotifyChannel[]>([])
const snap = ref('')
const saving = ref(false)
const saved = ref(false)
const error = ref('')
const dirty = computed(() => !!snap.value && JSON.stringify(form) !== snap.value)
// 不选渠道 = 全部启用的渠道
const allChannels = computed({
  get: () => !form.channel_ids.length,
  set: (v: boolean) => (form.channel_ids = v ? [] : channels.value.filter((c) => c.enabled).map((c) => c.id)),
})

function fill(v: SubPush) {
  Object.assign(form, { ...v, channel_ids: [...v.channel_ids].sort((a, b) => a - b) })
  snap.value = JSON.stringify(form)
}

onMounted(async () => {
  try {
    const [p, cs] = await Promise.all([getSubPush(), listChannels()])
    channels.value = cs.items
    fill(p)
  } catch (e) {
    error.value = errorText(e, t('加载失败'))
  }
})

function toggleChannel(id: number, on: boolean) {
  const ids = new Set(form.channel_ids)
  if (on) ids.add(id)
  else ids.delete(id)
  form.channel_ids = [...ids].sort((a, b) => a - b)
}

async function save() {
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    fill(await withReauth(() => putSubPush({ ...form, channel_ids: [...form.channel_ids] })))
    saved.value = true
  } catch (e) {
    if (e instanceof ReauthCancelled) return
    error.value = e instanceof ApiError && e.details.length ? e.details[0].message : errorText(e, t('保存失败'))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <form class="panel subpush" novalidate @submit.prevent="save">
    <label class="check title"><input v-model="form.enabled" type="checkbox" role="switch" />{{ t('推送订阅提醒') }}</label>
    <p class="muted small">{{ t('订阅提醒与服务器告警分开设置：告警推送关闭时，这里打开也会照常推送。免打扰时段内先不发，结束后再发。') }}</p>

    <fieldset :class="{ off: !form.enabled }">
      <legend class="small muted">{{ t('提醒种类') }}</legend>
      <label class="check"><input v-model="form.charge" type="checkbox" />{{ t('扣费前提醒') }}<small class="muted">{{ t('按每个订阅的“提醒”天数') }}</small></label>
      <label class="check"><input v-model="form.end" type="checkbox" />{{ t('结束提醒') }}<small class="muted">{{ t('填了结束日期的，结束前 7、3、1 天与当天') }}</small></label>
      <label class="check"><input v-model="form.budget" type="checkbox" />{{ t('超出预算提醒') }}<small class="muted">{{ t('本月预计扣费超过每月预算时，每月一次') }}</small></label>
    </fieldset>

    <fieldset :class="{ off: !form.enabled }">
      <legend class="small muted">{{ t('发送到') }}</legend>
      <label class="check"><input v-model="allChannels" type="checkbox" />{{ t('全部启用的通知渠道') }}</label>
      <div v-if="!allChannels" class="channels">
        <label v-for="c in channels" :key="c.id" class="check" :class="{ disabled: !c.enabled }">
          <input type="checkbox" :checked="form.channel_ids.includes(c.id)" @change="toggleChannel(c.id, ($event.target as HTMLInputElement).checked)" />
          {{ c.name }}<small class="muted">{{ c.enabled ? c.type : t('{v}，已停用', { v: c.type }) }}</small>
        </label>
      </div>
      <p v-if="!channels.length" class="small muted">{{ t('还没有通知渠道，请先在“通知”标签添加。') }}</p>
      <label class="check"><input v-model="form.app" type="checkbox" />{{ t('App 推送') }}<small class="muted">{{ t('发到已配对并打开推送的手机') }}</small></label>
    </fieldset>

    <label class="days">{{ t('新增订阅时默认提醒') }}
      <select v-model.number="form.default_remind_days">
        <option v-for="o in remindOptions" :key="o.v" :value="o.v">{{ t(o.t) }}</option>
      </select>
    </label>

    <div class="actions">
      <button type="submit" :disabled="saving || !dirty" :title="!dirty ? t('没有修改') : undefined">{{ saving ? t('保存中…') : t('保存') }}</button>
      <SaveState :state="saveStatus({ saving, error, dirty, saved })" :error="error || undefined" />
    </div>
  </form>
</template>

<style scoped>
.subpush { padding: var(--space-4); display: flex; flex-direction: column; gap: var(--space-3); }
.subpush > p { margin: 0; }
.title { font-weight: var(--weight-strong); }
.check { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; min-height: 32px; }
.check input { width: auto; }
.check small { flex-basis: 100%; margin-left: calc(var(--space-2) + 16px); margin-top: -4px; }
.check.disabled { color: var(--text-muted); }
fieldset { border: 0; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-1); }
fieldset.off { opacity: .55; }
legend { padding: 0; margin-bottom: var(--space-1); }
.channels { display: flex; flex-direction: column; gap: var(--space-1); padding-left: var(--space-4); }
.days { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); max-width: 220px; }
.actions { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; }
</style>
