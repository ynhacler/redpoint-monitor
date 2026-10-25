<script setup lang="ts">
// 登录页（设计 8.1）：用户名 + 密码，可选“记住登录”（保存 7 天，否则关闭浏览器即退出）。
// 登录成功后回到原来要访问的页面；使用初始密码时先去修改密码（设计 17.4）。
// 开启了两步验证的账号在密码之后再输入认证器的 6 位验证码或恢复码（修订第 160 条）。
import { nextTick, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { errorText, UnauthorizedError, type CaptchaAnswer } from '../api'
import Icon from '../components/Icon.vue'
import SliderCaptcha from '../components/SliderCaptcha.vue'
import { t } from '../i18n'
import { site } from '../site'
import { login, loginTotp, state } from '../store'

const route = useRoute()
const router = useRouter()
// 不预填用户名（修订第 247 条，UI 精修 3.12）：浏览器 / 密码管理器自动填充照常可用
const username = ref('')
const password = ref('')
const remember = ref(false)
const showPassword = ref(false) // 密码明文显示开关（修订第 188 条）
const error = ref('')
const busy = ref(false)
// 字段错误显示在对应输入框下（修订第 247 条）
const fieldErr = ref<{ username?: string; password?: string }>({})
const helpOpen = ref(false)
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
    error.value = t('请输入验证码')
    return
  }
  busy.value = true
  error.value = ''
  try {
    await loginTotp(challenge.value, code.value.trim())
    done()
  } catch (e) {
    error.value = failText(e, t('验证失败'))
    code.value = ''
    // challenge 过期或尝试次数用完：回到第一步重新输入密码
    if (e instanceof UnauthorizedError && e.code === 'token_expired') backToPassword()
    else codeInput.value?.focus()
  } finally {
    busy.value = false
  }
}

