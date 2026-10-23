<script setup lang="ts">
// 账号：修改密码（设计 17.4）、两步验证（修订第 160 条）、登录中的设备（设计 24.8）、API 密钥与查看者账号（修订第 161 条）。
// 查看者只看到前三项。在“系统 → 账号”中显示（修订第 158 条）；
// 使用初始 / 重置密码登录时，账号页（/account）只显示修改密码，修改后才能使用其他页面。
// 修改成功后，该账号在其他浏览器上的会话全部失效。
// 版式（修订第 207 条）：宽屏两列——左列“个人与安全”（账号与密码、两步验证、登录中的设备），右列“访问与共享”
// （API Key、查看者账号）；各卡片用 SettingsCard，与“网站配置”一致。
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, changePassword, errorText } from '../api'
import Icon from '../components/Icon.vue'
import SettingsCard from '../components/SettingsCard.vue'
import ApiKeysPanel from '../components/ApiKeysPanel.vue'
import SessionsPanel from '../components/SessionsPanel.vue'
import TwoFactorPanel from '../components/TwoFactorPanel.vue'
import UsersPanel from '../components/UsersPanel.vue'
import { t } from '../i18n'
import { isViewer, passwordChanged, state } from '../store'

const router = useRouter()
const f = reactive({ current: '', next: '', confirm: '' })
const errors = ref<Record<string, string>>({})
const message = ref('')
const busy = ref(false)
const forced = state.me?.must_change_password ?? false
// 修改密码表单默认收起，点击后展开；使用初始密码时直接展开（修订第 179 条）
const editing = ref(forced)
const initial = (state.me?.username ?? '?').slice(0, 1).toUpperCase()

