<script setup lang="ts">
// 登录页（设计 8.1）：用户名 + 密码，可选“记住登录”（保存 7 天，否则关闭浏览器即退出）。
// 登录成功后回到原来要访问的页面；使用初始密码时先去修改密码（设计 17.4）。
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../api'
import { login, state } from '../store'

const route = useRoute()
const router = useRouter()
const username = ref('admin')
const password = ref('')
const remember = ref(false)
const error = ref('')
const busy = ref(false)

async function submit() {
  if (!username.value.trim() || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  busy.value = true
  error.value = ''
  try {
    await login(username.value.trim(), password.value, remember.value)
    password.value = ''
    if (state.me?.must_change_password) {
      router.replace({ name: 'account' })
    } else {
      // 只接受站内路径，防止 ?redirect= 被用来跳到外部网站
      const r = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') && !route.query.redirect.startsWith('//')
        ? route.query.redirect : '/'
      router.replace(r)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '登录失败'
    password.value = ''
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="page">
    <form class="panel login" novalidate @submit.prevent="submit">
      <h1>登录</h1>
      <label class="field">
        <span class="small muted">用户名</span>
        <input v-model="username" autocomplete="username" autocapitalize="off" spellcheck="false" />
      </label>
      <label class="field">
        <span class="small muted">密码</span>
        <input v-model="password" type="password" autocomplete="current-password" autofocus />
      </label>
      <label class="remember small">
        <input v-model="remember" type="checkbox" />
        记住登录（7 天）
      </label>
      <p v-if="error" class="banner" role="alert">{{ error }}</p>
      <button type="submit" :disabled="busy">{{ busy ? '登录中…' : '登录' }}</button>
      <p class="small muted hint">忘记密码：在面板主机上执行 <code>vpsmon-server admin reset-password</code></p>
    </form>
  </main>
</template>

<style scoped>
.login { max-width: 380px; margin: var(--space-8) auto 0; display: flex; flex-direction: column; gap: var(--space-4); }
.field { display: flex; flex-direction: column; gap: var(--space-1); }
.remember { display: flex; align-items: center; gap: var(--space-2); color: var(--text-muted); }
.remember input { width: auto; }
.login button { justify-content: center; }
.login .banner { margin: 0; }
.hint { margin: 0; }
</style>
