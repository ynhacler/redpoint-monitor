<script setup lang="ts">
// App 接入（设计 8.4）：创建配对 AK、查看与吊销已连接的设备。
// 【安全】AK 只用于首次配对：默认一次性、1 台设备、1 天内有效；完整 AK 只在创建后显示一次。
// App 永远只读，最多允许静音与维护（设计 8.4.1）；所有配置变更留在 Web。
import { computed, onMounted, ref } from 'vue'
import {
  ApiError, createAppKey, errorText, listAppDevices, listAppKeys, reauth, revokeAppDevice, revokeAppKey,
  type AppAccessKey, type AppAccessKeyInput, type AppDevice,
} from '../api'
import CommandBlock from '../components/CommandBlock.vue'
import { DASH, fmtDateTime, fmtTime } from '../format'
import { scopeText } from '../scope'
import { state } from '../store'

const keys = ref<AppAccessKey[]>([])
const devices = ref<AppDevice[]>([])
const error = ref('')
async function load() {
  try {
    ;[keys.value, devices.value] = await Promise.all([listAppKeys(), listAppDevices()])
    error.value = ''
  } catch (e) {
    error.value = errorText(e, 'App 接入信息加载失败')
  }
}
onMounted(load)

// ---- 创建 AK ----
const adding = ref(false)
const form = ref({
  name: '', scope_type: 'all' as NonNullable<AppAccessKeyInput['scope_type']>, group: '', server_ids: [] as number[],
  allow_low_risk_ops: true, max_devices: 1, expires_in_days: 1, password: '',
})
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)
/** 刚创建的 AK：完整值只显示这一次 */
const created = ref<{ access_key: string; server_url: string; pair_url: string } | null>(null)

const groups = computed(() => [...new Set(state.servers.map((s) => s.group).filter(Boolean))].sort())
const servers = computed(() => [...state.servers].sort((a, b) => a.name.localeCompare(b.name)))

function openNew() {
  adding.value = true
  created.value = null
  form.value = { name: '', scope_type: 'all', group: groups.value[0] ?? '', server_ids: [], allow_low_risk_ops: true,
    max_devices: 1, expires_in_days: 1, password: '' }
  fieldErrors.value = {}
}

async function save() {
  const f = form.value
  fieldErrors.value = {}
  if (!f.password) {
    fieldErrors.value = { password: '请输入当前登录密码以确认' }
    return
  }
  saving.value = true
  try {
    await reauth(f.password)
    const r = await createAppKey({
      name: f.name.trim(), scope_type: f.scope_type, allow_low_risk_ops: f.allow_low_risk_ops,
      max_devices: f.max_devices, expires_in_days: f.expires_in_days,
      group: f.scope_type === 'group' ? f.group : undefined,
      server_ids: f.scope_type === 'servers' ? f.server_ids : undefined,
    })
    created.value = { access_key: r.access_key, server_url: r.server_url, pair_url: r.pair_url }
    adding.value = false
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.details.length) fieldErrors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
    else fieldErrors.value = { form: errorText(e) }
  } finally {
    f.password = ''
    saving.value = false
  }
}

// 面板地址不是 https 时 App 会拒绝连接（设计 12.4），提前提示
const insecure = computed(() => !!created.value && !created.value.server_url.startsWith('https://'))

// ---- 吊销 ----
const statusText: Record<string, string> = { active: '可用', used: '已用完', expired: '已过期', revoked: '已吊销' }

async function revokeKey(k: AppAccessKey) {
  if (!confirm(`吊销 AK“${k.name}”？之后不能再用它配对。`)) return
  let withDevices = false
  const active = devices.value.filter((d) => d.access_key_id === k.id && d.status === 'active').length
  if (active > 0) {
    withDevices = confirm(`用这个 AK 配对的 ${active} 台设备是否一并吊销？\n确定：一并吊销（这些设备需要重新配对）\n取消：只吊销 AK，设备照常使用`)
  }
  try {
    await revokeAppKey(k.id, withDevices)
    await load()
  } catch (e) {
    error.value = errorText(e)
  }
}

async function revokeDevice(d: AppDevice) {
  if (!confirm(`吊销设备“${d.name}”？该设备将立即退出，需要重新生成 AK 配对。`)) return
  try {
    await revokeAppDevice(d.id)
    await load()
  } catch (e) {
    error.value = errorText(e)
  }
}

