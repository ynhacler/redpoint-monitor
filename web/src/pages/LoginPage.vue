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
import { cycleTheme, themeMode } from '../theme'

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
// 登录页不显示顶栏，主题切换放在右上角（修订第 332 条）
const themeLabels = { system: '跟随系统', light: '浅色', dark: '深色' } as const

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
  <!-- 登录页（修订第 188、332 条）：电脑宽度左侧为品牌区（强调色渐变与装饰性的状态示意，不含数据），右侧为表单；
       窄屏只保留表单卡片，顶部浅色渐变。配色只用设计令牌，深色模式随之切换。功能与字段不变。 -->
  <main class="login-page">
    <button class="theme-btn" type="button" :title="t('主题：{m}（点击切换）', { m: t(themeLabels[themeMode]) })"
      :aria-label="t('主题：{m}（点击切换）', { m: t(themeLabels[themeMode]) })" @click="cycleTheme">
      <Icon :name="themeMode === 'dark' ? 'moon' : themeMode === 'light' ? 'sun' : 'monitor'" :size="18" />
    </button>

    <!-- 窄屏没有品牌区：左上角显示面板名称 -->
    <span class="mini-brand">{{ site.app_name }}</span>
    <aside class="brand" aria-hidden="true">
      <div class="brand-top">
        <img class="brand-logo" src="/favicon.svg" alt="" width="40" height="40" />
        <span class="brand-name">{{ site.app_name }}</span>
      </div>
      <div class="brand-mid">
        <h2>{{ t('你的服务器，一目了然') }}</h2>
        <p>{{ t('轻量、自托管的服务器监控') }}</p>
        <!-- 装饰：三行节点状态示意（纯图形，没有真实数据） -->
        <div class="mock">
          <div v-for="(w, i) in [72, 46, 88]" :key="i" class="mock-row">
            <i class="mock-dot" :class="{ warn: i === 1 }" />
            <span class="mock-name" :style="{ width: `${[38, 30, 44][i]}%` }" />
            <span class="mock-bar"><span :style="{ width: `${w}%` }" /></span>
          </div>
          <div class="mock-spark">
            <span v-for="(h, i) in [30, 46, 38, 60, 52, 72, 64, 80, 58, 70, 62, 76]" :key="i" :style="{ height: `${h}%` }" />
          </div>
        </div>
      </div>
      <p class="brand-foot">{{ t('数据只存在你自己的服务器上') }}</p>
    </aside>

    <section class="form-side">
      <form v-if="challenge" class="login" novalidate @submit.prevent="submitCode">
        <header class="head">
          <img class="logo" src="/favicon.svg" alt="" width="48" height="48" />
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
          <img class="logo" src="/favicon.svg" alt="" width="48" height="48" />
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
        <p v-if="helpOpen" class="small muted help">{{ t('忘记密码或丢失两步验证设备：由服务器管理员在面板主机上用命令行恢复。') }}</p>
        <div v-if="captchaOn" class="captcha-wrap">
          <SliderCaptcha ref="captchaRef" @done="(a) => (answer = a)" @disabled="captchaOn = false" />
        </div>
        <p v-if="error" class="banner" role="alert">{{ error }}</p>
        <button type="submit" class="submit" :disabled="busy">{{ busy ? t('登录中…') : t('登录') }}</button>
        <!-- 恢复命令不展示给未登录的访客（修订第 173 条）：见文档与 vpsmon-server --help；默认收起（修订第 247 条） -->
      </form>
    </section>
  </main>
</template>

<style scoped>
.login-page { position: relative; min-height: 100dvh; display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); background: var(--bg); }
.theme-btn { position: absolute; top: var(--space-4); right: var(--space-4); z-index: 2; display: grid; place-items: center; width: 40px; height: 40px;
  padding: 0; border-radius: var(--radius-full); border: 1px solid var(--border); background: var(--surface); color: var(--text-muted); cursor: pointer; }
.theme-btn:hover { color: var(--text); filter: none; }

