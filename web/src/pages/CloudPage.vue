<script setup lang="ts">
// 云账户页（设计 44）：接入用户自己的云账户，查看本月费用、预估与预算，云厂商口径的实例与流量包用量。
// 第一步只支持 AWS；阿里云、腾讯云、Oracle Cloud 随后续步骤加入（设计 44.10）。
// 【安全】凭证只写不读：列表只显示末 4 位；添加、更换凭证、删除前重新输入密码（设计 17.4、44.2）。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  createCloudAccount, deleteCloudAccount, errorText, listCloudAccounts, listCloudInstances, reauth, syncCloudAccount,
  updateCloudAccount, ApiError, type CloudAccount, type CloudAccountInput, type CloudInstance,
} from '../api'
import CommandBlock from '../components/CommandBlock.vue'
import EmptyState from '../components/EmptyState.vue'
import UsageBar from '../components/UsageBar.vue'
import { DASH, fmtBytes, fmtMoney, fmtTime } from '../format'

const accounts = ref<CloudAccount[]>([])
const instances = ref<CloudInstance[]>([])
const loaded = ref(false)
const error = ref('')

async function load() {
  try {
    const [a, i] = await Promise.all([listCloudAccounts(), listCloudInstances()])
    accounts.value = a
    instances.value = i
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '加载失败，请稍后重试')
  } finally {
    loaded.value = true
    schedule()
  }
}

// 有账户正在同步时每 3 秒刷新，否则每分钟
let timer: number | undefined
function schedule() {
  if (timer) clearTimeout(timer)
  timer = window.setTimeout(load, accounts.value.some((a) => a.syncing) ? 3000 : 60_000)
}
onMounted(load)
onUnmounted(() => timer && clearTimeout(timer))

// ---- 账户状态 ----

function status(a: CloudAccount): { tone: string; text: string } {
  if (a.syncing) return { tone: 'muted', text: '同步中…' }
  if (!a.enabled) return { tone: 'muted', text: '已停用' }
  if (a.auth_failed) return { tone: 'bad', text: `凭证失效或权限不足，已停止自动同步：${a.last_error}` }
  if (a.last_error) return { tone: 'warn', text: `同步失败：${a.last_error}（${fmtTime(a.next_try_at)} 重试）` }
  const last = Math.max(a.cost_synced_at, a.instances_synced_at, a.traffic_synced_at)
  return last ? { tone: 'muted', text: `已同步 ${fmtTime(last)}` } : { tone: 'muted', text: '等待首次同步' }
}

/** 预算用量：优先按本月预估，没有预估时按已产生 */
function budgetPct(a: CloudAccount): number | null {
  if (!a.budget_cents || !a.current_cost) return null
  const v = a.current_cost.forecast_cents ?? a.current_cost.amount_cents
  return (v / a.budget_cents) * 100
}

const syncMsg = ref<Record<number, string>>({})
async function syncNow(a: CloudAccount) {
  try {
    await syncCloudAccount(a.id)
    syncMsg.value[a.id] = ''
    a.syncing = true
    schedule()
  } catch (e) {
    syncMsg.value[a.id] = errorText(e)
  }
}

async function toggle(a: CloudAccount) {
  try {
    await updateCloudAccount(a.id, { name: a.name, enabled: !a.enabled })
    await load()
  } catch (e) {
    error.value = errorText(e)
  }
}

// ---- 删除（需重新输入密码） ----

const deleting = ref<number | null>(null)
const deletePassword = ref('')
const deleteError = ref('')
async function remove(a: CloudAccount) {
  if (!deletePassword.value) return
  deleteError.value = ''
  try {
    await reauth(deletePassword.value)
    await deleteCloudAccount(a.id)
    deleting.value = null
    await load()
  } catch (e) {
    deleteError.value = errorText(e)
  } finally {
    deletePassword.value = ''
  }
}

// ---- 添加 / 编辑 ----

const editing = ref<'new' | number | null>(null)
const form = ref({
  name: '', access_key_id: '', secret_access_key: '', regions: '', budget: '', cost_interval_h: 12,
  sync_cost: true, sync_traffic: true, enabled: true, password: '',
})
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const saving = ref(false)
const showPolicy = ref(false)

function openNew() {
  editing.value = 'new'
  form.value = { name: '', access_key_id: '', secret_access_key: '', regions: '', budget: '', cost_interval_h: 12,
    sync_cost: true, sync_traffic: true, enabled: true, password: '' }
  fieldErrors.value = {}
  formError.value = ''
}