const platformName: Record<string, string> = { ios: 'iOS', android: 'Android' }
const revokedByText: Record<string, string> = {
  admin: '管理员吊销', app: '设备自行解除', access_key: '随 AK 吊销', refresh_reuse: '凭证重复使用，已自动吊销',
}
const activeDevices = computed(() => devices.value.filter((d) => d.status === 'active').length)
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>App 接入</h1>
      <button v-if="!adding" type="button" @click="openNew">创建配对 AK</button>
    </div>
    <p class="muted small intro">
      App 不使用面板的用户名和密码。在这里创建一次性的配对 AK，在 App 中扫码或填写面板地址与 AK 即可连接。
      App 只能查看授权范围内的节点、指标、流量与告警；创建时可允许它静音告警、开启维护模式，其他设置只能在 Web 中修改。
    </p>
    <p v-if="error" class="banner">{{ error }}</p>

    <!-- 刚创建：完整 AK 只显示这一次 -->
    <section v-if="created" class="panel created">
      <h2>配对信息</h2>
      <p class="small"><b>请立即在 App 中完成配对</b>：离开本页后无法再次查看完整 AK。</p>
      <p v-if="insecure" class="banner warn small">
        面板地址不是 https，App 会拒绝连接。请为面板配置 HTTPS（如用 Caddy 反向代理），并以 --public-url 启动面板。
      </p>
      <div class="pair">
        <span class="small muted">面板地址</span>
        <CommandBlock :command="created.server_url" plain />
        <span class="small muted">AK</span>
        <CommandBlock :command="created.access_key" plain />
        <span class="small muted">配对链接（二维码内容）</span>
        <CommandBlock :command="created.pair_url" plain />
      </div>
    </section>

    <form v-if="adding" class="panel editor" novalidate @submit.prevent="save">
      <h2>创建配对 AK</h2>
      <div class="fields">
        <label>名称
          <input v-model="form.name" maxlength="64" placeholder="如 Tom 的 iPhone" />
          <small v-if="fieldErrors.name" class="bad">{{ fieldErrors.name }}</small>
        </label>
        <label>可查看的节点
          <select v-model="form.scope_type">
            <option value="all">全部节点</option>
            <option value="group" :disabled="!groups.length">指定分组</option>
            <option value="servers">指定节点</option>
          </select>
          <small v-if="fieldErrors.scope_type" class="bad">{{ fieldErrors.scope_type }}</small>
        </label>
        <label v-if="form.scope_type === 'group'">分组
          <select v-model="form.group">
            <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
          </select>
          <small v-if="fieldErrors.group" class="bad">{{ fieldErrors.group }}</small>
        </label>
        <fieldset v-if="form.scope_type === 'servers'" class="servers wide">
          <legend class="small">节点</legend>
          <label v-for="s in servers" :key="s.id" class="check small">
            <input v-model="form.server_ids" type="checkbox" :value="s.id" />{{ s.name }}
          </label>
          <small v-if="fieldErrors.server_ids" class="bad">{{ fieldErrors.server_ids }}</small>
        </fieldset>
        <label>最多配对设备
          <select v-model.number="form.max_devices">
            <option :value="1">1 台（推荐）</option>
            <option :value="2">2 台</option>
            <option :value="3">3 台</option>
            <option :value="5">5 台</option>
          </select>
          <small v-if="fieldErrors.max_devices" class="bad">{{ fieldErrors.max_devices }}</small>
        </label>
        <label>AK 有效期
          <select v-model.number="form.expires_in_days">
            <option :value="1">1 天（推荐）</option>
            <option :value="7">7 天</option>
            <option :value="30">30 天</option>
            <option :value="90">90 天</option>
          </select>
          <small v-if="fieldErrors.expires_in_days" class="bad">{{ fieldErrors.expires_in_days }}</small>
        </label>
        <label class="check wide">
          <input v-model="form.allow_low_risk_ops" type="checkbox" />允许静音告警、开启 / 结束维护模式（仅限可查看的节点）
        </label>
        <label>当前登录密码
          <input v-model="form.password" type="password" autocomplete="current-password" />
          <small v-if="fieldErrors.password" class="bad">{{ fieldErrors.password }}</small>
        </label>
      </div>
      <p class="small muted">AK 只用于首次配对；配对后设备使用独立的凭证，AK 过期不影响已配对的设备。</p>
      <p v-if="fieldErrors.form" class="small bad">{{ fieldErrors.form }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving">{{ saving ? '创建中…' : '创建' }}</button>
        <button type="button" class="secondary" @click="adding = false">取消</button>
      </div>
    </form>

    <section class="panel">
      <h2>已连接设备 <span class="muted small num">{{ activeDevices }}</span></h2>
      <ul v-if="devices.length" class="list">
        <li v-for="d in devices" :key="d.id" :class="{ off: d.status !== 'active' }">
          <div class="who">
            <strong>{{ d.name }}</strong>
            <span class="small muted"> {{ platformName[d.platform] ?? d.platform }}{{ d.app_version ? ` · App ${d.app_version}` : '' }}</span>
            <span v-if="d.status === 'revoked'" class="tag">{{ revokedByText[d.revoked_by] ?? '已吊销' }}</span>
            <span v-else-if="d.status === 'expired'" class="tag">90 天未使用，已失效</span>
            <div class="small muted">
              {{ scopeText(d.scope_type, d.scope_value) }} · {{ d.allow_low_risk_ops ? '只读 + 静音 / 维护' : '只读' }}
              · 最后在线 {{ d.last_seen_at ? fmtTime(d.last_seen_at) : DASH }} · 配对于 {{ fmtDateTime(d.paired_at) }}
              <template v-if="d.access_key_name"> · AK：{{ d.access_key_name }}</template>
            </div>
          </div>
          <button v-if="d.status === 'active'" type="button" class="secondary small" @click="revokeDevice(d)">吊销</button>
        </li>
      </ul>
      <p v-else class="small muted empty">{{ DASH }} 还没有设备。创建 AK 后在 App 中扫码或填写即可配对。</p>
    </section>

    <section class="panel">
      <h2>配对 AK</h2>
      <ul v-if="keys.length" class="list">
        <li v-for="k in keys" :key="k.id" :class="{ off: k.status !== 'active' }">
          <div class="who">
            <strong>{{ k.name }}</strong> <span class="small muted mono">{{ k.hint }}</span>
            <span class="tag" :class="{ live: k.status === 'active' }">{{ statusText[k.status] ?? k.status }}</span>
            <div class="small muted">
              {{ scopeText(k.scope_type, k.scope_value) }} · {{ k.allow_low_risk_ops ? '允许静音 / 维护' : '只读' }}
              · 已配对 {{ k.paired_devices }} / {{ k.max_devices }}
              · {{ k.status === 'active' ? `${fmtDateTime(k.expires_at)} 前有效` : `创建于 ${fmtDateTime(k.created_at)}` }}
            </div>
          </div>
          <button v-if="!k.revoked_at" type="button" class="secondary small" @click="revokeKey(k)">吊销</button>
        </li>
      </ul>
      <p v-else class="small muted empty">{{ DASH }} 还没有 AK</p>
    </section>
  </main>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-4); max-width: 72ch; }