.mini-brand { display: none; }
/* 品牌区：强调色渐变 + 两个柔和的光斑 */
.brand { position: relative; overflow: hidden; display: flex; flex-direction: column; justify-content: space-between; gap: var(--space-7);
  padding: var(--space-8); color: var(--on-accent);
  /* 与背景色混合：浅色模式接近纯强调色，深色模式自动压暗 */
  background: linear-gradient(150deg, color-mix(in srgb, var(--accent) 88%, var(--bg)) 0%,
    color-mix(in srgb, color-mix(in srgb, var(--accent) 55%, var(--series-2)) 82%, var(--bg)) 100%); }
.brand::before, .brand::after { content: ''; position: absolute; border-radius: 50%; pointer-events: none;
  background: radial-gradient(circle, color-mix(in srgb, var(--on-accent) 22%, transparent), transparent 70%); }
.brand::before { width: 520px; height: 520px; right: -180px; top: -160px; }
.brand::after { width: 420px; height: 420px; left: -160px; bottom: -180px; }
.brand > * { position: relative; }
.brand-top { display: flex; align-items: center; gap: var(--space-3); }
.brand-logo { border-radius: 10px; box-shadow: 0 4px 14px color-mix(in srgb, var(--scrim) 25%, transparent); }
.brand-name { font-size: var(--font-xl); font-weight: var(--weight-strong); }
.brand-mid { max-width: 460px; }
.brand-mid h2 { margin: 0; font-size: 2rem; line-height: 2.5rem; font-weight: var(--weight-strong); }
.brand-mid p { margin: var(--space-2) 0 0; font-size: var(--font-lg); opacity: .85; }
.brand-foot { margin: 0; font-size: var(--font-sm); opacity: .75; }
.mock { margin-top: var(--space-7); padding: var(--space-5); display: flex; flex-direction: column; gap: var(--space-3); border-radius: 16px;
  background: color-mix(in srgb, var(--on-accent) 12%, transparent); border: 1px solid color-mix(in srgb, var(--on-accent) 24%, transparent);
  backdrop-filter: blur(6px); box-shadow: 0 20px 40px color-mix(in srgb, var(--scrim) 18%, transparent); }
.mock-row { display: grid; grid-template-columns: 10px minmax(0, 1fr) minmax(0, 1.4fr); align-items: center; gap: var(--space-3); }
.mock-dot { width: 10px; height: 10px; border-radius: 50%; background: var(--ok); box-shadow: 0 0 0 3px color-mix(in srgb, var(--ok) 30%, transparent); }
.mock-dot.warn { background: var(--warn); box-shadow: 0 0 0 3px color-mix(in srgb, var(--warn) 30%, transparent); }
.mock-name { height: 10px; border-radius: var(--radius-full); background: color-mix(in srgb, var(--on-accent) 55%, transparent); }
.mock-bar { height: 8px; border-radius: var(--radius-full); background: color-mix(in srgb, var(--on-accent) 20%, transparent); overflow: hidden; }
.mock-bar span { display: block; height: 100%; border-radius: inherit; background: color-mix(in srgb, var(--on-accent) 85%, transparent); }
.mock-spark { display: flex; align-items: flex-end; gap: 6px; height: 56px; margin-top: var(--space-2); }
.mock-spark span { flex: 1; border-radius: 4px 4px 2px 2px; background: color-mix(in srgb, var(--on-accent) 45%, transparent); }
.mock-spark span:last-child { background: var(--on-accent); }

/* 表单区 */
.form-side { display: flex; align-items: center; justify-content: center; padding: var(--space-8) var(--space-6); }
.login { width: 100%; max-width: 380px; display: flex; flex-direction: column; gap: var(--space-4); }
.head { display: flex; flex-direction: column; align-items: flex-start; gap: var(--space-1); margin-bottom: var(--space-2); }
.head .logo { display: none; }
.head h1 { margin: 0; font-size: 1.75rem; line-height: 2.25rem; }
.head p { margin: 0; font-size: var(--font-md); }
.field { display: flex; flex-direction: column; gap: 6px; }
.label { font-size: var(--font-sm); font-weight: var(--weight-strong); color: var(--text); }
.pw { position: relative; display: block; }
/* 输入框约 48px 高、圆角 10px；聚焦时强调色描边与柔和光圈；显示密码按钮触控区 44 × 44（修订第 247 条） */
.login input:not([type='checkbox']) { min-height: 48px; border-radius: 10px; font-size: 1rem; scroll-margin: 120px 0; background: var(--surface);
  transition: border-color .15s, box-shadow .15s; }
