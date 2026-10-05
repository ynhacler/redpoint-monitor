<script setup lang="ts">
// 云账户页（设计 44）：接入用户自己的云账户，查看本月费用、预估与预算，云厂商口径的实例与流量包用量。
// 已支持 AWS、阿里云与腾讯云（各分国内站、国际站）；Oracle Cloud 随后续步骤加入（设计 44.10）。
// 【安全】凭证只写不读：列表只显示末 4 位；添加、更换凭证、删除前重新输入密码（设计 17.4、44.2）。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  createCloudAccount, deleteCloudAccount, errorText, linkCloudInstance, listCloudAccounts, listCloudInstances, reauth, syncCloudAccount,
  updateCloudAccount, ApiError, type CloudAccount, type CloudAccountInput, type CloudInstance,
} from '../api'
import CommandBlock from '../components/CommandBlock.vue'
import EmptyState from '../components/EmptyState.vue'
import UsageBar from '../components/UsageBar.vue'
import { DASH, fmtBytes, fmtDate, fmtMoney, fmtTime } from '../format'
import { state } from '../store'

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

// ---- 服务商（设计 44.3） ----

type Provider = NonNullable<CloudAccountInput['provider']>
interface ProviderInfo {
  label: string
  /** 账户默认币种（预算的单位） */
  currency: string
  idLabel: string
  idPlaceholder: string
  secretLabel: string
  /** 凭证中 ID 与密钥字段的名称（与接口一致） */
  idField: 'access_key_id' | 'secret_id' | 'user_ocid'
  secretField: 'secret_access_key' | 'access_key_secret' | 'secret_key' | 'private_key'
  /** 轻量 / 套餐流量包的名称 */
  trafficName: string
  /** 账单时区说明 */
  billingTZ: string
  regionExample: string
  /** 费用查询是否收费（AWS Cost Explorer） */
  paidCostApi: boolean
  /** 密钥从哪里获取：简短步骤与控制台地址 */
  keySteps: string
  keyURL: string
}
const providers: Record<Provider, ProviderInfo> = {
  aws: { label: 'AWS', currency: 'USD', idLabel: 'Access Key ID', idPlaceholder: 'AKIA…', secretLabel: 'Secret Access Key',
    idField: 'access_key_id', secretField: 'secret_access_key', regionExample: 'ap-northeast-1, us-west-2', paidCostApi: true,
    trafficName: 'Lightsail', billingTZ: '按 UTC 自然月',
    keySteps: 'IAM 控制台 → 用户 → 创建用户（附加下面的只读策略）→ 安全凭证 → 创建访问密钥（用途选“第三方服务”）',
    keyURL: 'https://console.aws.amazon.com/iam/home#/users' },
  aliyun_cn: { label: '阿里云', currency: 'CNY', idLabel: 'AccessKey ID', idPlaceholder: 'LTAI…', secretLabel: 'AccessKey Secret',
    idField: 'access_key_id', secretField: 'access_key_secret', regionExample: 'cn-hongkong, cn-hangzhou', paidCostApi: false,
    trafficName: '轻量应用服务器', billingTZ: '按北京时间自然月',
    keySteps: 'RAM 访问控制 → 用户 → 创建用户（勾选“使用永久 AccessKey 访问”）→ 添加权限（三个只读策略）→ 保存 AccessKey',
    keyURL: 'https://ram.console.aliyun.com/users' },
  aliyun_intl: { label: '阿里云国际', currency: 'USD', idLabel: 'AccessKey ID', idPlaceholder: 'LTAI…', secretLabel: 'AccessKey Secret',
    idField: 'access_key_id', secretField: 'access_key_secret', regionExample: 'ap-southeast-1, ap-northeast-1', paidCostApi: false,
    trafficName: '轻量应用服务器', billingTZ: '按北京时间自然月',
    keySteps: 'RAM 控制台 → Users → Create User（勾选 Using permanent AccessKey）→ Add Permissions（三个只读策略）→ 保存 AccessKey',
    keyURL: 'https://ram.console.alibabacloud.com/users' },
  tencent_cn: { label: '腾讯云', currency: 'CNY', idLabel: 'SecretId', idPlaceholder: 'AKID…', secretLabel: 'SecretKey',
    idField: 'secret_id', secretField: 'secret_key', regionExample: 'ap-hongkong, ap-guangzhou', paidCostApi: false,
    trafficName: '轻量应用服务器', billingTZ: '按北京时间自然月',
    keySteps: '访问管理 CAM → 用户 → 新建用户（自定义创建，访问方式选“编程访问”）→ 关联下面的自定义策略 → 保存 SecretId / SecretKey',
    keyURL: 'https://console.cloud.tencent.com/cam' },
  tencent_intl: { label: '腾讯云国际', currency: 'USD', idLabel: 'SecretId', idPlaceholder: 'AKID…', secretLabel: 'SecretKey',
    idField: 'secret_id', secretField: 'secret_key', regionExample: 'ap-singapore, ap-tokyo', paidCostApi: false,
    trafficName: 'Lighthouse', billingTZ: '按北京时间自然月',
    keySteps: 'CAM 控制台 → Users → Create User（Custom，Programmatic access）→ 关联下面的自定义策略 → 保存 SecretId / SecretKey',
    keyURL: 'https://console.tencentcloud.com/cam' },
  // Oracle Cloud 用 API 签名密钥（RSA 私钥），另需租户 OCID、指纹与主区域（设计 44.3）
  oci: { label: 'Oracle Cloud', currency: 'USD', idLabel: '用户 OCID', idPlaceholder: 'ocid1.user.oc1..…', secretLabel: 'API 私钥（PEM）',
    idField: 'user_ocid', secretField: 'private_key', regionExample: 'ap-tokyo-1, us-ashburn-1', paidCostApi: false,
    trafficName: '出站流量（每月 10 TB 免费）', billingTZ: '按 UTC 自然月',
    keySteps: '控制台右上角头像 → 我的概要信息（My profile）→ API 密钥 → 添加 API 密钥 → 下载私钥 → 添加，复制弹出的配置文件预览中的 user、fingerprint、tenancy、region',
    keyURL: 'https://cloud.oracle.com/identity/domains/my-profile/api-keys' },
}
const providerOf = (p: string) => providers[p as Provider] ?? providers.aws

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
  provider: 'aws' as Provider, name: '', access_key_id: '', secret: '', tenancy_ocid: '', fingerprint: '', home_region: '', regions: '', budget: '', cost_interval_h: 12,
  sync_cost: true, sync_traffic: true, enabled: true, password: '',
})
const info = computed(() => providers[form.value.provider])
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const saving = ref(false)
const showPolicy = ref(false)