.panel + .panel, .created + .panel, .editor + .panel { margin-top: var(--space-4); }
.panel h2 { font-size: var(--font-lg); margin-bottom: var(--space-2); }
.created p { margin: 0 0 var(--space-2); }
.pair { display: flex; flex-direction: column; gap: var(--space-1); min-width: 0; }
.pair .muted { margin-top: var(--space-2); }
.fields { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: var(--space-3); margin: var(--space-3) 0; }
.fields label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.fields .wide { grid-column: 1 / -1; }
.check { flex-direction: row !important; align-items: center; gap: var(--space-2) !important; }
.check input { width: auto; }
.servers { border: 1px solid var(--border); border-radius: var(--radius-sm); padding: var(--space-2) var(--space-3);
  max-height: 200px; overflow-y: auto; display: flex; flex-direction: column; gap: var(--space-1); }
.actions { display: flex; gap: var(--space-2); margin-top: var(--space-3); }
.list { list-style: none; margin: 0; padding: 0; }
.list li { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); padding: var(--space-3) 0; }
.list li + li { border-top: 1px solid var(--border); }
.list li.off { opacity: .6; }
.who { min-width: 0; overflow-wrap: anywhere; }
.mono { font-family: var(--font-mono); }
.tag { margin-left: var(--space-2); font-size: var(--font-xs); color: var(--text-muted); border: 1px solid var(--border);
  border-radius: var(--radius-sm); padding: 0 var(--space-1); white-space: nowrap; }
.tag.live { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 40%, var(--border)); }
.empty { margin: 0; }
</style>