function openEdit(a: CloudAccount) {
  editing.value = a.id
  form.value = { name: a.name, access_key_id: '', secret_access_key: '', regions: a.regions.join(', '),
    budget: a.budget_cents ? String(a.budget_cents / 100) : '', cost_interval_h: a.cost_interval_h,
    sync_cost: a.sync_cost, sync_traffic: a.sync_traffic, enabled: a.enabled, password: '' }
  fieldErrors.value = {}
  formError.value = ''
}

// 添加时、或编辑时填写了新凭证：需要重新输入密码
const changingCred = computed(() => editing.value === 'new' || !!(form.value.access_key_id || form.value.secret_access_key))

async function save() {
  const f = form.value
  fieldErrors.value = {}
  formError.value = ''
  if (changingCred.value && !f.password) {
    fieldErrors.value = { password: '请输入当前登录密码以确认' }
    return
  }
  const body: CloudAccountInput = {
    name: f.name.trim(),
    regions: f.regions.split(/[\s,，]+/).filter(Boolean),
    budget: f.budget.trim() === '' ? 0 : Number(f.budget),
    cost_interval_h: f.cost_interval_h as CloudAccountInput['cost_interval_h'],
    sync_cost: f.sync_cost, sync_traffic: f.sync_traffic, enabled: f.enabled,
  }
  if (changingCred.value) {
    body.credential = { access_key_id: f.access_key_id.trim(), secret_access_key: f.secret_access_key.trim() }
  }
  saving.value = true
  try {
    if (changingCred.value) await reauth(f.password)
    if (editing.value === 'new') await createCloudAccount({ ...body, provider: 'aws' })
    else if (editing.value !== null) await updateCloudAccount(editing.value, body)
    editing.value = null
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.details.length) {
      // credential.access_key_id → access_key_id
      fieldErrors.value = Object.fromEntries(e.details.map((d) => [d.field.replace(/^credential\./, ''), d.message]))
    } else {
      formError.value = errorText(e)
    }
  } finally {
    f.password = ''
    f.secret_access_key = ''
    saving.value = false
  }
}

// 最小只读权限（设计 44.3）：在 IAM 中创建只读用户，附加这条策略
const awsPolicy = JSON.stringify({
  Version: '2012-10-17',
  Statement: [{
    Effect: 'Allow',
    Action: ['ce:GetCostAndUsage', 'ce:GetCostForecast', 'ec2:DescribeInstances', 'ec2:DescribeRegions',
      'lightsail:GetRegions', 'lightsail:GetInstances', 'lightsail:GetInstanceMetricData'],
    Resource: '*',
  }],
}, null, 2)

const intervalCost: Record<number, string> = { 6: '约 2.4', 12: '约 1.2', 24: '约 0.6' }

// ---- 实例 ----

