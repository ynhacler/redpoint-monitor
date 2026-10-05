<script setup lang="ts">
// 只读 API Key（设计 45.2）：供脚本、Grafana、Home Assistant 等读取节点、指标、流量与告警。
// 【安全】创建前重新输入密码；完整 Key 只在创建后显示一次，之后只显示末 4 位。
import { computed, onMounted, ref } from 'vue'
import { ApiError, createAPIKey, errorText, listAPIKeys, reauth, revokeAPIKey, type APIKey, type APIKeyInput } from '../api'
import { DASH, fmtDateTime, fmtTime } from '../format'
import { state } from '../store'
import { scopeText } from '../scope'
import CommandBlock from './CommandBlock.vue'

const keys = ref<APIKey[]>([])
const error = ref('')
async function load() {
  try {
    keys.value = await listAPIKeys()
    error.value = ''
  } catch (e) {
    error.value = errorText(e, 'API Key 加载失败')
  }
}
onMounted(load)

// ---- 创建 ----
const adding = ref(false)
const form = ref({ name: '', scope_type: 'all' as APIKeyInput['scope_type'], group: '', server_ids: [] as number[], expires: 0, password: '' })
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)
const created = ref('') // 刚创建的完整 Key，只显示这一次

const groups = computed(() => [...new Set(state.servers.map((s) => s.group).filter(Boolean))].sort())
const servers = computed(() => [...state.servers].sort((a, b) => a.name.localeCompare(b.name)))

function openNew() {
  adding.value = true
  created.value = ''
  form.value = { name: '', scope_type: 'all', group: groups.value[0] ?? '', server_ids: [], expires: 0, password: '' }
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
    const r = await createAPIKey({
      name: f.name.trim(), scope_type: f.scope_type, expires_in_days: f.expires,
      group: f.scope_type === 'group' ? f.group : undefined,
      server_ids: f.scope_type === 'servers' ? f.server_ids : undefined,
    })
    created.value = r.key
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

async function revoke(k: APIKey) {
  if (!confirm(`吊销 API Key“${k.name}”？使用它的脚本将立即无法访问。`)) return
  try {
    await revokeAPIKey(k.id)
    await load()
  } catch (e) {
    error.value = errorText(e)
  }
}

// 示例：用 curl 读取节点列表（Key 只放在 Authorization 头中，不放在网址里）
const example = computed(() => `curl -H "Authorization: Bearer ${created.value}" ${location.origin}/api/v1/servers`)
</script>

<template>
  <section class="panel apikeys">
    <div class="head">
      <h2>API Key</h2>
      <button v-if="!adding" type="button" class="secondary small" @click="openNew">创建</button>
    </div>
    <p class="small muted">
      只读：供脚本、Grafana、Home Assistant 读取节点、历史指标、流量与告警；不能修改任何设置。
      放在请求头 <code>Authorization: Bearer api_…</code> 中使用，每分钟最多 120 次。
    </p>
    <p v-if="error" class="small bad">{{ error }}</p>

    <!-- 刚创建：完整 Key 只显示这一次 -->
    <div v-if="created" class="created">
      <p class="small"><b>请立即复制保存</b>：离开本页后无法再次查看完整 Key。</p>
      <CommandBlock :command="created" />
      <p class="small muted">示例：</p>
      <CommandBlock :command="example" />
    </div>

    <form v-if="adding" class="form" novalidate @submit.prevent="save">
      <label>名称
        <input v-model="form.name" maxlength="64" placeholder="如 Grafana" />
        <small v-if="fieldErrors.name" class="bad">{{ fieldErrors.name }}</small>
      </label>
      <label>可访问的节点
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
      <fieldset v-if="form.scope_type === 'servers'" class="servers">
        <legend class="small">节点</legend>
        <label v-for="s in servers" :key="s.id" class="check small">
          <input v-model="form.server_ids" type="checkbox" :value="s.id" />{{ s.name }}
        </label>
        <small v-if="fieldErrors.server_ids" class="bad">{{ fieldErrors.server_ids }}</small>
      </fieldset>
      <label>有效期
        <select v-model.number="form.expires">
          <option :value="0">不过期</option>
          <option :value="30">30 天</option>
          <option :value="90">90 天</option>
          <option :value="365">1 年</option>
        </select>
      </label>
      <label>当前登录密码
        <input v-model="form.password" type="password" autocomplete="current-password" />
        <small v-if="fieldErrors.password" class="bad">{{ fieldErrors.password }}</small>
      </label>
      <p v-if="fieldErrors.form" class="small bad">{{ fieldErrors.form }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving">{{ saving ? '创建中…' : '创建' }}</button>
        <button type="button" class="secondary" @click="adding = false">取消</button>
      </div>
    </form>

    <ul v-if="keys.length" class="list">
      <li v-for="k in keys" :key="k.id" :class="{ off: k.revoked_at || (k.expires_at && k.expires_at * 1000 < Date.now()) }">
        <div class="who">
          <strong>{{ k.name }}</strong> <span class="small muted mono">api_…{{ k.hint }}</span>
          <span v-if="k.revoked_at" class="tag">已吊销</span>
          <span v-else-if="k.expires_at && k.expires_at * 1000 < Date.now()" class="tag">已过期</span>
          <div class="small muted">
            {{ scopeText(k.scope_type, k.scope_value) }} · 最近使用 {{ k.last_used_at ? fmtTime(k.last_used_at) : '从未' }}
            · 创建于 {{ fmtDateTime(k.created_at) }}{{ k.expires_at ? ` · ${fmtDateTime(k.expires_at)} 过期` : '' }}
          </div>
        </div>
        <button v-if="!k.revoked_at" type="button" class="secondary small" @click="revoke(k)">吊销</button>
      </li>
    </ul>
    <p v-else-if="!adding" class="small muted empty">{{ DASH }} 还没有 API Key</p>
  </section>
</template>

<style scoped>
.apikeys { max-width: 420px; margin: var(--space-4) auto 0; }
.head { display: flex; align-items: center; justify-content: space-between; margin-bottom: var(--space-1); }
.apikeys h2 { margin: 0; font-size: var(--font-lg); }
.apikeys p { margin: var(--space-1) 0 0; }
.created { margin-top: var(--space-3); display: flex; flex-direction: column; gap: var(--space-2); }
.form { display: flex; flex-direction: column; gap: var(--space-3); margin-top: var(--space-3); }
.form label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.servers { border: 1px solid var(--border); border-radius: var(--radius-sm); padding: var(--space-2) var(--space-3);
  max-height: 200px; overflow-y: auto; display: flex; flex-direction: column; gap: var(--space-1); }
.check { flex-direction: row !important; align-items: center; gap: var(--space-2) !important; }
.check input { width: auto; }
.actions { display: flex; gap: var(--space-2); }
.list { list-style: none; margin: var(--space-3) 0 0; padding: 0; }
.list li { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); padding: var(--space-3) 0; }
.list li + li { border-top: 1px solid var(--border); }
.list li.off { opacity: .6; }
.who { min-width: 0; }
.mono { font-family: var(--font-mono); }
.tag { margin-left: var(--space-2); font-size: var(--font-xs); color: var(--text-muted); border: 1px solid var(--border);
  border-radius: var(--radius-sm); padding: 0 var(--space-1); }
.empty { margin-top: var(--space-3); }
</style>
