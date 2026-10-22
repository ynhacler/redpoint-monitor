<script setup lang="ts">
// 登录页（设计 8.1）：用户名 + 密码，可选“记住登录”（保存 7 天，否则关闭浏览器即退出）。
// 登录成功后回到原来要访问的页面；使用初始密码时先去修改密码（设计 17.4）。
// 开启了两步验证的账号在密码之后再输入认证器的 6 位验证码或恢复码（修订第 160 条）。
import { nextTick, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { errorText, UnauthorizedError, type CaptchaAnswer } from '../api'
import Icon from '../components/Icon.vue'
import SliderCaptcha from '../components/SliderCaptcha.vue'
import { login, loginTotp, state } from '../store'

const route = useRoute()
const router = useRouter()
const username = ref('admin')
const password = ref('')
const remember = ref(false)
const showPassword = ref(false) // 密码明文显示开关（修订第 188 条）
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
  <!-- 登录页（修订第 188 条）：居中卡片，顶部为面板标志、标题与一句说明；配色与组件沿用设计令牌，与面板一致 -->
  <main class="page login-page">
    <form v-if="challenge" class="login" novalidate @submit.prevent="submitCode">
      <header class="head">
        <img class="logo" src="/favicon.svg" alt="" width="44" height="44" />
        <h1>两步验证</h1>
        <p class="small muted">打开认证器 App（如 Google Authenticator、1Password），输入 VPS Monitor 当前显示的 6 位数字</p>
      </header>
      <label class="field">
        <span class="label">验证码</span>
        <input ref="codeInput" v-model="code" class="num code" inputmode="numeric" autocomplete="one-time-code" autocapitalize="off"
          spellcheck="false" maxlength="11" placeholder="123456" />
      </label>
      <p v-if="error" class="banner" role="alert">{{ error }}</p>
      <button type="submit" class="submit" :disabled="busy">{{ busy ? '验证中…' : '验证并登录' }}</button>
      <p class="small muted hint">手机不在身边：输入开启时保存的恢复码（如 k7m2-x9qp），每个只能用一次。</p>
      <button type="button" class="text back" @click="backToPassword">← 返回重新输入密码</button>
    </form>
    <form v-else class="login" novalidate @submit.prevent="submit">
      <header class="head">
        <img class="logo" src="/favicon.svg" alt="" width="44" height="44" />
        <h1>登录 VPS Monitor</h1>
        <p class="small muted">输入账号与密码以继续</p>
      </header>
      <label class="field">
        <span class="label">用户名</span>
        <input v-model="username" autocomplete="username" autocapitalize="off" spellcheck="false" />
      </label>
      <label class="field">
        <span class="label">密码</span>
        <span class="pw">
          <input v-model="password" :type="showPassword ? 'text' : 'password'" autocomplete="current-password" autofocus />
          <button type="button" class="text eye" :aria-label="showPassword ? '隐藏密码' : '显示密码'" :aria-pressed="showPassword"
            @click="showPassword = !showPassword"><Icon :name="showPassword ? 'eye-off' : 'eye'" :size="16" /></button>
        </span>
      </label>
      <label class="remember small">
        <input v-model="remember" type="checkbox" />
        记住登录（7 天）
      </label>
      <SliderCaptcha v-if="captchaOn" ref="captchaRef" @done="(a) => (answer = a)" @disabled="captchaOn = false" />
      <p v-if="error" class="banner" role="alert">{{ error }}</p>
      <button type="submit" class="submit" :disabled="busy">{{ busy ? '登录中…' : '登录' }}</button>
      <!-- 恢复命令不展示给未登录的访客（修订第 173 条）：见文档与 vpsmon-server --help -->
      <p class="small muted hint">忘记密码或丢失两步验证设备：由服务器管理员在面板主机上用命令行恢复。</p>
    </form>
  </main>
</template>

<style scoped>
.login-page { display: flex; justify-content: center; }
.login { width: 100%; max-width: 400px; margin: var(--space-8) auto 0; display: flex; flex-direction: column; gap: var(--space-4);
  padding: var(--space-6); background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md);
  box-shadow: 0 12px 32px color-mix(in srgb, var(--text) 8%, transparent); }
.head { display: flex; flex-direction: column; align-items: center; gap: var(--space-2); text-align: center; margin-bottom: var(--space-2); }
.head h1 { margin: var(--space-1) 0 0; font-size: var(--font-xl); line-height: var(--line-xl); }
.head p { margin: 0; }
.logo { display: block; }
.field { display: flex; flex-direction: column; gap: var(--space-1); }
.label { font-size: var(--font-sm); font-weight: var(--weight-strong); color: var(--text); }
.pw { position: relative; display: block; }
.pw input { width: 100%; padding-right: 40px; }
.eye { position: absolute; right: var(--space-1); top: 50%; transform: translateY(-50%); padding: var(--space-1) var(--space-2); }
.remember { display: flex; align-items: center; gap: var(--space-2); color: var(--text-muted); }
.remember input { width: auto; }
.submit { justify-content: center; width: 100%; padding-top: var(--space-3); padding-bottom: var(--space-3); font-weight: var(--weight-strong); }
.login .banner { margin: 0; }
.login :deep(.captcha) { align-self: center; }
.hint { margin: 0; text-align: center; }
.code { font-size: var(--font-lg); letter-spacing: .2em; text-align: center; }
.back { align-self: center; color: var(--text-muted); padding: 0; }
@media (max-width: 480px) {
  .login { margin-top: var(--space-4); padding: var(--space-5) var(--space-4); box-shadow: none; }
}
</style>
