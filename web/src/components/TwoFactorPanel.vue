<script setup lang="ts">
// 两步验证（设计 17.4，修订第 160 条）：开启后 Web 登录在密码之后还要输入认证器 App 的 6 位验证码。
// 开启：输入密码 → 扫码（或手动输入密钥）→ 输入验证码确认 → 保存 10 个恢复码（只显示这一次）。关闭同样要先输入密码。
// 二维码在浏览器本地生成（QrCode），密钥不发给任何第三方。
import { computed, reactive, ref } from 'vue'
import { ApiError, errorText, getMe, reauth, totpDisable, totpEnable, totpSetup } from '../api'
import { state } from '../store'
import QrCode from './QrCode.vue'

type Step = 'idle' | 'password' | 'scan' | 'codes'
const step = ref<Step>('idle')
const enabled = computed(() => state.me?.totp_enabled ?? false)
const left = computed(() => state.me?.recovery_codes_left ?? 0)
const f = reactive({ password: '', code: '' })
const setup = ref<{ secret: string; otpauth_uri: string } | null>(null)
const codes = ref<string[]>([])
const errors = ref<Record<string, string>>({})
const busy = ref(false)
const copied = ref(false)

// 密钥每 4 个字符一组，方便手动输入
const groupedSecret = computed(() => setup.value?.secret.match(/.{1,4}/g)?.join(' ') ?? '')

function reset() {
  step.value = 'idle'
  f.password = f.code = ''
  setup.value = null
  errors.value = {}
}

async function refreshMe() {
  try {
    state.me = await getMe()
  } catch {
    /* 读取失败时保持原状，下次刷新页面再更新 */
  }
}

function fail(e: unknown, field: string) {
  if (e instanceof ApiError && e.details.length) errors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
  else errors.value = { [field]: errorText(e) }
}

// 第一步：重新验证密码，然后开始设置（开启）或直接关闭
async function confirmPassword() {
  errors.value = {}
  if (!f.password) {
    errors.value = { password: '请输入当前登录密码以确认' }
    return
  }
  busy.value = true
  try {
    await reauth(f.password)
    f.password = ''
    if (enabled.value) {
      await totpDisable()
      await refreshMe()
      reset()
    } else {
      setup.value = await totpSetup()
      step.value = 'scan'
    }
  } catch (e) {
    fail(e, 'password')
  } finally {
    busy.value = false
  }
}

async function confirmCode() {
  errors.value = {}
  if (!/^\d{6}$/.test(f.code.replace(/\s/g, ''))) {
    errors.value = { code: '请输入认证器显示的 6 位数字' }
    return
  }
  busy.value = true
  try {
    codes.value = (await totpEnable(f.code.replace(/\s/g, ''))).recovery_codes
    f.code = ''
    setup.value = null
    step.value = 'codes'
    await refreshMe()
  } catch (e) {
    fail(e, 'code')
    // 设置已过期（409）：回到开始
    if (e instanceof ApiError && e.status === 409) step.value = 'idle'
  } finally {
    busy.value = false
  }
}

async function copyCodes() {
  try {
    await navigator.clipboard.writeText(codes.value.join('\n'))
    copied.value = true
  } catch {
    /* 剪贴板不可用时用户可以手动选择复制 */
  }
}

function savedCodes() {
  codes.value = []
  copied.value = false
  reset()
}
</script>