async function submit() {
  errors.value = {}
  message.value = ''
  if (f.next !== f.confirm) {
    errors.value = { confirm: t('两次输入的新密码不一致') }
    return
  }
  busy.value = true
  try {
    await changePassword(f.current, f.next)
    f.current = f.next = f.confirm = ''
    const wasForced = state.me?.must_change_password
    passwordChanged()
    if (wasForced) router.replace('/')
    else message.value = t('密码已修改，其他设备上的登录已退出')
  } catch (e) {
    if (e instanceof ApiError && e.details.length) {
      errors.value = Object.fromEntries(e.details.map((d) => [d.field === 'new_password' ? 'next' : d.field === 'current_password' ? 'current' : d.field, d.message]))
    } else {
      message.value = errorText(e)
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="account-wrap" :class="{ forced }">
    <div class="col">
      <h2 v-if="!forced" class="col-title">{{ t('个人与安全') }}</h2>
      <!-- 账号与密码：头像、用户名、角色；修改密码在卡片内展开 -->
      <section class="panel profile">
        <div class="who">
          <span class="avatar" aria-hidden="true">{{ initial }}</span>
          <div class="who-main">
            <strong class="name">{{ state.me?.username }}</strong>
            <span class="pill-row">
              <span class="pill accent">{{ isViewer ? t('查看者（只读）') : t('管理员') }}</span>
              <span class="pill" :class="{ on: state.me?.totp_enabled }">
                {{ state.me?.totp_enabled ? t('两步验证已开启') : t('两步验证未开启') }}
              </span>
            </span>
          </div>
        </div>
        <div v-if="!editing" class="pw-row">
          <Icon name="lock" :size="16" class="muted" />
          <div class="pw-text">
            <span>{{ t('登录密码') }}</span>
            <span class="small muted">{{ t('修改后其他设备上的登录会退出') }}</span>
          </div>
          <button type="button" class="secondary small" @click="editing = true">{{ t('修改密码') }}</button>
        </div>
        <form v-else class="pw-form" novalidate @submit.prevent="submit">
          <p v-if="forced" class="banner warn">{{ t('当前使用的是初始密码，请先设置自己的密码。') }}</p>
          <label class="field">
            <span class="small muted">{{ t('当前密码') }}</span>
            <input v-model="f.current" type="password" autocomplete="current-password" />
            <small v-if="errors.current" class="bad">{{ errors.current }}</small>
          </label>
          <label class="field">
            <span class="small muted">{{ t('新密码') }}<span class="hint">{{ t('至少 8 位') }}</span></span>
            <input v-model="f.next" type="password" autocomplete="new-password" minlength="8" />
            <small v-if="errors.next" class="bad">{{ errors.next }}</small>
          </label>
          <label class="field">
            <span class="small muted">{{ t('确认新密码') }}</span>
            <input v-model="f.confirm" type="password" autocomplete="new-password" />
            <small v-if="errors.confirm" class="bad">{{ errors.confirm }}</small>
          </label>
          <div class="actions">
            <button type="submit" :disabled="busy">{{ busy ? t('保存中…') : t('修改密码') }}</button>
            <button v-if="!forced" type="button" class="text" @click="editing = false">{{ t('取消') }}</button>
          </div>
        </form>
        <p v-if="message" class="small msg" role="status">{{ message }}</p>
      </section>
      <TwoFactorPanel v-if="!forced" />
      <SessionsPanel v-if="!forced" />
    </div>
    <div v-if="!forced && !isViewer" class="col">
      <h2 class="col-title">{{ t('访问与共享') }}</h2>
      <ApiKeysPanel />
      <UsersPanel />
      <p class="account-links small muted">
        <Icon name="shield" :size="14" />
        <span>{{ t('发现异常登录？查看') }}<RouterLink to="/logs?tab=login">{{ t('登录日志') }}</RouterLink>{{ t('，修改密码后其他设备会被退出。') }}</span>
      </p>
    </div>
  </div>
</template>

<style scoped>
.account-wrap { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-5); align-items: start; }
.account-wrap.forced { grid-template-columns: minmax(0, 440px); justify-content: center; }
.col { display: flex; flex-direction: column; gap: var(--space-4); min-width: 0; }
.col-title { margin: 0; font-size: var(--font-sm); font-weight: var(--weight-strong); color: var(--text-muted); letter-spacing: .04em; }
@media (max-width: 960px) {
  .account-wrap { grid-template-columns: minmax(0, 1fr); }
}

/* 账号与密码 */
.profile { padding: 0; overflow: hidden; }
.who { display: flex; align-items: center; gap: var(--space-4); padding: var(--space-5); }
.avatar { flex: none; width: 52px; height: 52px; border-radius: 50%; display: grid; place-items: center; font-size: 22px;
  font-weight: var(--weight-strong); color: var(--on-accent); background: linear-gradient(135deg, var(--accent), color-mix(in srgb, var(--accent) 55%, var(--series-2))); }
.who-main { display: flex; flex-direction: column; gap: var(--space-1); min-width: 0; }
.name { font-size: var(--font-lg); line-height: var(--line-lg); overflow: hidden; text-overflow: ellipsis; }
.pill-row { display: flex; gap: var(--space-2); flex-wrap: wrap; }
.pw-row { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-3) var(--space-5); border-top: 1px solid var(--border); }
.pw-text { flex: 1; display: flex; flex-direction: column; min-width: 0; }
.pw-form { display: flex; flex-direction: column; gap: var(--space-3); padding: var(--space-4) var(--space-5); border-top: 1px solid var(--border); }
.pw-form .banner { margin: 0; }
.field { display: flex; flex-direction: column; gap: var(--space-1); }
.hint { margin-left: var(--space-2); opacity: .8; }
.actions { display: flex; gap: var(--space-2); align-items: center; }
.msg { margin: 0; padding: 0 var(--space-5) var(--space-4); }
.bad { color: var(--bad); }
.account-links { margin: 0; display: flex; gap: var(--space-2); align-items: flex-start; }
.account-links :deep(svg) { flex: none; margin-top: 2px; }
@media (max-width: 600px) {
  .who { padding: var(--space-4); }
  .pw-row, .pw-form { padding-left: var(--space-4); padding-right: var(--space-4); }
}
</style>