function openNew() {
  editing.value = 'new'
  form.value = { provider: 'aws', name: '', access_key_id: '', secret: '', tenancy_ocid: '', fingerprint: '', home_region: '', regions: '', budget: '', cost_interval_h: 12,
    sync_cost: true, sync_traffic: true, enabled: true, password: '' }
  showPolicy.value = false
  fieldErrors.value = {}
  formError.value = ''
}

function openEdit(a: CloudAccount) {
  editing.value = a.id
  form.value = { provider: a.provider as Provider, name: a.name, access_key_id: '', secret: '', tenancy_ocid: '', fingerprint: '', home_region: '', regions: a.regions.join(', '),
    budget: a.budget_cents ? String(a.budget_cents / 100) : '', cost_interval_h: a.cost_interval_h,
    sync_cost: a.sync_cost, sync_traffic: a.sync_traffic, enabled: a.enabled, password: '' }
  fieldErrors.value = {}
  formError.value = ''
}

// 添加时、或编辑时填写了新凭证：需要重新输入密码
const changingCred = computed(() => {
  const f = form.value
  return editing.value === 'new' || !!(f.access_key_id || f.secret || f.tenancy_ocid || f.fingerprint || f.home_region)
})

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
    body.credential = { [info.value.idField]: f.access_key_id.trim(), [info.value.secretField]: f.secret.trim() }
    if (f.provider === 'oci') {
      Object.assign(body.credential, { tenancy_ocid: f.tenancy_ocid.trim(), fingerprint: f.fingerprint.trim().toLowerCase(),
        region: f.home_region.trim() })
    }
  }
  saving.value = true
  try {
    if (changingCred.value) await reauth(f.password)
    if (editing.value === 'new') await createCloudAccount({ ...body, provider: f.provider })
    else if (editing.value !== null) await updateCloudAccount(editing.value, body)
    editing.value = null
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.details.length) {
      // credential.access_key_id → access_key_id；各家的密钥字段都显示在“密钥”输入框下，OCI 的 region 显示在“主区域”下
      fieldErrors.value = Object.fromEntries(e.details.map((d) => [
        d.field.replace(/^credential\./, '').replace(/^(secret_access_key|access_key_secret|secret_key|private_key)$/, 'secret')
          .replace(/^(secret_id|user_ocid)$/, 'access_key_id').replace(/^region$/, 'home_region'), d.message]))
    } else {
      formError.value = errorText(e)
    }
  } finally {
    f.password = ''
    f.secret = ''
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

// 阿里云：在 RAM 中创建用户，只授予这三个系统只读策略
const aliyunPolicies = 'AliyunBSSReadOnlyAccess\nAliyunECSReadOnlyAccess\nAliyunSWASReadOnlyAccess'

// 腾讯云：自定义策略，只列出用到的只读接口（也可改用预设策略 QcloudFinanceBillReadOnlyAccess、
// QcloudCVMReadOnlyAccess、QcloudLighthouseReadOnlyAccess，余额另需 DescribeAccountBalance 授权）
const tencentPolicy = JSON.stringify({
  version: '2.0',
  statement: [{
    effect: 'allow',
    action: ['billing:DescribeBillSummaryByProduct', 'billing:DescribeAccountBalance', 'cvm:DescribeRegions',
      'cvm:DescribeInstances', 'lighthouse:DescribeRegions', 'lighthouse:DescribeInstances',
      'lighthouse:DescribeInstancesTrafficPackages'],
    resource: ['*'],
  }],
}, null, 2)
// Oracle Cloud：把 API 用户放进一个组，在根区间为该组添加这四条只读策略
const ociPolicy = ['read usage-reports', 'inspect compartments', 'read instance-family', 'read virtual-network-family']
  .map((x) => `Allow group <组名> to ${x} in tenancy`).join('\n')
const policyFor = (p: Provider) => (p === 'aws' ? awsPolicy : p === 'oci' ? ociPolicy
  : p.startsWith('tencent') ? tencentPolicy : aliyunPolicies)

// ---- 实例 ----

function trafficPct(i: CloudInstance): number {
  return i.traffic_limit_bytes ? (i.traffic_used_bytes / i.traffic_limit_bytes) * 100 : 0
}
const kindNames: Record<string, string> = { ec2: 'EC2', lightsail: 'Lightsail', ecs: 'ECS', swas: '轻量', cvm: 'CVM', lighthouse: '轻量',
  oci: 'OCI', oci_egress: '租户' }

// ---- 与节点关联（设计 44.5）：按公网 IP 建议，由用户确认 ----
const nodeName = (id: number | null | undefined) => (id ? state.servers.find((s) => s.id === id)?.name ?? `#${id}` : '')
const linkable = computed(() => [...state.servers].sort((a, b) => a.name.localeCompare(b.name)))
const linkError = ref('')
async function link(i: CloudInstance, serverId: number | null) {
  linkError.value = ''
  try {
    const v = await linkCloudInstance(i.id, serverId)
    const k = instances.value.findIndex((x) => x.id === i.id)
    if (k >= 0) instances.value[k] = { ...v, suggested_server_id: null }
  } catch (e) {
    linkError.value = errorText(e)
  }
}

/** 到期提示：30 天内为 warn，已过期为 bad */
function expireTone(i: CloudInstance): string {
  const days = (i.expire_at - Date.now() / 1000) / 86400
  return days < 0 ? 'bad' : days < 30 ? 'warn' : 'muted'
}
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>云账户</h1>
      <button v-if="editing === null" type="button" @click="openNew">添加账户</button>
    </div>
    <p class="muted small intro">
      接入你自己的云账户，查看本月费用与预估、云厂商口径的实例与流量包用量。只需只读权限；凭证加密保存在本面板，
      面板直接请求云厂商的官方接口，数据不经过任何第三方。目前支持 AWS、阿里云与腾讯云（国内站、国际站），Oracle Cloud 将陆续支持。
    </p>
    <p v-if="error" class="banner">{{ error }}</p>

    <!-- 添加 / 编辑 -->
    <form v-if="editing !== null" class="panel editor" novalidate @submit.prevent="save">
      <h2>{{ editing === 'new' ? '添加云账户' : `编辑 ${form.name}` }}</h2>
      <!-- 服务商较多，用下拉而不是分段按钮，窄屏也不溢出 -->
      <label v-if="editing === 'new'" class="provider-pick small">服务商
        <select v-model="form.provider" @change="showPolicy = false">
          <option v-for="(p, key) in providers" :key="key" :value="key">{{ p.label }}</option>
        </select>
      </label>
      <p v-if="form.provider === 'aws'" class="muted small">
        在 IAM 中创建一个专用用户，只附加下面的只读策略，再为它创建访问密钥。
        <button type="button" class="text" @click="showPolicy = !showPolicy">{{ showPolicy ? '收起策略' : '查看最小权限策略' }}</button>
      </p>
      <p v-else-if="form.provider.startsWith('aliyun')" class="muted small">
        在{{ form.provider === 'aliyun_cn' ? '阿里云（aliyun.com）' : '阿里云国际站（alibabacloud.com）' }} RAM 控制台创建一个专用用户，
        只授予三个系统只读策略，再为它创建 AccessKey。国内站与国际站是两套账号，请按账号所在站点选择。
        <button type="button" class="text" @click="showPolicy = !showPolicy">{{ showPolicy ? '收起' : '查看策略名称' }}</button>
      </p>
      <p v-else-if="form.provider === 'oci'" class="muted small">
        建议创建一个专用用户并加入一个组，在根区间为该组添加下面的只读策略，再为该用户添加 API 密钥（不带密码的 RSA 私钥）。
        出站流量按租户统计，对照每月 10 TB 免费额度。
        <button type="button" class="text" @click="showPolicy = !showPolicy">{{ showPolicy ? '收起策略' : '查看最小权限策略' }}</button>
      </p>
      <p v-else class="muted small">
        在{{ form.provider === 'tencent_cn' ? '腾讯云（cloud.tencent.com）' : '腾讯云国际站（tencentcloud.com）' }}访问管理 CAM 中创建子用户，
        关联下面的自定义只读策略，再为它创建 API 密钥。国内站与国际站是两套账号，请按账号所在站点选择。
        <button type="button" class="text" @click="showPolicy = !showPolicy">{{ showPolicy ? '收起策略' : '查看最小权限策略' }}</button>
      </p>
      <CommandBlock v-if="showPolicy" :command="policyFor(form.provider)" plain class="policy" />
      <!-- 密钥从哪里获取（用户最常问的问题）：一行步骤 + 控制台链接 -->
      <p class="key-help small">
        <b>{{ info.idLabel }} 从哪里获取：</b>{{ info.keySteps }}。
        <a :href="info.keyURL" target="_blank" rel="noopener noreferrer">打开控制台</a>
        <br><span class="muted">密钥只显示一次，请立即复制；不要使用主账号（根账号）的密钥。</span>
      </p>
      <div class="fields">
        <label>名称
          <input v-model="form.name" maxlength="64" :placeholder="`如 ${info.label} 主账户`" />
          <small v-if="fieldErrors.name" class="err">{{ fieldErrors.name }}</small>
        </label>
        <label>{{ info.idLabel }}
          <input v-model="form.access_key_id" autocomplete="off" spellcheck="false"
            :placeholder="editing === 'new' ? info.idPlaceholder : '留空保持不变'" />
          <small v-if="fieldErrors.access_key_id" class="err">{{ fieldErrors.access_key_id }}</small>
        </label>
        <template v-if="form.provider === 'oci'">
          <label>租户 OCID
            <input v-model="form.tenancy_ocid" autocomplete="off" spellcheck="false"
              :placeholder="editing === 'new' ? 'ocid1.tenancy.oc1..…' : '留空保持不变'" />
            <small v-if="fieldErrors.tenancy_ocid" class="err">{{ fieldErrors.tenancy_ocid }}</small>
          </label>
          <label>指纹（fingerprint）
            <input v-model="form.fingerprint" autocomplete="off" spellcheck="false"
              :placeholder="editing === 'new' ? 'aa:bb:…（16 组）' : '留空保持不变'" />
            <small v-if="fieldErrors.fingerprint" class="err">{{ fieldErrors.fingerprint }}</small>
          </label>
          <label>主区域（home region）
            <input v-model="form.home_region" autocomplete="off" spellcheck="false"
              :placeholder="editing === 'new' ? '如 ap-tokyo-1' : '留空保持不变'" />
            <small v-if="fieldErrors.home_region" class="err">{{ fieldErrors.home_region }}</small>
          </label>
        </template>
        <label :class="{ wide: form.provider === 'oci' }">{{ info.secretLabel }}
          <!-- 私钥是多行 PEM：用文本框；不进浏览器自动填充 -->
          <textarea v-if="form.provider === 'oci'" v-model="form.secret" rows="4" autocomplete="off" spellcheck="false" class="mono pem"
            :placeholder="editing === 'new' ? '-----BEGIN PRIVATE KEY-----\n…\n-----END PRIVATE KEY-----' : '留空保持不变'" />
          <input v-else v-model="form.secret" type="password" autocomplete="new-password" spellcheck="false"
            :placeholder="editing === 'new' ? '' : '留空保持不变'" />
          <small v-if="fieldErrors.secret" class="err">{{ fieldErrors.secret }}</small>
          <small v-else-if="fieldErrors.credential" class="err">{{ fieldErrors.credential }}</small>
        </label>
        <label class="wide">区域
          <input v-model="form.regions" spellcheck="false" :placeholder="`留空表示全部区域；如 ${info.regionExample}`" />
          <small v-if="fieldErrors.regions" class="err">{{ fieldErrors.regions }}</small>
        </label>
        <label>月度预算（{{ info.currency }}）
          <input v-model="form.budget" inputmode="decimal" placeholder="不设" />
          <small v-if="fieldErrors.budget" class="err">{{ fieldErrors.budget }}</small>
        </label>
        <label>费用同步间隔
          <select v-model.number="form.cost_interval_h">
            <option :value="6">每 6 小时</option>
            <option :value="12">每 12 小时</option>
            <option :value="24">每天</option>
          </select>
          <small v-if="info.paidCostApi" class="muted">AWS Cost Explorer 每次调用收费 0.01 美元，此频率每月{{ intervalCost[form.cost_interval_h] }} 美元</small>
          <small v-else class="muted">账单与余额接口免费</small>
        </label>
        <label class="check"><input v-model="form.sync_cost" type="checkbox" />同步费用</label>
        <label class="check"><input v-model="form.sync_traffic" type="checkbox" />同步流量包（{{ info.trafficName }}，每小时）</label>
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
      <button type="button" @click="openNew">添加账户</button>
    </EmptyState>

    <!-- 账户 -->
    <ul v-if="accounts.length" class="accounts">
      <li v-for="a in accounts" :key="a.id" class="panel account" :class="{ off: !a.enabled }">
        <div class="head">
          <div class="title">
            <span class="type">{{ providerOf(a.provider).label }}</span><b>{{ a.name }}</b>
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
          <!-- 阿里云没有费用预测，显示账户余额 -->
          <div v-if="a.current_cost?.balance_cents != null">
            <div class="muted small">账户余额</div>
            <div class="big num">{{ fmtMoney(a.current_cost.balance_cents, a.current_cost.currency) }}</div>
          </div>
          <div v-else>
            <div class="muted small">本月预估</div>
            <div class="big num">{{ a.current_cost ? fmtMoney(a.current_cost.forecast_cents, a.current_cost.currency) : DASH }}</div>
          </div>
          <div>
            <div class="muted small">预算</div>
            <div class="big num">{{ a.budget_cents ? fmtMoney(a.budget_cents, a.current_cost?.currency ?? providerOf(a.provider).currency) : '未设' }}</div>
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
          费用每 {{ a.cost_interval_h }} 小时{{ a.sync_cost ? '' : '（已关闭）' }} ·
          {{ providerOf(a.provider).billingTZ }}，数据有数小时延迟
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
          <span class="small state">
            <span :class="i.state.toLowerCase() === 'running' ? 'ok' : 'muted'">{{ i.state }}</span>
            <span v-if="i.expire_at" class="expire" :class="expireTone(i)">到期 {{ fmtDate(i.expire_at) }}</span>
          </span>
          <div v-if="i.traffic_limit_bytes" class="traffic">
            <UsageBar :pct="trafficPct(i)" />
            <span class="small num muted">{{ fmtBytes(i.traffic_used_bytes) }} / {{ fmtBytes(i.traffic_limit_bytes) }}</span>
          </div>
          <span v-else class="traffic muted small">{{ i.account_name }}</span>
          <!-- 与节点关联：已关联 / 建议关联（公网 IP 唯一匹配）/ 手动选择 -->
          <div v-if="i.kind !== 'oci_egress'" class="link small">
            <template v-if="i.server_id">
              关联节点 <RouterLink :to="`/servers/${i.server_id}`">{{ nodeName(i.server_id) }}</RouterLink>
              <button type="button" class="text" @click="link(i, null)">取消关联</button>
            </template>
            <template v-else-if="i.suggested_server_id">
              公网 IP 与节点 <b>{{ nodeName(i.suggested_server_id) }}</b> 相同
              <button type="button" class="secondary small" @click="link(i, i.suggested_server_id)">关联</button>
            </template>
            <select v-else class="small" aria-label="关联节点" :value="''"
              @change="link(i, Number(($event.target as HTMLSelectElement).value) || null)">
              <option value="">关联到节点…</option>
              <option v-for="s in linkable" :key="s.id" :value="s.id">{{ s.name }}</option>
            </select>
          </div>
        </li>
      </ul>
      <p v-if="linkError" class="small err">{{ linkError }}</p>
      <p class="muted small">
        Lightsail 流量为本月（UTC）入站 + 出站，按套餐额度计；阿里云、腾讯云轻量为流量包的额度与已用量。
        关联节点后，节点详情显示云厂商口径的状态、流量包与到期时间，可一键写入节点的到期日。
      </p>
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
.fields .pem { font-size: var(--font-xs); resize: vertical; -webkit-text-security: disc; }
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
.list li { display: grid; grid-template-columns: minmax(0, 1fr) auto 220px; gap: var(--space-2) var(--space-3); align-items: center;
  padding: var(--space-3) var(--space-4); border-bottom: 1px solid var(--border); }
.link { grid-column: 1 / -1; display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; color: var(--text-muted); }
.link select { width: auto; max-width: 220px; }
.list li:last-child { border-bottom: 0; }
.state { display: flex; flex-direction: column; align-items: flex-end; gap: 2px; white-space: nowrap; }
.key-help { margin: 0 0 var(--space-3); padding: var(--space-2) var(--space-3); background: var(--surface-2);
  border-radius: var(--radius-sm); line-height: var(--line-md); }
.provider-pick { display: flex; flex-direction: column; gap: var(--space-1); max-width: 240px; margin: var(--space-2) 0 var(--space-3); }
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