<template>
  <section class="panel tfa">
    <div class="head">
      <h2>两步验证</h2>
      <span class="tag" :class="enabled ? 'on' : 'off'">{{ enabled ? '已开启' : '未开启' }}</span>
    </div>

    <!-- 恢复码：只显示这一次 -->
    <template v-if="step === 'codes'">
      <p class="banner warn">两步验证已开启。请立即保存下面的恢复码：手机丢失时每个可以代替验证码登录一次，关闭此提示后不再显示。</p>
      <ol class="codes num">
        <li v-for="c in codes" :key="c">{{ c }}</li>
      </ol>
      <div class="actions">
        <button type="button" class="secondary" @click="copyCodes">{{ copied ? '已复制' : '复制全部' }}</button>
        <button type="button" @click="savedCodes">我已保存</button>
      </div>
    </template>

    <template v-else-if="step === 'scan' && setup">
      <p class="small muted">用认证器 App（Google Authenticator、Microsoft Authenticator、1Password 等）扫描二维码，然后输入它显示的 6 位数字。</p>
      <div class="scan">
        <QrCode :value="setup.otpauth_uri" label="两步验证二维码" class="qr" />
        <div class="manual small">
          <span class="muted">无法扫码时手动输入密钥：</span>
          <code class="secret">{{ groupedSecret }}</code>
          <span class="muted">类型：基于时间，6 位，30 秒</span>
        </div>
      </div>
      <form class="step-form" novalidate @submit.prevent="confirmCode">
        <label class="field">
          <span class="small muted">验证码</span>
          <input v-model="f.code" class="num" inputmode="numeric" autocomplete="one-time-code" maxlength="7" placeholder="123456" />
          <small v-if="errors.code" class="bad">{{ errors.code }}</small>
        </label>
        <div class="actions">
          <button type="submit" :disabled="busy">{{ busy ? '确认中…' : '确认开启' }}</button>
          <button type="button" class="text" @click="reset">取消</button>
        </div>
      </form>
    </template>

    <form v-else-if="step === 'password'" class="step-form" novalidate @submit.prevent="confirmPassword">
      <label class="field">
        <span class="small muted">{{ enabled ? '输入当前密码以关闭两步验证' : '输入当前密码以继续' }}</span>
        <input v-model="f.password" type="password" autocomplete="current-password" />
        <small v-if="errors.password" class="bad">{{ errors.password }}</small>
      </label>
      <div class="actions">
        <button type="submit" :class="{ danger: enabled }" :disabled="busy">{{ busy ? '处理中…' : enabled ? '关闭两步验证' : '继续' }}</button>
        <button type="button" class="text" @click="reset">取消</button>
      </div>
    </form>

    <template v-else>
      <p v-if="enabled" class="small muted">
        登录时在密码之后还要输入认证器的验证码。剩余恢复码 <strong class="num" :class="{ bad: left <= 3 }">{{ left }}</strong> 个<template
          v-if="left <= 3">：快用完了，可以关闭后重新开启，生成新的一组</template>。
      </p>
      <p v-else class="small muted">开启后，登录时在密码之后还要输入手机认证器 App 的 6 位验证码；密码泄露也无法登录。只影响 Web 登录，App 与 Agent 不受影响。</p>
      <div class="actions">
        <button type="button" :class="enabled ? 'secondary' : ''" @click="step = 'password'">{{ enabled ? '关闭…' : '开启两步验证' }}</button>
      </div>
    </template>
  </section>
</template>

<style scoped>
.tfa { max-width: 420px; margin: var(--space-4) auto 0; display: flex; flex-direction: column; gap: var(--space-3); }
.head { display: flex; align-items: center; gap: var(--space-2); }
.tfa h2 { margin: 0; font-size: var(--font-lg); }
.tfa p { margin: 0; }
.tag { font-size: var(--font-xs); padding: 1px var(--space-2); border-radius: var(--radius-full); }
.tag.on { background: color-mix(in srgb, var(--ok) 12%, transparent); color: var(--ok); }
.tag.off { background: var(--surface-2); color: var(--text-muted); }
.scan { display: flex; gap: var(--space-4); align-items: center; flex-wrap: wrap; }
.qr { width: 160px; height: 160px; flex-shrink: 0; }
.manual { display: flex; flex-direction: column; gap: var(--space-1); min-width: 0; flex: 1 1 160px; }
.secret { word-break: break-all; font-size: var(--font-md); }
.step-form { display: flex; flex-direction: column; gap: var(--space-3); }
.field { display: flex; flex-direction: column; gap: var(--space-1); }
.actions { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; }
.codes { margin: 0; padding: var(--space-3) var(--space-3) var(--space-3) var(--space-7); background: var(--surface-2);
  border-radius: var(--radius-sm); columns: 2; column-gap: var(--space-6); font-size: var(--font-md); }
.bad { color: var(--bad); }
</style>