function trafficPct(i: CloudInstance): number {
  return i.traffic_limit_bytes ? (i.traffic_used_bytes / i.traffic_limit_bytes) * 100 : 0
}
const kindNames: Record<string, string> = { ec2: 'EC2', lightsail: 'Lightsail' }
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>云账户</h1>
      <button v-if="editing === null" type="button" @click="openNew">添加 AWS 账户</button>
    </div>
    <p class="muted small intro">
      接入你自己的云账户，查看本月费用与预估、云厂商口径的实例与流量包用量。只需只读权限；凭证加密保存在本面板，
      面板直接请求云厂商的官方接口，数据不经过任何第三方。阿里云、腾讯云、Oracle Cloud 将陆续支持。
    </p>
    <p v-if="error" class="banner">{{ error }}</p>

    <!-- 添加 / 编辑 -->
    <form v-if="editing !== null" class="panel editor" novalidate @submit.prevent="save">
      <h2>{{ editing === 'new' ? '添加 AWS 账户' : `编辑 ${form.name}` }}</h2>
      <p class="muted small">
        在 IAM 中创建一个专用用户，只附加下面的只读策略，再为它创建访问密钥。
        <button type="button" class="text" @click="showPolicy = !showPolicy">{{ showPolicy ? '收起策略' : '查看最小权限策略' }}</button>
      </p>
      <CommandBlock v-if="showPolicy" :command="awsPolicy" class="policy" />
      <div class="fields">
        <label>名称
          <input v-model="form.name" maxlength="64" placeholder="如 AWS 主账户" />
          <small v-if="fieldErrors.name" class="err">{{ fieldErrors.name }}</small>
        </label>
        <label>Access Key ID
          <input v-model="form.access_key_id" autocomplete="off" spellcheck="false"
            :placeholder="editing === 'new' ? 'AKIA…' : '留空保持不变'" />
          <small v-if="fieldErrors.access_key_id" class="err">{{ fieldErrors.access_key_id }}</small>
        </label>
        <label>Secret Access Key
          <input v-model="form.secret_access_key" type="password" autocomplete="new-password" spellcheck="false"
            :placeholder="editing === 'new' ? '' : '留空保持不变'" />
          <small v-if="fieldErrors.secret_access_key" class="err">{{ fieldErrors.secret_access_key }}</small>
          <small v-else-if="fieldErrors.credential" class="err">{{ fieldErrors.credential }}</small>
        </label>
        <label class="wide">区域
          <input v-model="form.regions" spellcheck="false" placeholder="留空表示全部已启用的区域；如 ap-northeast-1, us-west-2" />
          <small v-if="fieldErrors.regions" class="err">{{ fieldErrors.regions }}</small>
        </label>
        <label>月度预算（USD）
          <input v-model="form.budget" inputmode="decimal" placeholder="不设" />
          <small v-if="fieldErrors.budget" class="err">{{ fieldErrors.budget }}</small>
        </label>
        <label>费用同步间隔
          <select v-model.number="form.cost_interval_h">
            <option :value="6">每 6 小时</option>
            <option :value="12">每 12 小时</option>
            <option :value="24">每天</option>
          </select>
          <small class="muted">AWS Cost Explorer 每次调用收费 0.01 美元，此频率每月{{ intervalCost[form.cost_interval_h] }} 美元</small>
        </label>
        <label class="check"><input v-model="form.sync_cost" type="checkbox" />同步费用</label>
        <label class="check"><input v-model="form.sync_traffic" type="checkbox" />同步流量（Lightsail 每小时）</label>
        <label class="check"><input v-model="form.enabled" type="checkbox" />启用</label>
        <label v-if="changingCred">当前登录密码
          <input v-model="form.password" type="password" autocomplete="current-password" />
          <small v-if="fieldErrors.password" class="err">{{ fieldErrors.password }}</small>
          <small v-else class="muted">添加或更换凭证前确认身份</small>
        </label>
      </div>
      <p v-if="formError" class="err small">{{ formError }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        <button type="button" class="secondary" @click="editing = null">取消</button>
      </div>
    </form>

    <EmptyState v-if="loaded && !accounts.length && editing === null" text="还没有云账户。添加后面板会定时同步费用、实例与流量包用量。">
      <button type="button" @click="openNew">添加 AWS 账户</button>
    </EmptyState>

    <!-- 账户 -->
    <ul v-if="accounts.length" class="accounts">
      <li v-for="a in accounts" :key="a.id" class="panel account" :class="{ off: !a.enabled }">
        <div class="head">
          <div class="title">
            <span class="type">AWS</span><b>{{ a.name }}</b>
            <span class="muted small mono">{{ a.credential_hint }}</span>
          </div>
          <div class="acts">
            <button type="button" class="secondary" :disabled="a.syncing || !a.enabled" @click="syncNow(a)">立即同步</button>
            <button type="button" class="text" @click="toggle(a)">{{ a.enabled ? '停用' : '启用' }}</button>
            <button type="button" class="text" @click="openEdit(a)">编辑</button>
            <button type="button" class="text danger-text" @click="deleting = deleting === a.id ? null : a.id; deleteError = ''">删除</button>
          </div>
        </div>
        <p class="small status" :class="status(a).tone">{{ status(a).text }}</p>
        <p v-if="syncMsg[a.id]" class="small err">{{ syncMsg[a.id] }}</p>

        <div class="stats">
          <div>
            <div class="muted small">本月已产生</div>
            <div class="big num">{{ a.current_cost ? fmtMoney(a.current_cost.amount_cents, a.current_cost.currency) : DASH }}</div>
          </div>
          <div>
            <div class="muted small">本月预估</div>
            <div class="big num">{{ a.current_cost ? fmtMoney(a.current_cost.forecast_cents, a.current_cost.currency) : DASH }}</div>
          </div>
          <div>
            <div class="muted small">预算</div>
            <div class="big num">{{ a.budget_cents ? fmtMoney(a.budget_cents, a.current_cost?.currency ?? 'USD') : '未设' }}</div>
          </div>
          <div>
            <div class="muted small">实例</div>
            <div class="big num">{{ a.instance_count }}</div>
          </div>
        </div>
        <div v-if="budgetPct(a) != null" class="budget">
          <UsageBar :pct="budgetPct(a)!" />
          <span class="small num" :class="budgetPct(a)! >= 100 ? 'bad' : budgetPct(a)! >= 80 ? 'warn' : 'muted'">
            {{ a.current_cost?.forecast_cents != null ? '预估' : '已产生' }}占预算 {{ Math.round(budgetPct(a)!) }}%
          </span>
        </div>
        <p class="muted small">
          {{ a.regions.length ? `区域：${a.regions.join('、')}` : '全部已启用的区域' }} ·
          费用每 {{ a.cost_interval_h }} 小时{{ a.sync_cost ? '' : '（已关闭）' }} · 费用为 UTC 自然月，数据有数小时延迟
        </p>

        <form v-if="deleting === a.id" class="confirm" @submit.prevent="remove(a)">
          <span class="small">删除账户及同步的费用、实例数据（不影响云上的资源）。输入当前登录密码确认：</span>
          <input v-model="deletePassword" type="password" autocomplete="current-password" />
          <button type="submit" class="danger" :disabled="!deletePassword">删除</button>
          <span v-if="deleteError" class="small err">{{ deleteError }}</span>
        </form>
      </li>
    </ul>

    <!-- 实例 -->
    <section v-if="instances.length" class="section">
      <h2>实例</h2>
      <ul class="panel list">
        <li v-for="i in instances" :key="i.id">
          <div class="inst-main">
            <div class="ellipsis"><b>{{ i.name || i.instance_id }}</b>
              <span class="muted small meta">{{ kindNames[i.kind] ?? i.kind }} · {{ i.region }} · {{ i.plan }}</span>
            </div>
            <div class="muted small ellipsis mono">{{ [i.public_ipv4, i.public_ipv6].filter(Boolean).join(' / ') || '无公网 IP' }}</div>
          </div>
          <span class="small state" :class="i.state === 'running' ? 'ok' : 'muted'">{{ i.state }}</span>
          <div v-if="i.traffic_limit_bytes" class="traffic">
            <UsageBar :pct="trafficPct(i)" />
            <span class="small num muted">{{ fmtBytes(i.traffic_used_bytes) }} / {{ fmtBytes(i.traffic_limit_bytes) }}</span>
          </div>
          <span v-else class="traffic muted small">{{ i.account_name }}</span>
        </li>
      </ul>
      <p class="muted small">Lightsail 流量为本月（UTC）入站 + 出站，按套餐额度计；与节点关联、自动校准与到期提醒将在后续版本加入。</p>
    </section>
  </main>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-4); max-width: 72ch; }