.login input:not([type='checkbox']):focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 22%, transparent); }
.login input[aria-invalid='true'] { border-color: var(--bad); }
.login input[aria-invalid='true']:focus { box-shadow: 0 0 0 3px color-mix(in srgb, var(--bad) 20%, transparent); }
.field .bad { color: var(--bad); font-size: var(--font-sm); }
.pw input { width: 100%; padding-right: 48px; }
.eye { position: absolute; right: 2px; top: 50%; transform: translateY(-50%); width: 44px; height: 44px; display: grid; place-items: center; padding: 0; color: var(--text-muted); }
.row { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); flex-wrap: wrap; }
.help-link { padding: var(--space-2) 0; min-height: 44px; }
.help { margin: -8px 0 0; padding: var(--space-2) var(--space-3); border-radius: var(--radius-sm); background: var(--surface-2); text-align: left; }
.remember { display: flex; align-items: center; gap: var(--space-2); color: var(--text-muted); cursor: pointer; }
.remember input { width: 16px; height: 16px; accent-color: var(--accent); }
.captcha-wrap { display: flex; justify-content: center; padding: var(--space-3); border-radius: var(--radius-md); background: var(--surface-2);
  border: 1px solid var(--border); }
.submit { justify-content: center; width: 100%; min-height: 48px; border-radius: 10px; font-weight: var(--weight-strong); font-size: var(--font-lg);
  box-shadow: 0 8px 20px color-mix(in srgb, var(--accent) 28%, transparent); transition: transform .15s, box-shadow .15s; }
.submit:not(:disabled):hover { transform: translateY(-1px); box-shadow: 0 10px 24px color-mix(in srgb, var(--accent) 34%, transparent); filter: none; }
.login .banner { margin: 0; }
.hint { margin: 0; text-align: center; }
.code { font-size: var(--font-xl); letter-spacing: .3em; text-align: center; }
.back { align-self: center; color: var(--text-muted); padding: 0; }
@media (prefers-reduced-motion: reduce) { .submit, .login input { transition: none; } .submit:not(:disabled):hover { transform: none; } }

/* 窄屏：只显示表单卡片，顶部浅色渐变 */
@media (max-width: 959px) {
  .login-page { grid-template-columns: minmax(0, 1fr);
    background: radial-gradient(120% 60% at 50% 0%, color-mix(in srgb, var(--accent) 14%, var(--bg)), var(--bg) 70%); }
  .brand { display: none; }
  .mini-brand { display: block; position: absolute; top: var(--space-4); left: var(--space-4); line-height: 40px; font-weight: var(--weight-strong);
    color: var(--text); max-width: calc(100% - 96px); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .form-side { align-items: flex-start; padding: 80px var(--space-4) var(--space-6); }
  .login { max-width: 400px; padding: var(--space-6); background: var(--surface); border: 1px solid var(--border); border-radius: 16px;
    box-shadow: 0 16px 40px color-mix(in srgb, var(--scrim) 10%, transparent); }
  .head { align-items: center; text-align: center; }
  .head .logo { display: block; margin-bottom: var(--space-2); border-radius: 12px; box-shadow: 0 6px 16px color-mix(in srgb, var(--accent) 30%, transparent); }
  .head h1 { font-size: 1.5rem; line-height: 2rem; }
}
@media (max-width: 480px) {
  .form-side { padding: 72px var(--space-3) var(--space-5); }
  .login { padding: var(--space-5) var(--space-4); }
  .captcha-wrap { padding: var(--space-2); }
}
</style>
