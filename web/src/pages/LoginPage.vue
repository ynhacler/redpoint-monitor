<script setup lang="ts">
// 登录页（设计 8.1）：用户名 + 密码，可选“记住登录”（保存 7 天，否则关闭浏览器即退出）。
// 登录成功后回到原来要访问的页面；使用初始密码时先去修改密码（设计 17.4）。
// 开启了两步验证的账号在密码之后再输入认证器的 6 位验证码或恢复码（修订第 160 条）。
import { nextTick, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { errorText, UnauthorizedError, type CaptchaAnswer } from '../api'
import SliderCaptcha from '../components/SliderCaptcha.vue'
import { login, loginTotp, state } from '../store'

const route = useRoute()
const router = useRouter()
const username = ref('admin')
const password = ref('')
const remember = ref(false)
const error = ref('')
const busy = ref(false)
// 滑动验证码（设计 17.4）：完成后才能登录；每次登录尝试后重新获取（一次性）
const captchaOn = ref(true)
const answer = ref<CaptchaAnswer>()
const captchaRef = ref<InstanceType<typeof SliderCaptcha>>()
// 第二步：密码正确后面板返回的 challenge（5 分钟内有效）
const challenge = ref('')
const code = ref('')
const codeInput = ref<HTMLInputElement>()

// errorText 对 401 返回空（其他页面由统一处理回到登录页），登录页要直接显示面板的说明（用户名或密码错误、验证码不正确）
function failText(e: unknown, fallback: string) {
  return e instanceof UnauthorizedError ? e.message || fallback : errorText(e, fallback)
}

function done() {
  if (state.me?.must_change_password) {
    router.replace({ name: 'account' })
    return
  }
  // 只接受站内路径，防止 ?redirect= 被用来跳到外部网站
  const r = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') && !route.query.redirect.startsWith('//')
    ? route.query.redirect : '/'
  router.replace(r)
}

function backToPassword() {
  challenge.value = ''
  code.value = ''
  answer.value = undefined
  captchaRef.value?.reload()
}

async function submitCode() {
  if (!code.value.trim()) {
    error.value = '请输入验证码'
    return
  }
  busy.value = true
  error.value = ''
  try {
    await loginTotp(challenge.value, code.value.trim())
    done()
  } catch (e) {
    error.value = failText(e, '验证失败')
    code.value = ''
    // challenge 过期或尝试次数用完：回到第一步重新输入密码
    if (e instanceof UnauthorizedError && e.code === 'token_expired') backToPassword()
    else codeInput.value?.focus()
  } finally {
    busy.value = false
  }
}

async function submit() {
  if (!username.value.trim() || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  if (captchaOn.value && !answer.value) {
    error.value = '请先拖动滑块完成验证'
    return
  }
  busy.value = true
  error.value = ''
  try {
    const c = await login(username.value.trim(), password.value, remember.value, answer.value)
    password.value = ''
    if (c) {
      challenge.value = c
      await nextTick()
      codeInput.value?.focus()
      return
    }
    done()
  } catch (e) {
    error.value = failText(e, '登录失败')
    password.value = ''
    answer.value = undefined
    captchaRef.value?.reload()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="page">
    <form v-if="challenge" class="panel login" novalidate @submit.prevent="submitCode">
      <h1>两步验证</h1>
      <p class="small muted hint">打开手机上的认证器 App（如 Google Authenticator、1Password），输入 VPS Monitor 当前显示的 6 位数字。</p>
      <label class="field">
        <span class="small muted">验证码</span>
        <input ref="codeInput" v-model="code" class="num code" inputmode="numeric" autocomplete="one-time-code" autocapitalize="off"
          spellcheck="false" maxlength="11" placeholder="123456" />
      </label>
      <p v-if="error" class="banner" role="alert">{{ error }}</p>
      <button type="submit" :disabled="busy">{{ busy ? '验证中…' : '验证并登录' }}</button>
      <p class="small muted hint">手机不在身边：输入开启时保存的恢复码（如 k7m2-x9qp），每个只能用一次。</p>
      <button type="button" class="text back" @click="backToPassword">← 返回重新输入密码</button>
    </form>
    <form v-else class="panel login" novalidate @submit.prevent="submit">
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
      <SliderCaptcha v-if="captchaOn" ref="captchaRef" @done="(a) => (answer = a)" @disabled="captchaOn = false" />
      <p v-if="error" class="banner" role="alert">{{ error }}</p>
      <button type="submit" :disabled="busy">{{ busy ? '登录中…' : '登录' }}</button>
      <p class="small muted hint">忘记密码：在面板主机上执行 <code>vpsmon-server admin reset-password</code>；
        丢失两步验证设备且没有恢复码：执行 <code>vpsmon-server admin disable-totp</code></p>
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
.code { font-size: var(--font-lg); letter-spacing: .2em; }
.back { align-self: flex-start; color: var(--text-muted); padding: 0; }
</style>