.editor { margin-bottom: var(--space-4); }
.editor h2 { margin-bottom: var(--space-2); }
.policy { margin-bottom: var(--space-3); }
.fields { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: var(--space-3); margin: var(--space-3) 0; }
.fields label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.fields .wide { grid-column: 1 / -1; }
.fields .check { flex-direction: row; align-items: center; gap: var(--space-2); }
.check input { width: auto; }
.actions { display: flex; gap: var(--space-2); }
.err { color: var(--bad); }
.mono { font-family: var(--font-mono); }
.accounts { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-3); }
.account.off { opacity: .7; }
.head { display: flex; justify-content: space-between; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.title { display: flex; align-items: baseline; gap: var(--space-2); min-width: 0; flex-wrap: wrap; }
.type { font-size: var(--font-xs); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 0 var(--space-1); color: var(--text-muted); }
.acts { display: flex; gap: var(--space-2); align-items: center; flex-wrap: wrap; }
.status { margin: var(--space-2) 0; word-break: break-word; }
.stats { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--space-3); margin: var(--space-3) 0; }
.big { font-size: var(--font-lg); line-height: var(--line-lg); font-weight: var(--weight-strong); }
.budget { display: flex; align-items: center; gap: var(--space-3); margin-bottom: var(--space-2); }
.budget > :first-child { flex: 1; }
.confirm { display: flex; gap: var(--space-2); align-items: center; flex-wrap: wrap; margin-top: var(--space-3);
  padding-top: var(--space-3); border-top: 1px solid var(--border); }
.confirm input { width: 200px; }
.list { list-style: none; margin: var(--space-3) 0 var(--space-2); padding: 0; }
.list li { display: grid; grid-template-columns: minmax(0, 1fr) auto 220px; gap: var(--space-3); align-items: center;
  padding: var(--space-3) var(--space-4); border-bottom: 1px solid var(--border); }
.list li:last-child { border-bottom: 0; }
.inst-main { min-width: 0; }
.meta { margin-left: var(--space-2); }
.ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.traffic { display: flex; flex-direction: column; gap: var(--space-1); }
.danger-text { color: var(--bad); }
@media (max-width: 640px) {
  .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .list li { grid-template-columns: minmax(0, 1fr) auto; }
  .list .traffic { grid-column: 1 / -1; }
}
</style>
