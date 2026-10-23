<script setup lang="ts">
// 查看者账号（修订第 161 条）：给同事或客户的只读 Web 账号，能看节点、指标、流量、测速与告警，不能修改任何设置，
// 也不能静音或开启维护。新建、重置密码、删除都要先输入管理员密码；初始密码随机生成、只显示一次，首次登录须修改。
import { onMounted, ref } from 'vue'
import { ApiError, createViewer, deleteViewer, errorText, listUsers, reauth, resetViewerPassword, type UserView } from '../api'
import { fmtTime } from '../format'
import { t } from '../i18n'
import { state } from '../store'
import SettingsCard from './SettingsCard.vue'
import CommandBlock from './CommandBlock.vue'

type Action = { kind: 'create' } | { kind: 'reset' | 'delete'; user: UserView }
const users = ref<UserView[]>([])
const error = ref('')
const action = ref<Action | null>(null)
const username = ref('')
const password = ref('')
const fieldErrors = ref<Record<string, string>>({})
const busy = ref(false)
// 刚生成的密码：只显示这一次
const shown = ref<{ username: string; password: string } | null>(null)

async function load() {
  try {
    users.value = await listUsers()
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t('账号列表加载失败'))
  }
}
onMounted(load)

function open(a: Action) {
  action.value = a
  username.value = password.value = ''
  fieldErrors.value = {}
  shown.value = null
}

const titles = { create: '新建查看者', reset: '重置密码', delete: '删除' } as const // 显示时 t()

async function confirmAction() {
  const a = action.value
  if (!a) return
  fieldErrors.value = {}
  if (a.kind === 'create' && !username.value.trim()) {
    fieldErrors.value = { username: t('请输入用户名') }
    return
  }
  if (!password.value) {
    fieldErrors.value = { password: t('请输入你的登录密码以确认') }
    return
  }
  busy.value = true
  try {
    await reauth(password.value)
    if (a.kind === 'create') {
      const r = await createViewer(username.value.trim())
      shown.value = { username: r.user.username, password: r.password }
    } else if (a.kind === 'reset') {
      shown.value = { username: a.user.username, password: (await resetViewerPassword(a.user.id)).password }
    } else {
      await deleteViewer(a.user.id)
    }
    action.value = null
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.details.length) fieldErrors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
    else fieldErrors.value = { form: errorText(e) }
  } finally {
    password.value = ''
    busy.value = false
  }
}
</script>

<template>
  <SettingsCard icon="users" :title="t('查看者账号')"
    :desc="t('只读的 Web 账号，适合给同事或客户：能看节点、指标、流量、测速与告警，不能修改任何设置，也不能静音或开启维护。')">
    <template v-if="!action" #actions>
      <button type="button" class="secondary small" @click="open({ kind: 'create' })">{{ t('新建') }}</button>
    </template>
    <p v-if="error" class="small bad">{{ error }}</p>

    <!-- 刚生成的密码：只显示这一次 -->
    <div v-if="shown" class="created">
      <p class="small"><b>{{ t('请把登录信息交给 {name}', { name: shown.username }) }}</b>{{ t('：密码只显示这一次，对方首次登录后须修改。') }}</p>
      <CommandBlock :command="`${t('用户名')} ${shown.username}  ${t('密码')} ${shown.password}`" />
      <button type="button" class="text small" @click="shown = null">{{ t('我已记下') }}</button>
    </div>

    <form v-if="action" class="form" novalidate @submit.prevent="confirmAction">
      <strong class="small">{{ t(titles[action.kind]) }}<template v-if="action.kind !== 'create'">{{ t('：') }}{{ action.user.username }}</template></strong>
      <p v-if="action.kind === 'reset'" class="small muted">{{ t('生成新的随机密码，对方的登录全部退出，下次登录后须修改。') }}</p>
      <p v-if="action.kind === 'delete'" class="small muted">{{ t('删除后对方立即无法登录。') }}</p>
      <label v-if="action.kind === 'create'">{{ t('用户名') }}
        <input v-model="username" maxlength="32" autocomplete="off" autocapitalize="off" spellcheck="false" :placeholder="t('如 alice')" />
        <small v-if="fieldErrors.username" class="bad">{{ fieldErrors.username }}</small>
        <small v-else class="muted">{{ t('3～32 位字母、数字、点、下划线或连字符') }}</small>
      </label>
      <label>{{ t('你的登录密码') }}
        <input v-model="password" type="password" autocomplete="current-password" />
        <small v-if="fieldErrors.password" class="bad">{{ fieldErrors.password }}</small>
      </label>
      <p v-if="fieldErrors.form" class="small bad">{{ fieldErrors.form }}</p>
      <div class="actions">
        <button type="submit" :class="{ danger: action.kind === 'delete' }" :disabled="busy">{{ busy ? t('处理中…') : t(titles[action.kind]) }}</button>
        <button type="button" class="secondary" @click="action = null">{{ t('取消') }}</button>
      </div>
    </form>

    <ul class="sc-list">
      <li v-for="u in users" :key="u.id">
        <span class="avatar" :class="u.role" aria-hidden="true">{{ u.username.slice(0, 1).toUpperCase() }}</span>
        <div class="who">
          <div class="who-line">
            <strong>{{ u.username }}</strong>
            <span v-if="u.username === state.me?.username" class="muted small">{{ t('（你）') }}</span>
            <span class="pill" :class="{ accent: u.role === 'admin' }">{{ u.role === 'admin' ? t('管理员') : t('查看者') }}</span>
            <span v-if="u.totp_enabled" class="pill on">{{ t('两步验证') }}</span>
          </div>
          <div class="small muted">
            <template v-if="u.must_change_password">{{ t('尚未修改初始密码') }}</template>
            <template v-else-if="u.last_login_at">{{ t('最近登录 {time}', { time: fmtTime(u.last_login_at) }) }}</template>
            <template v-else>{{ t('从未登录') }}</template>
          </div>
        </div>
        <div v-if="u.role === 'viewer'" class="row-actions">
          <button type="button" class="text small" @click="open({ kind: 'reset', user: u })">{{ t('重置密码') }}</button>
          <button type="button" class="text small bad" @click="open({ kind: 'delete', user: u })">{{ t('删除') }}</button>
        </div>
      </li>
    </ul>
    <p v-if="users.length && !users.some((u) => u.role === 'viewer')" class="small muted">{{ t('还没有查看者。点“新建”生成一个只读账号与随机密码。') }}</p>
  </SettingsCard>
</template>

<style scoped>
.created { display: flex; flex-direction: column; gap: var(--space-2); align-items: flex-start; }
.created :deep(.command), .created > :nth-child(2) { align-self: stretch; }
.form { display: flex; flex-direction: column; gap: var(--space-3); }
.form label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.actions { display: flex; gap: var(--space-2); }
.avatar { flex: none; width: 32px; height: 32px; border-radius: 50%; display: grid; place-items: center; font-weight: var(--weight-strong);
  background: var(--surface-2); color: var(--text-muted); }
.avatar.admin { background: color-mix(in srgb, var(--accent) 12%, transparent); color: var(--accent); }
.who { flex: 1; min-width: 0; }
.who-line { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.row-actions { display: flex; gap: var(--space-2); flex-shrink: 0; }
.bad { color: var(--bad); }
</style>
