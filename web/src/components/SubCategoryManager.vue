<script setup lang="ts">
// 自定义类别（修订第 314 条，参考 Wallos）：添加、改名、换颜色、删除。颜色从内置类别的颜色中选（design/tokens.json 的 cat-*）。
// 删除时用到它的订阅改为“其他”（面板处理），删除前确认。保存后通知父组件重新读取。
import { ref } from 'vue'
import { createSubCategory, deleteSubCategory, errorText, updateSubCategory } from '../api'
import { t } from '../i18n'
import { builtinCategories, categoryColors, categoryNames, type CustomCategory } from '../subscriptions'

const props = defineProps<{
  /** 现有的自定义类别 */
  list: CustomCategory[]
  /** 每个类别的订阅数 */
  counts: Record<string, number>
}>()
const emit = defineEmits<{ changed: [] }>()

const draft = ref<Record<number, { name: string; color: string }>>({})
const newName = ref('')
const newColor = ref('cloud')
const error = ref('')
const busy = ref(false)
const confirmId = ref<number | null>(null)

const edit = (c: CustomCategory) => (draft.value[c.id] ??= { name: c.name, color: c.color })
const colorName = (k: string) => t(categoryNames[k] ?? k)

async function run(fn: () => Promise<unknown>) {
  busy.value = true
  error.value = ''
  try {
    await fn()
    emit('changed')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
const add = () => run(async () => {
  await createSubCategory(newName.value.trim(), newColor.value)
  newName.value = ''
})
const save = (c: CustomCategory) => run(async () => {
  const d = edit(c)
  await updateSubCategory(c.id, d.name.trim(), d.color)
  delete draft.value[c.id]
})
const remove = (c: CustomCategory) => run(async () => {
  await deleteSubCategory(c.id)
  confirmId.value = null
})
</script>

<template>
  <div class="mgr">
    <p class="small muted">{{ t('内置类别之外，可以添加自己的类别（最多 30 个）。删除类别时，用到它的订阅改为“其他”。') }}</p>
    <ul class="rows">
      <li v-for="c in props.list" :key="c.id" class="row">
        <i class="dot" :style="{ background: categoryColors[edit(c).color] }" />
        <input v-model="edit(c).name" maxlength="16" :aria-label="t('类别名称')" />
        <select v-model="edit(c).color" :aria-label="t('颜色')">
          <option v-for="k in builtinCategories" :key="k" :value="k">{{ t('颜色同“{c}”', { c: colorName(k) }) }}</option>
        </select>
        <span class="small muted num">{{ t('{n} 个', { n: props.counts[c.key] ?? 0 }) }}</span>
        <button type="button" class="secondary" :disabled="busy || (edit(c).name === c.name && edit(c).color === c.color)" @click="save(c)">{{ t('保存') }}</button>
        <button v-if="confirmId !== c.id" type="button" class="text danger-text" :disabled="busy" @click="confirmId = c.id">{{ t('删除') }}</button>
        <button v-else type="button" class="danger" :disabled="busy" @click="remove(c)">{{ t('确认删除') }}</button>
      </li>
      <li v-if="!props.list.length" class="small muted">{{ t('还没有自定义类别。') }}</li>
    </ul>
    <form class="row add" @submit.prevent="add">
      <i class="dot" :style="{ background: categoryColors[newColor] }" />
      <input v-model="newName" maxlength="16" :placeholder="t('新类别名称，如 保险、域名')" :aria-label="t('新类别名称')" />
      <select v-model="newColor" :aria-label="t('颜色')">
        <option v-for="k in builtinCategories" :key="k" :value="k">{{ t('颜色同“{c}”', { c: colorName(k) }) }}</option>
      </select>
      <button type="submit" :disabled="busy || !newName.trim()">{{ t('添加') }}</button>
    </form>
    <p v-if="error" class="small bad" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.mgr { display: flex; flex-direction: column; gap: var(--space-3); }
.mgr p { margin: 0; }
.rows { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-2); }
.row { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.row input { flex: 1 1 120px; min-width: 0; }
.row select { flex: 0 1 160px; }
.dot { width: 12px; height: 12px; border-radius: 50%; flex: none; }
.bad { color: var(--bad); }
</style>
