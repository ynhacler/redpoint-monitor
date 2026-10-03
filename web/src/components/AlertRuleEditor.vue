<script setup lang="ts">
// 告警规则编辑（设计 16.2）：修改现有规则，或为分组 / 节点新增覆盖。
// 输入时实时预览“按当前数据，此规则会对几台节点触发”（只比较阈值，不考虑持续时间）。
// 时间在界面上以分钟 / 小时输入，提交时换算为秒。
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import {
  ApiError, createAlertRule, previewAlertRule, updateAlertRule, UnauthorizedError,
  type AlertRule, type AlertRuleInput,
} from '../api'
import { ruleNames, severityNames, thresholdRange, thresholdUnit } from '../alertRules'

const props = defineProps<{
  /** 修改时为该规则；新增覆盖时为作为基础的全局规则 */
  rule: AlertRule
  /** 新增覆盖时的层级与对象；省略表示修改 rule 本身 */
  scope?: { type: 'group' | 'server'; id: string }
}>()
const emit = defineEmits<{ saved: [AlertRule]; cancel: []; unauthorized: [] }>()

const r = props.rule
const f = reactive({
  threshold: r.threshold, recover: r.recover_threshold,
  durMin: r.duration_s / 60, recDurMin: r.recover_duration_s / 60,
  severity: r.severity, repeatH: r.repeat_interval_s / 3600, enabled: r.enabled,
})
const unit = thresholdUnit(r.type)
const range = thresholdRange(r.type)
// 流量类规则在新周期开始时自然恢复，恢复阈值固定为触发阈值；离线的恢复指重新上报，不开放修改
const editRecover = !r.type.startsWith('traffic') && r.type !== 'offline'
const editDuration = r.type !== 'offline' && !r.type.startsWith('traffic')

function input(): AlertRuleInput {
  const n = (v: unknown) => Number(v)
  return {
    threshold: n(f.threshold),
    recover_threshold: editRecover ? n(f.recover) : r.type.startsWith('traffic') ? n(f.threshold) : r.recover_threshold,
    duration_s: editDuration ? Math.round(n(f.durMin) * 60) : r.duration_s,
    recover_duration_s: editRecover ? Math.round(n(f.recDurMin) * 60) : r.recover_duration_s,
    severity: f.severity, repeat_interval_s: Math.round(n(f.repeatH) * 3600), enabled: f.enabled,
  }
}

// 实时预览（输入停止 300ms 后请求）
const preview = ref<{ matching: number; total: number; names: string } | null>(null)
let timer: number | undefined
watch(f, () => {
  if (timer) clearTimeout(timer)
  timer = window.setTimeout(async () => {
    try {
      const p = await previewAlertRule(props.scope
        ? { ...input(), rule_key: r.rule_key, scope_type: props.scope.type, scope_id: props.scope.id }
        : { ...input(), id: r.id })
      preview.value = { matching: p.matching, total: p.total, names: p.items.slice(0, 5).map((x) => x.name).join('、') }
    } catch (e) {
      if (e instanceof UnauthorizedError) emit('unauthorized')
      preview.value = null
    }
  }, 300)
}, { deep: true, immediate: true })
onBeforeUnmount(() => timer && clearTimeout(timer))

const errors = ref<Record<string, string>>({})
const saving = ref(false)
async function save() {
  saving.value = true
  errors.value = {}
  try {
    const saved = props.scope
      ? await createAlertRule({ ...input(), rule_key: r.rule_key, scope_type: props.scope.type, scope_id: props.scope.id })
      : await updateAlertRule(r.id, input())
    emit('saved', saved)
  } catch (e) {
    if (e instanceof UnauthorizedError) emit('unauthorized')
    else if (e instanceof ApiError) {
      errors.value = e.details.length ? Object.fromEntries(e.details.map((d) => [d.field, d.message])) : { form: e.message }
    }
  } finally {
    saving.value = false
  }
}
const title = computed(() => ruleNames[r.rule_key] ?? r.rule_key)
</script>

<template>
  <form class="editor" novalidate @submit.prevent="save">
    <div class="fields">
      <label>触发阈值（{{ unit }}）
        <input v-model="f.threshold" type="number" :min="range.min" :max="range.max" :step="range.step" />
        <small v-if="errors.threshold" class="err">{{ errors.threshold }}</small>
      </label>
      <label v-if="editDuration">持续（分钟）
        <input v-model="f.durMin" type="number" min="0" max="1440" step="1" />
        <small v-if="errors.duration_s" class="err">{{ errors.duration_s }}</small>
      </label>
      <label v-if="editRecover">恢复阈值（{{ unit }}）
        <input v-model="f.recover" type="number" min="0" :max="range.max" :step="range.step" />
        <small v-if="errors.recover_threshold" class="err">{{ errors.recover_threshold }}</small>
        <small v-else class="muted">低于此值才恢复，避免在阈值附近反复触发</small>
      </label>
      <label v-if="editRecover">恢复持续（分钟）
        <input v-model="f.recDurMin" type="number" min="0" max="1440" step="1" />
      </label>
      <label>级别
        <select v-model="f.severity">
          <option v-for="(n, k) in severityNames" :key="k" :value="k">{{ n }}</option>
        </select>
      </label>
      <label>未恢复时重复提醒（小时）
        <input v-model="f.repeatH" type="number" min="0" max="168" step="0.5" />
        <small class="muted">0 表示不重复；通知发到“通知”标签中的渠道</small>
      </label>
      <label class="check"><input v-model="f.enabled" type="checkbox" />启用“{{ title }}”</label>
    </div>
    <p class="preview small" :class="{ hot: preview && preview.matching }">
      <template v-if="!f.enabled">规则关闭后不会触发。</template>
      <template v-else-if="preview">
        按当前数据，此规则会对 <b>{{ preview.matching }}</b> / {{ preview.total }} 台节点触发<template v-if="preview.names">：{{ preview.names }}</template>
        <span class="muted">（只比较阈值，不含持续时间）</span>
      </template>
    </p>
    <p v-if="errors.form" class="err small">{{ errors.form }}</p>
    <div class="actions">
      <button type="submit" :disabled="saving">{{ saving ? '保存中…' : scope ? '添加覆盖' : '保存' }}</button>
      <button type="button" class="secondary" @click="emit('cancel')">取消</button>
    </div>
  </form>
</template>

<style scoped>
.editor { padding: var(--space-3) var(--space-4); background: var(--surface-2); border-radius: var(--radius-sm); margin-top: var(--space-2); }
.fields { display: grid; grid-template-columns: repeat(auto-fill, minmax(170px, 1fr)); gap: var(--space-3); }
.fields label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.fields .check { flex-direction: row; align-items: center; gap: var(--space-2); grid-column: 1 / -1; }
.check input { width: auto; }
.err { color: var(--bad); }
.preview { margin: var(--space-3) 0 0; }
.preview.hot b { color: var(--warn); }
.actions { display: flex; gap: var(--space-2); margin-top: var(--space-3); }
</style>
