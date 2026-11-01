<script setup lang="ts">
// 变更通知（修订第 275 条，设计 4.9.5）：各类变更是否发送通知。关闭的类别仍记录在节点详情“安全”页的“变更记录”中。
import { computed, onMounted, reactive, ref } from 'vue'
import { errorText, getChangeNotify, saveChangeNotify, type ChangeNotify } from '../api'
import { t } from '../i18n'
import SaveState, { saveStatus } from './SaveState.vue'

const form = reactive<ChangeNotify>({ firewall: true, accounts: true, files: true })
const snap = ref('')
const saving = ref(false)
const saved = ref(false)
const error = ref('')
const dirty = computed(() => !!snap.value && JSON.stringify(form) !== snap.value)

const items: { key: keyof ChangeNotify; label: string; hint: string }[] = [
  { key: 'firewall', label: '防火墙', hint: '启用与停用、入站默认策略、规则增删' },
  { key: 'accounts', label: '账号与权限组', hint: '可登录账号、UID 0、sudo 等管理员组成员' },
  { key: 'files', label: '重要文件', hint: 'SSH、sudo、定时任务、systemd 等；装软件时也可能变化' },
]

function fill(v: ChangeNotify) {
  Object.assign(form, v)
  snap.value = JSON.stringify(form)
}

onMounted(async () => {
  try {
    fill(await getChangeNotify())
  } catch (e) {
    error.value = errorText(e, t('加载失败'))
  }
})

async function save() {
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    fill(await saveChangeNotify({ ...form }))
    saved.value = true
  } catch (e) {
    error.value = errorText(e, t('保存失败'))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <form class="panel change-notify" novalidate @submit.prevent="save">
    <div class="title">{{ t('变更通知') }}</div>
    <p class="muted small">{{ t('节点上的配置有变化时通知（通知中只有类别与条数，细节在节点详情“安全”页）。关闭的类别仍会记录，只是不发送。') }}</p>
    <div class="items">
      <label v-for="i in items" :key="i.key" class="check">
        <input v-model="form[i.key]" type="checkbox" />
        <span><span class="name">{{ t(i.label) }}</span><small class="muted">{{ t(i.hint) }}</small></span>
      </label>
    </div>
    <p v-if="error" class="err small">{{ error }}</p>
    <div class="actions">
      <button type="submit" :disabled="saving || !dirty" :title="!dirty ? t('没有修改') : undefined">{{ saving ? t('保存中…') : t('保存') }}</button>
      <SaveState :state="saveStatus({ saving, error, dirty, saved })" :error="error || undefined" />
    </div>
  </form>
</template>

<style scoped>
.change-notify { padding: var(--space-4); margin-bottom: var(--space-4); }
.title { font-weight: var(--weight-strong); }
.change-notify > p { margin: var(--space-2) 0 var(--space-3); }
.items { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: var(--space-3); }
.check { display: flex; align-items: flex-start; gap: var(--space-2); font-size: var(--font-sm); min-height: 44px; }
.check input { width: auto; margin-top: 3px; }
.check > span { display: flex; flex-direction: column; gap: 2px; }
.name { font-weight: var(--weight-strong); }
.actions { display: flex; align-items: center; gap: var(--space-3); margin-top: var(--space-3); }
.err { color: var(--bad); }
</style>
