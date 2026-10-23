<script setup lang="ts">
// 只读 API Key（设计 45.2）：供脚本、Grafana、Home Assistant 等读取节点、指标、流量与告警。
// 【安全】创建前重新输入密码；完整 Key 只在创建后显示一次，之后只显示末 4 位。
import { computed, onMounted, ref } from 'vue'
import { ApiError, createAPIKey, errorText, listAPIKeys, reauth, revokeAPIKey, type APIKey, type APIKeyInput } from '../api'
import { fmtDateTime, fmtTime } from '../format'
import { t } from '../i18n'
import { state } from '../store'
import Icon from './Icon.vue'
import SettingsCard from './SettingsCard.vue'
import { scopeText } from '../scope'
import CommandBlock from './CommandBlock.vue'

const keys = ref<APIKey[]>([])
const error = ref('')
async function load() {
  try {
    keys.value = await listAPIKeys()
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t('API Key 加载失败'))
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
    fieldErrors.value = { password: t('请输入当前登录密码以确认') }
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
  if (!confirm(t('吊销 API Key“{name}”？使用它的脚本将立即无法访问。', { name: k.name }))) return
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
  <SettingsCard icon="key" title="API Key" class="apikeys">
    <template #desc>
      {{ t('只读：供脚本、Grafana、Home Assistant 读取节点、历史指标、流量与告警；不能修改任何设置。放在请求头') }}
      <code>Authorization: Bearer api_…</code>{{ t('中使用，每分钟最多 120 次。') }}
    </template>
    <template v-if="!adding" #actions>
      <button type="button" class="small" @click="openNew"><Icon name="plus" :size="14" />{{ t('创建') }}</button>
    </template>
    <p v-if="error" class="small bad">{{ error }}</p>

    <!-- 刚创建：完整 Key 只显示这一次 -->
    <div v-if="created" class="created">
      <p class="small"><b>{{ t('请立即复制保存') }}</b>{{ t('：离开本页后无法再次查看完整 Key。') }}</p>
      <CommandBlock :command="created" />
      <p class="small muted">{{ t('示例：') }}</p>
      <CommandBlock :command="example" />
    </div>

    <form v-if="adding" class="form" novalidate @submit.prevent="save">
      <label>{{ t('名称') }}
        <input v-model="form.name" maxlength="64" :placeholder="t('如 Grafana')" />
        <small v-if="fieldErrors.name" class="bad">{{ fieldErrors.name }}</small>
      </label>
      <label>{{ t('可访问的节点') }}
        <select v-model="form.scope_type">
          <option value="all">{{ t('全部节点') }}</option>
          <option value="group" :disabled="!groups.length">{{ t('指定分组') }}</option>
          <option value="servers">{{ t('指定节点') }}</option>
        </select>
        <small v-if="fieldErrors.scope_type" class="bad">{{ fieldErrors.scope_type }}</small>
      </label>
      <label v-if="form.scope_type === 'group'">{{ t('分组') }}
        <select v-model="form.group">
          <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
        </select>
        <small v-if="fieldErrors.group" class="bad">{{ fieldErrors.group }}</small>
      </label>
      <fieldset v-if="form.scope_type === 'servers'" class="servers">
        <legend class="small">{{ t('节点') }}</legend>
        <label v-for="s in servers" :key="s.id" class="check small">
          <input v-model="form.server_ids" type="checkbox" :value="s.id" />{{ s.name }}
        </label>
        <small v-if="fieldErrors.server_ids" class="bad">{{ fieldErrors.server_ids }}</small>
      </fieldset>
      <label>{{ t('有效期') }}
        <select v-model.number="form.expires">
          <option :value="0">{{ t('不过期') }}</option>
          <option :value="30">{{ t('30 天') }}</option>
          <option :value="90">{{ t('90 天') }}</option>
          <option :value="365">{{ t('1 年') }}</option>
        </select>
      </label>
      <label>{{ t('当前登录密码') }}
        <input v-model="form.password" type="password" autocomplete="current-password" />
        <small v-if="fieldErrors.password" class="bad">{{ fieldErrors.password }}</small>
      </label>
      <p v-if="fieldErrors.form" class="small bad">{{ fieldErrors.form }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving">{{ saving ? t('创建中…') : t('创建') }}</button>
        <button type="button" class="secondary" @click="adding = false">{{ t('取消') }}</button>
      </div>
    </form>

    <ul v-if="keys.length" class="sc-list">
      <li v-for="k in keys" :key="k.id" :class="{ off: k.revoked_at || (k.expires_at && k.expires_at * 1000 < Date.now()) }">
        <div class="who">
          <div class="who-line">
            <strong>{{ k.name }}</strong> <span class="small muted mono">api_…{{ k.hint }}</span>
            <span v-if="k.revoked_at" class="pill">{{ t('已吊销') }}</span>
            <span v-else-if="k.expires_at && k.expires_at * 1000 < Date.now()" class="pill warn">{{ t('已过期') }}</span>
          </div>
          <div class="small muted">
            {{ scopeText(k.scope_type, k.scope_value) }} · {{ t('最近使用 {time}', { time: k.last_used_at ? fmtTime(k.last_used_at) : t('从未') }) }}
            · {{ t('创建于 {time}', { time: fmtDateTime(k.created_at) }) }}{{ k.expires_at ? ' · ' + t('{time} 过期', { time: fmtDateTime(k.expires_at) }) : '' }}
          </div>
        </div>
        <button v-if="!k.revoked_at" type="button" class="secondary small" @click="revoke(k)">{{ t('吊销') }}</button>
      </li>
    </ul>
    <p v-else-if="!adding && !created" class="sc-empty">{{ t('还没有 API Key') }}</p>
  </SettingsCard>
</template>

<style scoped>
.created { display: flex; flex-direction: column; gap: var(--space-2); }
.form { display: flex; flex-direction: column; gap: var(--space-3); }
.form label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.servers { border: 1px solid var(--border); border-radius: var(--radius-sm); padding: var(--space-2) var(--space-3);
  max-height: 200px; overflow-y: auto; display: flex; flex-direction: column; gap: var(--space-1); }
.check { flex-direction: row !important; align-items: center; gap: var(--space-2) !important; }
.check input { width: auto; }
.actions { display: flex; gap: var(--space-2); }
.sc-list li.off { opacity: .6; }
.who { flex: 1; min-width: 0; }
.who-line { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.mono { font-family: var(--font-mono); }
.bad { color: var(--bad); }
</style>
