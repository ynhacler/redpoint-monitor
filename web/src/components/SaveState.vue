<script setup lang="ts">
// 保存状态（修订第 229 条）：设置表单在保存按钮旁说明当前状态——没有修改、有未保存的修改、保存中、已保存（时间）、保存失败（原因）。
// 按钮只在有修改时可点；不可点时由调用方用 saveTitle() 作为提示说明原因。读屏通过 role="status" 播报变化。
import { computed } from 'vue'
import { t } from '../i18n'

/** 保存状态 */
export type SaveStatus = 'clean' | 'dirty' | 'saving' | 'saved' | 'error'

const props = defineProps<{
  /** 状态 */
  state: SaveStatus
  /** 失败原因（state 为 error 时） */
  error?: string
  /** 保存成功的时间（毫秒），state 为 saved 时显示 */
  savedAt?: number
}>()

const text = computed(() => {
  switch (props.state) {
    case 'dirty': return t('有未保存的修改')
    case 'saving': return t('保存中…')
    case 'saved': return props.savedAt
      ? t('已保存 {time}', { time: new Date(props.savedAt).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) })
      : t('已保存')
    case 'error': return props.error ? t('保存失败：{e}', { e: props.error }) : t('保存失败')
    default: return t('没有修改')
  }
})
</script>

<script lang="ts">
/** 由各项标志得出保存状态：保存中 > 失败 > 有修改 > 刚保存 > 没有修改 */
export function saveStatus(o: { saving: boolean; error?: string; dirty: boolean; saved: boolean }): 'clean' | 'dirty' | 'saving' | 'saved' | 'error' {
  if (o.saving) return 'saving'
  if (o.error) return 'error'
  if (o.dirty) return 'dirty'
  return o.saved ? 'saved' : 'clean'
}
</script>

<template>
  <span class="save-state small" :class="state" role="status" aria-live="polite">{{ text }}</span>
</template>

<style scoped>
.save-state { color: var(--text-muted); }
.save-state.dirty { color: var(--warn); }
.save-state.saved { color: var(--ok); }
.save-state.error { color: var(--bad); }
</style>
