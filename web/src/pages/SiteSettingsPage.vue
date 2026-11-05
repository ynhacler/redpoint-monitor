<script setup lang="ts">
// 系统 → 网站配置（设计 52，修订第 201 条）：应用名称、界面语言与登录验证码，以及监听端口（设计 51）。
// 保存后立即生效：语言与名称在本页面马上切换，其他已打开的浏览器在刷新后生效。
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ApiError, errorText, getStatusPage, putSite, putStatusPage } from '../api'
import Flag from '../components/Flag.vue'
import ListenSettings from '../components/ListenSettings.vue'
import SaveState, { saveStatus } from '../components/SaveState.vue'
import { localeNames, t, type Locale } from '../i18n'
import { applySite, site } from '../site'
import { state } from '../store'

const form = reactive({ app_name: site.app_name, language: site.language as Locale, captcha: site.captcha })
watch(() => [site.app_name, site.language, site.captcha], () => Object.assign(form, { app_name: site.app_name, language: site.language, captcha: site.captcha }))
const fieldErrors = ref<Record<string, string>>({})
const error = ref('')
const saving = ref(false)
const saved = ref(false)
const dirty = computed(() => form.app_name !== site.app_name || form.language !== site.language || form.captcha !== site.captcha)
const savedAt = ref(0)
// 保存状态（修订第 229 条）；字段错误也算保存失败
const saveError = computed(() => error.value || (Object.keys(fieldErrors.value).length ? t('请检查标出的项') : ''))
const status = computed(() => saveStatus({ saving: saving.value, error: saveError.value, dirty: dirty.value, saved: saved.value }))

async function save() {
  saving.value = true
  saved.value = false
  error.value = ''
  fieldErrors.value = {}
  try {
    applySite(await putSite({ ...form }))
    saved.value = true
    savedAt.value = Date.now()
  } catch (e) {
    if (e instanceof ApiError && e.details.length) fieldErrors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
    else error.value = errorText(e, t('保存失败'))
  } finally {
    saving.value = false
  }
}

// 公开展示页（设计 45.3）：默认关闭，节点逐个选择
const statusUrl = `${location.origin}/status`
const pub = reactive({ enabled: false, ids: [] as number[] })
const pubSaved = ref({ enabled: false, ids: '' })
const pubKey = () => [...pub.ids].sort((a, b) => a - b).join()
const pubDirty = computed(() => pub.enabled !== pubSaved.value.enabled || pubKey() !== pubSaved.value.ids)
const pubError = ref('')
const pubSaving = ref(false)
const pubDone = ref(false)
const pubAt = ref(0)
const pubStatus = computed(() => saveStatus({ saving: pubSaving.value, error: pubError.value, dirty: pubDirty.value, saved: pubDone.value }))
function applyPub(v: { enabled: boolean; server_ids: number[] }) {
  Object.assign(pub, { enabled: v.enabled, ids: [...v.server_ids] })
  pubSaved.value = { enabled: v.enabled, ids: pubKey() }
}
onMounted(async () => {
  try {
    applyPub(await getStatusPage())
  } catch (e) {
    pubError.value = errorText(e, t('读取失败'))
  }
})
async function savePub() {
  pubSaving.value = true
  pubDone.value = false
  pubError.value = ''
  try {
    applyPub(await putStatusPage({ enabled: pub.enabled, server_ids: pub.ids }))
    pubDone.value = true
    pubAt.value = Date.now()
  } catch (e) {
    pubError.value = errorText(e, t('保存失败'))
  } finally {
    pubSaving.value = false
  }
}
</script>

