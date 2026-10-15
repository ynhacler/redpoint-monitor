<script setup lang="ts">
// 查看者账号（修订第 161 条）：给同事或客户的只读 Web 账号，能看节点、指标、流量、测速与告警，不能修改任何设置，
// 也不能静音或开启维护。新建、重置密码、删除都要先输入管理员密码；初始密码随机生成、只显示一次，首次登录须修改。
import { onMounted, ref } from 'vue'
import { ApiError, createViewer, deleteViewer, errorText, listUsers, reauth, resetViewerPassword, type UserView } from '../api'
import { fmtTime } from '../format'
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
    error.value = errorText(e, '账号列表加载失败')
  }
}
onMounted(load)

function open(a: Action) {
  action.value = a
  username.value = password.value = ''
  fieldErrors.value = {}
  shown.value = null
}

const titles = { create: '新建查看者', reset: '重置密码', delete: '删除' } as const

async function confirmAction() {
  const a = action.value
  if (!a) return
  fieldErrors.value = {}
  if (a.kind === 'create' && !username.value.trim()) {
    fieldErrors.value = { username: '请输入用户名' }
    return
  }
  if (!password.value) {
    fieldErrors.value = { password: '请输入你的登录密码以确认' }
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
  <section class="panel users">
    <div class="head">
      <h2>查看者账号</h2>
      <button v-if="!action" type="button" class="secondary small" @click="open({ kind: 'create' })">新建</button>
    </div>
    <p class="small muted">
      只读的 Web 账号，适合给同事或客户：能看节点、指标、流量、测速与告警，不能修改任何设置，也不能静音或开启维护。
    </p>
    <p v-if="error" class="small bad">{{ error }}</p>

    <!-- 刚生成的密码：只显示这一次 -->
    <div v-if="shown" class="created">
      <p class="small"><b>请把登录信息交给 {{ shown.username }}</b>：密码只显示这一次，对方首次登录后须修改。</p>
      <CommandBlock :command="`用户名 ${shown.username}  密码 ${shown.password}`" />
      <button type="button" class="text small" @click="shown = null">我已记下</button>
    </div>

    <form v-if="action" class="form" novalidate @submit.prevent="confirmAction">
      <strong class="small">{{ titles[action.kind] }}<template v-if="action.kind !== 'create'">：{{ action.user.username }}</template></strong>
      <p v-if="action.kind === 'reset'" class="small muted">生成新的随机密码，对方的登录全部退出，下次登录后须修改。</p>
      <p v-if="action.kind === 'delete'" class="small muted">删除后对方立即无法登录。</p>
      <label v-if="action.kind === 'create'">用户名
        <input v-model="username" maxlength="32" autocomplete="off" autocapitalize="off" spellcheck="false" placeholder="如 alice" />
        <small v-if="fieldErrors.username" class="bad">{{ fieldErrors.username }}</small>
        <small v-else class="muted">3～32 位字母、数字、点、下划线或连字符</small>
      </label>
      <label>你的登录密码
        <input v-model="password" type="password" autocomplete="current-password" />
        <small v-if="fieldErrors.password" class="bad">{{ fieldErrors.password }}</small>
      </label>
      <p v-if="fieldErrors.form" class="small bad">{{ fieldErrors.form }}</p>
      <div class="actions">
        <button type="submit" :class="{ danger: action.kind === 'delete' }" :disabled="busy">{{ busy ? '处理中…' : titles[action.kind] }}</button>
        <button type="button" class="secondary" @click="action = null">取消</button>
      </div>
    </form>

    <ul class="list">
      <li v-for="u in users" :key="u.id">
        <div class="who">
          <strong>{{ u.username }}</strong>
          <span class="tag">{{ u.role === 'admin' ? '管理员' : '查看者' }}</span>
          <span v-if="u.totp_enabled" class="tag">两步验证</span>
          <div class="small muted">
            <template v-if="u.must_change_password">尚未修改初始密码</template>
            <template v-else-if="u.last_login_at">最近登录 {{ fmtTime(u.last_login_at) }}</template>
            <template v-else>从未登录</template>
          </div>
        </div>
        <div v-if="u.role === 'viewer'" class="row-actions">
          <button type="button" class="text small" @click="open({ kind: 'reset', user: u })">重置密码</button>
          <button type="button" class="text small bad" @click="open({ kind: 'delete', user: u })">删除</button>
        </div>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.users { max-width: 420px; margin: var(--space-4) auto 0; }
.head { display: flex; align-items: center; justify-content: space-between; margin-bottom: var(--space-1); }
.users h2 { margin: 0; font-size: var(--font-lg); }
.users p { margin: var(--space-1) 0 0; }
.created { margin-top: var(--space-3); display: flex; flex-direction: column; gap: var(--space-2); align-items: flex-start; }
.created :deep(.command), .created > :nth-child(2) { align-self: stretch; }
.form { display: flex; flex-direction: column; gap: var(--space-3); margin-top: var(--space-3); }
.form label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.actions { display: flex; gap: var(--space-2); }
.list { list-style: none; margin: var(--space-3) 0 0; padding: 0; }
.list li { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); padding: var(--space-3) 0; }
.list li + li { border-top: 1px solid var(--border); }
.who { min-width: 0; }
.row-actions { display: flex; gap: var(--space-2); flex-shrink: 0; }
.tag { margin-left: var(--space-2); font-size: var(--font-xs); color: var(--text-muted); border: 1px solid var(--border);
  border-radius: var(--radius-sm); padding: 0 var(--space-1); }
.bad { color: var(--bad); }
</style>
