<script setup lang="ts">
// 账号页：修改密码（设计 17.4）与登录中的设备（设计 24.8）。使用初始 / 重置密码登录时会被强制带到这里，修改后才能使用其他页面。
// 修改成功后，该账号在其他浏览器上的会话全部失效。
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, changePassword } from '../api'
import SessionsPanel from '../components/SessionsPanel.vue'
import { passwordChanged, state } from '../store'

const router = useRouter()
const f = reactive({ current: '', next: '', confirm: '' })
const errors = ref<Record<string, string>>({})
const message = ref('')
const busy = ref(false)
const forced = state.me?.must_change_password ?? false

async function submit() {
  errors.value = {}
  message.value = ''
  if (f.next !== f.confirm) {
    errors.value = { confirm: '两次输入的新密码不一致' }
    return
  }
  busy.value = true
  try {
    await changePassword(f.current, f.next)
    f.current = f.next = f.confirm = ''
    const wasForced = state.me?.must_change_password
    passwordChanged()
    if (wasForced) router.replace('/')
    else message.value = '密码已修改，其他设备上的登录已退出'
  } catch (e) {
    if (e instanceof ApiError && e.details.length) {
      errors.value = Object.fromEntries(e.details.map((d) => [d.field === 'new_password' ? 'next' : d.field === 'current_password' ? 'current' : d.field, d.message]))
    } else if (e instanceof ApiError) {
      message.value = e.message
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="page">
    <form class="panel account" novalidate @submit.prevent="submit">
      <h1>修改密码</h1>
      <p v-if="forced" class="banner warn">当前使用的是初始密码，请先设置自己的密码。</p>
      <p class="small muted">账号：{{ state.me?.username }}。新密码至少 12 位；修改后其他设备上的登录会退出。</p>
      <label class="field">
        <span class="small muted">当前密码</span>
        <input v-model="f.current" type="password" autocomplete="current-password" />
        <small v-if="errors.current" class="bad">{{ errors.current }}</small>
      </label>
      <label class="field">
        <span class="small muted">新密码</span>
        <input v-model="f.next" type="password" autocomplete="new-password" minlength="12" />
        <small v-if="errors.next" class="bad">{{ errors.next }}</small>
      </label>
      <label class="field">
        <span class="small muted">确认新密码</span>
        <input v-model="f.confirm" type="password" autocomplete="new-password" />
        <small v-if="errors.confirm" class="bad">{{ errors.confirm }}</small>
      </label>
      <p v-if="message" class="small" role="status">{{ message }}</p>
      <button type="submit" :disabled="busy">{{ busy ? '保存中…' : '修改密码' }}</button>
    </form>
    <SessionsPanel v-if="!forced" />
    <p v-if="!forced" class="account-links small muted">
      发现异常登录？查看<RouterLink to="/logs?tab=login">登录日志</RouterLink>，修改密码后其他设备会被退出。
    </p>
  </main>
</template>

<style scoped>
.account { max-width: 420px; margin: var(--space-6) auto 0; display: flex; flex-direction: column; gap: var(--space-4); }
.account .banner { margin: 0; }
.account p { margin: 0; }
.field { display: flex; flex-direction: column; gap: var(--space-1); }
.account button { align-self: flex-start; }
.account-links { max-width: 420px; margin: var(--space-4) auto 0; }
</style>