<template>
  <main class="page">
    <div class="page-head"><h1>{{ t('网站配置') }}</h1></div>

    <form class="panel card" novalidate @submit.prevent="save">
      <div class="card-head">
        <div>
          <h2>{{ t('基本设置') }}</h2>
          <p class="small muted">{{ t('应用名称、界面语言与登录验证码，保存后立即生效') }}</p>
        </div>
        <div class="save">
          <SaveState :state="status" :error="saveError || undefined" :saved-at="savedAt" />
          <button type="submit" :disabled="saving || !dirty" :title="!dirty ? t('没有修改') : undefined">{{ saving ? t('保存中…') : t('保存配置') }}</button>
        </div>
      </div>

      <div class="field">
        <label for="app-name">{{ t('应用名称') }}</label>
        <p class="small muted">{{ t('显示在顶栏、登录页与浏览器标签中') }}</p>
        <input id="app-name" v-model="form.app_name" maxlength="32" />
        <small v-if="fieldErrors.app_name" class="bad">{{ fieldErrors.app_name }}</small>
      </div>

      <div class="field">
        <label for="lang">{{ t('语言') }}</label>
        <p class="small muted">{{ t('界面显示的语言，所有账号与浏览器一致') }}</p>
        <select id="lang" v-model="form.language">
          <option v-for="(n, k) in localeNames" :key="k" :value="k">{{ n }}</option>
        </select>
        <small v-if="form.language === 'en'" class="muted">{{ t('英文翻译正在分批完成，暂未翻译的文字显示中文') }}</small>
      </div>

      <div class="field">
        <span class="label">{{ t('登录验证码') }}</span>
        <p class="small muted">{{ t('登录时需要拖动滑块完成验证；关闭后登录限速仍然有效') }}</p>
        <label class="switch">
          <input v-model="form.captcha" type="checkbox" role="switch" :disabled="site.captcha_forced_off" />
          <span class="track" aria-hidden="true" />
          <span>{{ form.captcha ? t('已启用') : t('已关闭') }}</span>
        </label>
        <small v-if="site.captcha_forced_off" class="muted">{{ t('启动参数 --no-login-captcha 已关闭验证码，这里不能打开') }}</small>
      </div>

    </form>

    <form id="status-page" class="panel card" novalidate @submit.prevent="savePub">
      <div class="card-head">
        <div>
          <h2>{{ t('公开展示页') }}</h2>
          <p class="small muted">{{ t('不用登录就能看的大屏，适合放在电视或副屏上') }}</p>
        </div>
        <div class="save">
          <SaveState :state="pubStatus" :error="pubError || undefined" :saved-at="pubAt" />
          <button type="submit" :disabled="pubSaving || !pubDirty" :title="!pubDirty ? t('没有修改') : undefined">{{ pubSaving ? t('保存中…') : t('保存配置') }}</button>
        </div>
      </div>

      <div class="field">
        <span class="label">{{ t('开启展示页') }}</span>
        <p class="small muted">{{ t('任何知道地址的人都能打开。只显示名称、国家、在线状态、资源使用率、网速、运行时间和流量比例，不显示 IP、主机名、服务商、价格和到期时间') }}</p>
        <label class="switch">
          <input v-model="pub.enabled" type="checkbox" role="switch" />
          <span class="track" aria-hidden="true" />
          <span>{{ pub.enabled ? t('已启用') : t('已关闭') }}</span>
        </label>
        <a v-if="pubSaved.enabled" class="small" href="/status" target="_blank" rel="noopener">{{ statusUrl }}</a>
      </div>

      <div class="field">
        <span class="label">{{ t('公开的节点') }}（{{ pub.ids.length }} / {{ state.servers.length }}）</span>
        <p class="small muted">{{ t('节点名称会公开显示，其中的 IP、邮箱和电话号码显示为 ***；域名等其他内容不会遮盖，请先检查名称') }}</p>
        <div class="pick-actions">
          <button type="button" class="ghost" @click="pub.ids = state.servers.map((s) => s.id)">{{ t('全选') }}</button>
          <button type="button" class="ghost" @click="pub.ids = []">{{ t('全不选') }}</button>
        </div>
        <div class="picks">
          <label v-for="s in state.servers" :key="s.id"><input v-model="pub.ids" type="checkbox" :value="s.id" /> <Flag :code="s.country" /> <span>{{ s.name }}</span></label>
        </div>
      </div>
    </form>

    <section class="listen-wrap">
      <h2 id="listen">{{ t('监听端口') }}</h2>
      <ListenSettings />
    </section>
  </main>
</template>

<style scoped>
.save { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; justify-content: flex-end; }
.card { padding: 0; margin-bottom: var(--space-6); }
.card-head { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-3); padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--border); }
.card-head h2 { margin: 0; }
.card-head p { margin: 2px 0 0; }
.field { display: flex; flex-direction: column; gap: var(--space-1); padding: var(--space-4) var(--space-5); }
.field + .field { border-top: 1px solid var(--border); }
.field > label, .field > .label { font-weight: var(--weight-strong); }
.field p { margin: 0 0 var(--space-1); }
.field input:not([type=checkbox]), .field select { max-width: 480px; }
.card > p { margin: 0; padding: 0 var(--space-5) var(--space-4); }
.ok { color: var(--ok); }
.bad { color: var(--bad); }
/* 开关：用原生复选框，键盘与读屏照常可用 */
.switch { display: inline-flex; align-items: center; gap: var(--space-2); cursor: pointer; align-self: flex-start; }
.switch input { position: absolute; opacity: 0; width: 1px; height: 1px; }
.switch .track { width: 44px; height: 24px; border-radius: 12px; background: var(--track); position: relative; transition: background .15s; flex: none; }
.switch .track::after { content: ''; position: absolute; top: 3px; left: 3px; width: 18px; height: 18px; border-radius: 50%; background: var(--surface);
  box-shadow: 0 1px 2px rgb(0 0 0 / .2); transition: transform .15s; }
.switch input:checked + .track { background: var(--accent); }
.switch input:checked + .track::after { transform: translateX(20px); }
.switch input:focus-visible + .track { outline: 2px solid var(--accent); outline-offset: 2px; }
.switch input:disabled + .track { opacity: .5; }
.listen-wrap h2 { margin-top: 0; }
.pick-actions { display: flex; gap: var(--space-2); }
.picks { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: var(--space-1) var(--space-3); margin-top: var(--space-1); }
.picks label { display: flex; align-items: center; gap: var(--space-2); min-width: 0; min-height: 28px; cursor: pointer; }
.picks span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
@media (max-width: 600px) {
  .card-head, .field { padding: var(--space-3) var(--space-4); }
}
</style>
