<script setup lang="ts">
// 批量修改节点（修订第 281 条）：在仪表板当前筛选出的节点中勾选，一次设置分组、添加或去掉网络线路。
// 线路标签点一下为“添加”，再点为“去掉”，第三下恢复不变；提交后全部成功或全部不变（面板在一个事务中修改）。
import { computed, ref } from 'vue'
import { bulkEditServers, errorText, type ServerView } from '../api'
import { t } from '../i18n'
import { linePresets } from '../lines'
import { refresh } from '../store'
import Flag from './Flag.vue'

const props = defineProps<{
  /** 可勾选的节点：仪表板当前筛选的结果 */
  servers: ServerView[]
  /** 已有的分组，供选择 */
  groups: string[]
}>()
const emit = defineEmits<{
  /** 关闭面板（已保存或取消） */
  close: []
}>()

const selected = ref(new Set<number>())
const allOn = computed(() => props.servers.length > 0 && props.servers.every((s) => selected.value.has(s.id)))
function toggle(id: number) {
  const next = new Set(selected.value)
  if (!next.delete(id)) next.add(id)
  selected.value = next
}
function toggleAll() {
  selected.value = allOn.value ? new Set() : new Set(props.servers.map((s) => s.id))
}

// 分组：KEEP 不修改，CLEAR 清除，NEW 输入新分组，其余为已有分组
const KEEP = '\u0000keep', CLEAR = '\u0000clear', NEW = '\u0000new'
const groupSel = ref(KEEP)
const newGroup = ref('')

// 线路：每个标签的操作；可选的标签为常见线路、节点上已有的与自己输入的
const ops = ref(new Map<string, 'add' | 'remove'>())
const custom = ref('')
const extra = ref<string[]>([])
const choices = computed(() => [...new Set([...linePresets, ...props.servers.flatMap((s) => s.line_tags), ...extra.value])])
function cycle(tag: string) {
  const next = new Map(ops.value)
  const cur = next.get(tag)
  if (!cur) next.set(tag, 'add')
  else if (cur === 'add') next.set(tag, 'remove')
  else next.delete(tag)
  ops.value = next
}
function addCustom() {
  const v = custom.value.trim().replace(/\s+/g, ' ')
  custom.value = ''
  if (!v) return
  if (!choices.value.includes(v)) extra.value = [...extra.value, v]
  ops.value = new Map(ops.value).set(v, 'add')
}
const opLabel = (tag: string) => {
  const op = ops.value.get(tag)
  return op === 'add' ? '+ ' : op === 'remove' ? '− ' : ''
}

const hasChange = computed(() => groupSel.value !== KEEP || ops.value.size > 0)
const saving = ref(false)
const error = ref('')
async function save() {
  saving.value = true
  error.value = ''
  const tags = (op: 'add' | 'remove') => [...ops.value].filter(([, o]) => o === op).map(([k]) => k)
  try {
    await bulkEditServers({
      ids: props.servers.filter((s) => selected.value.has(s.id)).map((s) => s.id),
      ...(groupSel.value === KEEP ? {} : { group: groupSel.value === CLEAR ? '' : groupSel.value === NEW ? newGroup.value : groupSel.value }),
      add_line_tags: tags('add'),
      remove_line_tags: tags('remove'),
    })
    await refresh()
    emit('close')
  } catch (e) {
    error.value = errorText(e, t('保存失败'))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="panel bulk" :aria-label="t('批量修改')">
    <div class="head">
      <strong>{{ t('批量修改') }}</strong>
      <span class="muted small">{{ t('勾选节点，设置分组或添加 / 去掉网络线路；没有设置的项目保持不变。') }}</span>
    </div>

    <label class="row all"><input type="checkbox" :checked="allOn" @change="toggleAll" />{{ t('全选（{n} 个）', { n: servers.length }) }}</label>
    <ul class="list">
      <li v-for="s in servers" :key="s.id">
        <label class="row">
          <input type="checkbox" :checked="selected.has(s.id)" @change="toggle(s.id)" />
          <span class="nm"><Flag :code="s.country" /> {{ s.name }}</span>
          <span class="muted small meta">{{ [s.group, ...s.line_tags].filter(Boolean).join(' · ') }}</span>
        </label>
      </li>
    </ul>

    <div class="fields">
      <label class="field">{{ t('分组') }}
        <select v-model="groupSel">
          <option :value="KEEP">{{ t('不修改') }}</option>
          <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
          <option :value="NEW">{{ t('新分组…') }}</option>
          <option :value="CLEAR">{{ t('清除分组') }}</option>
        </select>
        <input v-if="groupSel === NEW" v-model="newGroup" maxlength="32" :placeholder="t('分组名称')" />
      </label>
      <div class="field">{{ t('网络线路') }}
        <div class="chips">
          <button v-for="l in choices" :key="l" type="button" class="chip" :class="ops.get(l)" @click="cycle(l)">{{ opLabel(l) }}{{ l }}</button>
          <input v-model="custom" class="custom" maxlength="20" :placeholder="t('其他，回车添加')" @keydown.enter.prevent="addCustom" @blur="addCustom" />
        </div>
        <small class="muted">{{ t('点一下添加，再点一下去掉，第三下恢复不变') }}</small>
      </div>
    </div>

    <p v-if="error" class="small err">{{ error }}</p>
    <div class="actions">
      <button type="button" class="btn" :disabled="saving || !selected.size || !hasChange || (groupSel === NEW && !newGroup.trim())" @click="save">
        {{ saving ? t('保存中…') : t('应用到 {n} 个节点', { n: selected.size }) }}
      </button>
      <button type="button" class="btn secondary" :disabled="saving" @click="emit('close')">{{ t('取消') }}</button>
    </div>
  </section>
</template>

<style scoped>
.bulk { margin-bottom: var(--space-4); display: flex; flex-direction: column; gap: var(--space-3); }
.head { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-2); }
.list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-1); max-height: 320px; overflow-y: auto; }
.row { display: flex; align-items: center; gap: var(--space-2); padding: var(--space-2) var(--space-3); border: 1px solid var(--border);
  border-radius: var(--radius-md); cursor: pointer; min-width: 0; }
.row.all { border: 0; padding: 0 var(--space-3); }
.row input { flex: none; }
.nm { flex: none; max-width: 50%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.meta { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-align: right; }
.fields { display: flex; flex-direction: column; gap: var(--space-3); }
.field { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.field select, .field input { max-width: 320px; }
.chips { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; }
.chip { flex: none; padding: 2px var(--space-3); border-radius: var(--radius-full); border: 1px solid var(--border); background: var(--surface);
  color: var(--text); font: inherit; font-size: var(--font-sm); }
.chip.add { border-color: var(--accent); color: var(--accent); background: var(--surface-2); }
.chip.remove { border-color: var(--bad); color: var(--bad); text-decoration: line-through; }
.chips .custom { flex: 0 1 140px; min-width: 100px; }
.actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
.err { color: var(--bad); }
</style>