async function submit() {
  fieldErr.value = {
    username: username.value.trim() ? undefined : t('请输入用户名'),
    password: password.value ? undefined : t('请输入密码'),
  }
  if (fieldErr.value.username || fieldErr.value.password) {
    error.value = ''
    return
  }
  if (captchaOn.value && !answer.value) {
    error.value = t('请先拖动滑块完成验证')
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
    error.value = failText(e, t('登录失败'))
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
        <h1>{{ t('两步验证') }}</h1>
        <p class="small muted">{{ t('打开认证器 App（如 Google Authenticator、1Password），输入 {name} 当前显示的 6 位数字', { name: site.app_name }) }}</p>
      </header>
      <label class="field">
        <span class="label">{{ t('验证码') }}</span>
        <input ref="codeInput" v-model="code" class="num code" inputmode="numeric" autocomplete="one-time-code" autocapitalize="off"
          spellcheck="false" maxlength="11" placeholder="123456" />
      </label>
      <p v-if="error" class="banner" role="alert">{{ error }}</p>
      <button type="submit" class="submit" :disabled="busy">{{ busy ? t('验证中…') : t('验证并登录') }}</button>
      <p class="small muted hint">{{ t('手机不在身边：输入开启时保存的恢复码（如 k7m2-x9qp），每个只能用一次。') }}</p>
      <button type="button" class="text back" @click="backToPassword">← {{ t('返回重新输入密码') }}</button>
    </form>
    <form v-else class="login" novalidate @submit.prevent="submit">
      <header class="head">
        <!-- 顶栏已有产品名，这里不再重复（修订第 247 条） -->
        <img class="logo" src="/favicon.svg" alt="" width="52" height="52" />
        <h1>{{ t('登录账号') }}</h1>
        <p class="small muted">{{ t('使用账号密码安全登录') }}</p>
      </header>
      <label class="field">
        <span class="label">{{ t('用户名') }}</span>
        <input v-model="username" autocomplete="username" autocapitalize="off" spellcheck="false" :placeholder="t('请输入用户名')" autofocus
          :aria-invalid="!!fieldErr.username" @input="fieldErr.username = undefined" />
        <small v-if="fieldErr.username" class="bad" role="alert">{{ fieldErr.username }}</small>
      </label>
      <label class="field">
        <span class="label">{{ t('密码') }}</span>
        <span class="pw">
          <input v-model="password" :type="showPassword ? 'text' : 'password'" autocomplete="current-password" :placeholder="t('请输入密码')"
            :aria-invalid="!!fieldErr.password" @input="fieldErr.password = undefined" />
          <button type="button" class="text eye" :aria-label="showPassword ? t('隐藏密码') : t('显示密码')" :aria-pressed="showPassword"
            @click="showPassword = !showPassword"><Icon :name="showPassword ? 'eye-off' : 'eye'" :size="18" /></button>
        </span>
        <small v-if="fieldErr.password" class="bad" role="alert">{{ fieldErr.password }}</small>
      </label>
      <div class="row">
        <label class="remember small" :title="t('保存的是登录会话，不是密码；退出登录后失效')">
          <input v-model="remember" type="checkbox" />
          {{ t('记住登录（7 天）') }}
        </label>
        <button type="button" class="text small help-link" :aria-expanded="helpOpen" @click="helpOpen = !helpOpen">{{ t('无法登录？') }}</button>
      </div>
      <SliderCaptcha v-if="captchaOn" ref="captchaRef" @done="(a) => (answer = a)" @disabled="captchaOn = false" />
      <p v-if="error" class="banner" role="alert">{{ error }}</p>
      <button type="submit" class="submit" :disabled="busy">{{ busy ? t('登录中…') : t('登录') }}</button>
      <!-- 恢复命令不展示给未登录的访客（修订第 173 条）：见文档与 vpsmon-server --help；默认收起（修订第 247 条） -->
      <p v-if="helpOpen" class="small muted hint help">{{ t('忘记密码或丢失两步验证设备：由服务器管理员在面板主机上用命令行恢复。') }}</p>
    </form>
  </main>
</template>

<style scoped>
.login-page { display: flex; justify-content: center; }
.login { width: 100%; max-width: 400px; margin: var(--space-8) auto 0; display: flex; flex-direction: column; gap: var(--space-4);
  padding: var(--space-6); background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md);
  box-shadow: 0 12px 32px color-mix(in srgb, var(--text) 8%, transparent); }
.head { display: flex; flex-direction: column; align-items: center; gap: var(--space-2); text-align: center; margin-bottom: var(--space-2); }
.head h1 { margin: var(--space-1) 0 0; font-size: 22px; line-height: 30px; }
.head p { margin: 0; }
.logo { display: block; }
.field { display: flex; flex-direction: column; gap: var(--space-1); }
.label { font-size: var(--font-sm); font-weight: var(--weight-strong); color: var(--text); }
.pw { position: relative; display: block; }
/* 输入框约 48px 高、圆角 10px；显示密码按钮触控区 44 × 44（修订第 247 条） */
.login input:not([type='checkbox']) { min-height: 48px; border-radius: 10px; font-size: 16px; scroll-margin: 120px 0; }
.login input[aria-invalid='true'] { border-color: var(--bad); }
.field .bad { color: var(--bad); font-size: var(--font-sm); }
.pw input { width: 100%; padding-right: 48px; }
.eye { position: absolute; right: 2px; top: 50%; transform: translateY(-50%); width: 44px; height: 44px; display: grid; place-items: center; padding: 0; }
.row { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); flex-wrap: wrap; }
.help-link { padding: var(--space-2) 0; min-height: 44px; }
.help { text-align: left; }
.remember { display: flex; align-items: center; gap: var(--space-2); color: var(--text-muted); }
.remember input { width: auto; }
.submit { justify-content: center; width: 100%; min-height: 48px; border-radius: 10px; font-weight: var(--weight-strong); }
.login .banner { margin: 0; }
.login :deep(.captcha) { align-self: center; }
.hint { margin: 0; text-align: center; }
.code { font-size: var(--font-lg); letter-spacing: .2em; text-align: center; }
.back { align-self: center; color: var(--text-muted); padding: 0; }
@media (max-width: 480px) {
  .login { margin-top: var(--space-4); padding: var(--space-5) var(--space-4); box-shadow: none; }
}
</style>
