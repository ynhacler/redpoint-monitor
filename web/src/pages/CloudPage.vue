<script setup lang="ts">
// 云账户页（设计 44）：接入用户自己的云账户，查看本月费用、预估与预算，云厂商口径的实例与流量包用量。
// 已支持 AWS、阿里云与腾讯云（各分国内站、国际站）；Oracle Cloud 随后续步骤加入（设计 44.10）。
// 【安全】凭证只写不读：列表只显示末 4 位；添加、更换凭证、删除前重新输入密码（设计 17.4、44.2）。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  createCloudAccount, deleteCloudAccount, errorText, linkCloudInstance, setCloudAutoCalibrate, listCloudAccounts, listCloudInstances, reauth, syncCloudAccount, checkCloudAccount,
  updateCloudAccount, ApiError, type CloudAccount, type CloudAccountInput, type CloudCheckItem, type CloudInstance, type CloudTrafficPool,
} from '../api'
import CommandBlock from '../components/CommandBlock.vue'
import EmptyState from '../components/EmptyState.vue'
import UsageBar from '../components/UsageBar.vue'
import { DASH, fmtBytes, fmtDate, fmtMoney, fmtTime, fmtTraffic } from '../format'
import { t } from '../i18n'
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
    error.value = errorText(e, t('加载失败，请稍后重试'))
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
  /** 是否返回账户余额（可设余额提醒，设计 44.6） */
  hasBalance: boolean
  /** 密钥从哪里获取：简短步骤与控制台地址 */
  keySteps: string
  keyURL: string
}
const providers: Record<Provider, ProviderInfo> = {
  aws: { label: 'AWS', currency: 'USD', idLabel: 'Access Key ID', idPlaceholder: 'AKIA…', secretLabel: 'Secret Access Key',
    idField: 'access_key_id', secretField: 'secret_access_key', regionExample: 'ap-northeast-1, us-west-2', paidCostApi: true, hasBalance: false,
    trafficName: 'Lightsail', billingTZ: '按 UTC 自然月',
    keySteps: 'IAM → Users → 点该用户 → Security credentials → Create access key（用例选“在 AWS 外部运行的应用程序”）',
    keyURL: 'https://console.aws.amazon.com/iam/home#/users' },
  aliyun_cn: { label: '阿里云', currency: 'CNY', idLabel: 'AccessKey ID', idPlaceholder: 'LTAI…', secretLabel: 'AccessKey Secret',
    idField: 'access_key_id', secretField: 'access_key_secret', regionExample: 'cn-hongkong, cn-hangzhou', paidCostApi: false, hasBalance: true,
    trafficName: '轻量应用服务器与 CDT', billingTZ: '按北京时间自然月',
    keySteps: 'RAM 访问控制 → 用户 → 创建用户（勾选“使用永久 AccessKey 访问”）→ 添加权限（四个只读策略）→ 保存 AccessKey',
    keyURL: 'https://ram.console.aliyun.com/users' },
  aliyun_intl: { label: '阿里云国际', currency: 'USD', idLabel: 'AccessKey ID', idPlaceholder: 'LTAI…', secretLabel: 'AccessKey Secret',
    idField: 'access_key_id', secretField: 'access_key_secret', regionExample: 'ap-southeast-1, ap-northeast-1', paidCostApi: false, hasBalance: true,
    trafficName: '轻量应用服务器与 CDT', billingTZ: '按北京时间自然月',
    keySteps: 'RAM 控制台 → Users → Create User（勾选 Using permanent AccessKey）→ Add Permissions（四个只读策略）→ 保存 AccessKey',
    keyURL: 'https://ram.console.alibabacloud.com/users' },
  tencent_cn: { label: '腾讯云', currency: 'CNY', idLabel: 'SecretId', idPlaceholder: 'AKID…', secretLabel: 'SecretKey',
    idField: 'secret_id', secretField: 'secret_key', regionExample: 'ap-hongkong, ap-guangzhou', paidCostApi: false, hasBalance: true,
    trafficName: '轻量应用服务器', billingTZ: '按北京时间自然月',
    keySteps: '访问管理 CAM → 用户 → 新建用户（自定义创建，访问方式选“编程访问”）→ 关联下面的自定义策略 → 保存 SecretId / SecretKey',
    keyURL: 'https://console.cloud.tencent.com/cam' },
  tencent_intl: { label: '腾讯云国际', currency: 'USD', idLabel: 'SecretId', idPlaceholder: 'AKID…', secretLabel: 'SecretKey',
    idField: 'secret_id', secretField: 'secret_key', regionExample: 'ap-singapore, ap-tokyo', paidCostApi: false, hasBalance: true,
    trafficName: 'Lighthouse', billingTZ: '按北京时间自然月',
    keySteps: 'CAM 控制台 → Users → Create User（Custom，Programmatic access）→ 关联下面的自定义策略 → 保存 SecretId / SecretKey',
    keyURL: 'https://console.tencentcloud.com/cam' },
  // Oracle Cloud 用 API 签名密钥（RSA 私钥），另需租户 OCID、指纹与主区域（设计 44.3）
  oci: { label: 'Oracle Cloud', currency: 'USD', idLabel: '用户 OCID', idPlaceholder: 'ocid1.user.oc1..…', secretLabel: 'API 私钥（PEM）',
    idField: 'user_ocid', secretField: 'private_key', regionExample: 'ap-tokyo-1, us-ashburn-1', paidCostApi: false, hasBalance: false,
    trafficName: '出站流量（每月 10 TB 免费）', billingTZ: '按 UTC 自然月',
    keySteps: '控制台右上角头像 → 我的概要信息（My profile）→ API 密钥 → 添加 API 密钥 → 下载私钥 → 添加，复制弹出的配置文件预览中的 user、fingerprint、tenancy、region',
    keyURL: 'https://cloud.oracle.com/identity/domains/my-profile/api-keys' },
}
const providerOf = (p: string) => providers[p as Provider] ?? providers.aws

// ---- 账户状态 ----

function status(a: CloudAccount): { tone: string; text: string } {
  if (a.syncing) return { tone: 'muted', text: t('同步中…') }
  if (!a.enabled) return { tone: 'muted', text: t('已停用') }
  if (a.auth_failed) return { tone: 'bad', text: t('凭证失效或权限不足，已停止自动同步：{error}', { error: a.last_error }) }
  if (a.last_error) return { tone: 'warn', text: t('同步失败：{error}（{time} 重试）', { error: a.last_error, time: fmtTime(a.next_try_at) }) }
  const last = Math.max(a.cost_synced_at, a.instances_synced_at, a.traffic_synced_at)
  // 区域级问题（修订第 141 条）：其他区域照常同步，被跳过区域的实例保留上一次的结果
  // 有区域暂时失败时约 30 分钟后自动重新检查（修订第 151 条）
  const recheck = a.instances_recheck_at ? t('（{time} 自动重新检查）', { time: fmtTime(a.instances_recheck_at) }) : ''
  if (a.last_warning) return { tone: 'warn', text: t('已同步 {time}，部分内容已跳过：{warning}', { time: fmtTime(last), warning: a.last_warning }) + recheck }
  return last ? { tone: 'muted', text: t('已同步 {time}', { time: fmtTime(last) }) } : { tone: 'muted', text: t('等待首次同步') }
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

// 权限检查（修订第 204 条）：阿里云逐项列出能否读取、各区域的实例数（含走 CDT 的台数），排查“看不到某台实例”
const isAliyun = (a: CloudAccount) => a.provider === 'aliyun_cn' || a.provider === 'aliyun_intl'
const checks = ref<Record<number, { busy: boolean; items: CloudCheckItem[]; error: string }>>({})
async function checkNow(a: CloudAccount) {
  checks.value[a.id] = { busy: true, items: [], error: '' }
  const c = checks.value[a.id] // 经响应式代理修改，界面才会更新
  try {
    c.items = await checkCloudAccount(a.id)
  } catch (e) {
    c.error = errorText(e)
  } finally {
    c.busy = false
  }
}

function closeCheck(a: CloudAccount) {
  delete checks.value[a.id]
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
  if (!deletePassword.value) {
    deleteError.value = t('请输入登录密码以确认')
    return
  }
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
  provider: 'aws' as Provider, name: '', access_key_id: '', secret: '', tenancy_ocid: '', fingerprint: '', home_region: '', regions: '', budget: '', balance_alert: '', cost_interval_h: 12,
  sync_cost: true, sync_traffic: true, enabled: true, password: '',
})
const info = computed(() => providers[form.value.provider])
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const saving = ref(false)
const showPolicy = ref(false)

function openNew() {
  editing.value = 'new'
  form.value = { provider: 'aws', name: '', access_key_id: '', secret: '', tenancy_ocid: '', fingerprint: '', home_region: '', regions: '', budget: '', balance_alert: '', cost_interval_h: 12,
    sync_cost: true, sync_traffic: true, enabled: true, password: '' }
  showPolicy.value = false
  fieldErrors.value = {}
  formError.value = ''
}

function openEdit(a: CloudAccount) {
  editing.value = a.id
  form.value = { provider: a.provider as Provider, name: a.name, access_key_id: '', secret: '', tenancy_ocid: '', fingerprint: '', home_region: '', regions: a.regions.join(', '),
    budget: a.budget_cents ? String(a.budget_cents / 100) : '',
    balance_alert: a.balance_alert_cents ? String(a.balance_alert_cents / 100) : '', cost_interval_h: a.cost_interval_h,
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
    fieldErrors.value = { password: t('请输入当前登录密码以确认') }
    return
  }
  const body: CloudAccountInput = {
    name: f.name.trim(),
    regions: f.regions.split(/[\s,，]+/).filter(Boolean),
    budget: f.budget.trim() === '' ? 0 : Number(f.budget),
    balance_alert: f.balance_alert.trim() === '' ? 0 : Number(f.balance_alert),
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

// 最小只读权限（设计 44.3）：在 IAM 中创建只读用户，附加这条策略。
// 【安全】Condition 限定只有面板服务器的公网 IP 能使用这对密钥（修订第 146 条）：密钥泄露后在别处无法调用
const awsPolicy = JSON.stringify({
  Version: '2012-10-17',
  Statement: [{
    Effect: 'Allow',
    Action: ['ce:GetCostAndUsage', 'ce:GetCostForecast', 'ec2:DescribeInstances', 'ec2:DescribeRegions',
      'lightsail:GetRegions', 'lightsail:GetInstances', 'lightsail:GetInstanceMetricData'],
    Resource: '*',
    Condition: { IpAddress: { 'aws:SourceIp': ['面板服务器公网IP/32'] } },
  }],
}, null, 2)

// 实例所属账户的 CDT 额度池（修订第 186 条）
const cdtPool = (i: CloudInstance) => accounts.value.find((a) => a.id === i.account_id)?.traffic_pools.find((p) => p.key === i.cdt_pool)

// CDT 免费额度池（修订第 181 条）
const poolPct = (p: CloudTrafficPool) => (p.limit_bytes ? (p.used_bytes / p.limit_bytes) * 100 : 0)
const poolTip = (p: CloudTrafficPool) =>
  t('{month} 自然月（北京时间）；只计按流量计费的 BGP（多线）流量，超出免费额度的部分按量收费', { month: p.period_start.slice(0, 7) })

const intervalCost: Record<number, string> = { 6: '2.4', 12: '1.2', 24: '0.6' }

// 阿里云：在 RAM 中创建用户，只授予这四个系统只读策略；CDT 只读用于读取 CDT 公网流量（修订第 181 条），没有时只是不显示
const aliyunPolicies = 'AliyunBSSReadOnlyAccess\nAliyunECSReadOnlyAccess\nAliyunSWASReadOnlyAccess\nAliyunCDTReadOnlyAccess'
// 每个策略的用途与没有它的影响（修订第 202 条）：走 CDT 的实例本身是 ECS 实例，要 ECS 只读才能列出来
const aliyunPolicyUses: [string, string][] = [
  ['AliyunECSReadOnlyAccess', '列出 ECS 实例与到期时间，包括按流量计费、走 CDT 的实例。没有它，实例列表中看不到任何 ECS / CDT 实例'],
  ['AliyunCDTReadOnlyAccess', '读取 CDT 公网流量与免费额度用量（中国内地 20 GiB、非中国内地 200 GiB / 月）。没有它，CDT 实例显示“本月暂无数据”'],
  ['AliyunSWASReadOnlyAccess', '列出轻量应用服务器实例与流量包。不用轻量应用服务器时可以不授予'],
  ['AliyunBSSReadOnlyAccess', '读取本月费用与账户余额。没有它，只是不显示费用'],
]

// 腾讯云：自定义策略，只列出用到的只读接口（修订第 147 条）。也可改用预设策略 QcloudCVMReadOnlyAccess、
// QcloudLighthouseReadOnlyAccess，但费用与余额仍需下面两条 billing 接口，所以推荐直接用这条自定义策略。
// 【安全】condition 限定只有面板服务器的公网 IP 能使用这对密钥：密钥泄露后在别处无法调用
const tencentPolicy = JSON.stringify({
  version: '2.0',
  statement: [{
    effect: 'allow',
    action: ['billing:DescribeBillSummaryByProduct', 'billing:DescribeAccountBalance', 'cvm:DescribeRegions',
      'cvm:DescribeInstances', 'lighthouse:DescribeRegions', 'lighthouse:DescribeInstances',
      'lighthouse:DescribeInstancesTrafficPackages'],
    resource: ['*'],
    condition: { ip_equal: { 'qcs:ip': ['面板服务器公网IP/32'] } },
  }],
}, null, 2)
// 每条权限的用途（添加账户时列出，用户知道为什么需要它们）
const tencentActions: [string, string][] = [
  ['billing:DescribeBillSummaryByProduct', '本月账单'],
  ['billing:DescribeAccountBalance', '账户余额'],
  ['cvm:DescribeRegions、cvm:DescribeInstances', '云服务器 CVM 实例与到期'],
  ['lighthouse:DescribeRegions、lighthouse:DescribeInstances', '轻量应用服务器实例与到期'],
  ['lighthouse:DescribeInstancesTrafficPackages', '轻量流量包额度与用量'],
]
// Oracle Cloud：把 API 用户放进一个组，在根区间为该组添加这四条只读策略
const ociPolicy = ['read usage-reports', 'inspect compartments', 'read instance-family', 'read virtual-network-family']
  .map((x) => `Allow group <组名> to ${x} in tenancy`).join('\n')
// 策略中的占位文字按界面语言显示（复制到云控制台后由用户替换）
const policyFor = (p: Provider) => (p === 'aws' ? awsPolicy : p === 'oci' ? ociPolicy
  : p.startsWith('tencent') ? tencentPolicy : aliyunPolicies).replaceAll('面板服务器公网IP', t('面板服务器公网IP')).replaceAll('<组名>', t('<组名>'))

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

// 自动流量校准（设计 44.5）：默认关闭；云厂商数据有数小时延迟，且计费周期、口径需与节点一致
async function toggleCalibrate(i: CloudInstance, on: boolean) {
  if (on && !confirm(t('每天用“{inst}”的云厂商流量校准节点“{node}”？', { inst: i.name || i.instance_id, node: nodeName(i.server_id) }) + '\n' +
    t('请确认节点的流量重置日、计费时区与计费方式（入站 + 出站或只算出站）和云厂商一致；周期不一致时不会校准。'))) return
  linkError.value = ''
  try {
    const v = await setCloudAutoCalibrate(i.id, on)
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
      <h1>{{ t('云平台') }}</h1>
      <button v-if="editing === null" type="button" @click="openNew">{{ t('添加账户') }}</button>
    </div>
    <!-- 已有账户后不再显示介绍（修订第 185 条） -->
    <p v-if="loaded && !accounts.length" class="muted small intro">
      {{ t('接入你自己的云账户，查看本月费用与预估、云厂商口径的实例与流量包用量。只需只读权限；凭证加密保存在本面板，面板直接请求云厂商的官方接口，数据不经过任何第三方。目前支持 AWS、阿里云与腾讯云（国内站、国际站）、Oracle Cloud。') }}
    </p>
    <p v-if="error" class="banner">{{ error }}</p>

    <!-- 添加 / 编辑 -->
    <form v-if="editing !== null" class="panel editor" novalidate @submit.prevent="save">
      <h2>{{ editing === 'new' ? t('添加云账户') : t('编辑 {name}', { name: form.name }) }}</h2>
      <!-- 服务商较多，用下拉而不是分段按钮，窄屏也不溢出 -->
      <label v-if="editing === 'new'" class="provider-pick small">{{ t('服务商') }}
        <select v-model="form.provider" @change="showPolicy = false">
          <option v-for="(p, key) in providers" :key="key" :value="key">{{ t(p.label) }}</option>
        </select>
      </label>
      <!-- AWS 完整流程（修订第 146 条）：建用户 → 建策略（限定面板 IP）→ 挂策略 → 建密钥 -->
      <div v-if="form.provider === 'aws'" class="muted small key-steps">
        <p>{{ t('完整流程：') }}</p>
        <ol>
          <li><b>{{ t('建用户') }}</b>{{ t('：IAM → Users → Create user，用户名如') }} <code>vpsmon-readonly</code>，<b>{{ t('不要') }}</b>{{ t('勾选“提供用户对 AWS 管理控制台的访问”。') }}</li>
          <li><b>{{ t('建策略') }}</b>{{ t('：IAM → Policies → Create policy → JSON 标签页，粘贴下面的策略，把') }} <code>{{ t('面板服务器公网IP') }}</code> {{ t('换成面板服务器的真实公网 IP，命名') }} <code>vpsmon-readonly</code>。
            <button type="button" class="text" @click="showPolicy = !showPolicy">{{ showPolicy ? t('收起策略') : t('查看策略') }}</button></li>
          <li><b>{{ t('挂策略') }}</b>{{ t('：回到 Users → 点') }} <code>vpsmon-readonly</code> {{ t('→ Add permissions → Attach policies directly → 搜') }} <code>vpsmon-readonly</code> {{ t('→ 勾选。') }}</li>
          <li><b>{{ t('建密钥') }}</b>{{ t('：该用户 → Security credentials → Create access key → 用例选“在 AWS 外部运行的应用程序” → 建完立刻保存 Access Key ID 与 Secret Access Key（Secret 只显示一次）。') }}</li>
        </ol>
        <p>
          {{ t('策略中的 IP 限制让这对密钥只能从面板服务器使用，泄露后在别处无法调用。面板若通过 IPv6 访问 AWS，把 IPv6 地址（/128）也加进 aws:SourceIp；面板服务器换 IP 后要同步修改策略，否则同步会提示权限不足。') }}
        </p>
      </div>
      <!-- 阿里云（修订第 202 条）：逐条列出要授予的系统只读策略、用途与没有它的影响；查看 CDT 实例至少需要 ECS 与 CDT 两条 -->
      <div v-else-if="form.provider.startsWith('aliyun')" class="muted small key-steps">
        <p>
          {{ t('在{site} RAM 控制台创建一个专用用户，在“权限管理 → 新增授权”中授予下面的系统策略（都是只读，不能开关机或修改资源），再为它创建 AccessKey。国内站与国际站是两套账号，请按账号所在站点选择。',
            { site: form.provider === 'aliyun_cn' ? t('阿里云（aliyun.com）') : t('阿里云国际站（alibabacloud.com）') }) }}
        </p>
        <ul>
          <li v-for="[p, use] in aliyunPolicyUses" :key="p"><code>{{ p }}</code>{{ t('：') }}{{ t(use) }}</li>
        </ul>
        <p><b>{{ t('要看 CDT 实例') }}</b>{{ t('：至少授予') }} <code>AliyunECSReadOnlyAccess</code>{{ t('（列出实例）与') }} <code>AliyunCDTReadOnlyAccess</code>{{ t('（流量用量）。给已添加的账户补授权后，点账户上的“立即同步”。') }}
          <button type="button" class="text" @click="showPolicy = !showPolicy">{{ showPolicy ? t('收起') : t('复制策略名称') }}</button></p>
      </div>
      <p v-else-if="form.provider === 'oci'" class="muted small">
        {{ t('建议创建一个专用用户并加入一个组，在根区间为该组添加下面的只读策略，再为该用户添加 API 密钥（不带密码的 RSA 私钥）。出站流量按租户统计，对照每月 10 TB 免费额度。') }}
        <button type="button" class="text" @click="showPolicy = !showPolicy">{{ showPolicy ? t('收起策略') : t('查看最小权限策略') }}</button>
      </p>
      <!-- 腾讯云完整流程（修订第 147 条）：建策略（限定面板 IP）→ 建子用户（只开编程访问）→ 关联策略 → 保存密钥 -->
      <div v-else class="muted small key-steps">
        <p>
          {{ t('在{site}访问管理 CAM 中操作。国内站与国际站是两套账号，请按账号所在站点选择。完整流程：',
            { site: form.provider === 'tencent_cn' ? t('腾讯云（cloud.tencent.com）') : t('腾讯云国际站（tencentcloud.com）') }) }}
        </p>
        <ol>
          <li><b>{{ t('建策略') }}</b>{{ t('：CAM → 策略 → 新建自定义策略 → 按策略语法创建 → 空白模板，粘贴下面的策略，把') }} <code>{{ t('面板服务器公网IP') }}</code>
            {{ t('换成面板服务器的真实公网 IP，命名') }} <code>vpsmon-readonly</code>。
            <button type="button" class="text" @click="showPolicy = !showPolicy">{{ showPolicy ? t('收起策略') : t('查看策略') }}</button></li>
          <li><b>{{ t('建子用户') }}</b>{{ t('：CAM → 用户 → 新建用户 → 自定义创建 → 可访问资源并接收消息，访问方式') }}<b>{{ t('只勾“编程访问”') }}</b>{{ t('，不勾“腾讯云控制台访问”。') }}</li>
          <li><b>{{ t('关联策略') }}</b>{{ t('：设置用户权限时搜索') }} <code>vpsmon-readonly</code> {{ t('并勾选（不要关联 AdministratorAccess 等管理权限）。') }}</li>
          <li><b>{{ t('保存密钥') }}</b>{{ t('：创建完成页面显示 SecretId 与 SecretKey，立刻保存（SecretKey 只显示一次）。') }}</li>
        </ol>
        <p>{{ t('策略需要开通的权限（都是只读）：') }}</p>
        <ul>
          <li v-for="[a, use] in tencentActions" :key="a"><code>{{ a }}</code>{{ t('：') }}{{ t(use) }}</li>
        </ul>
        <p>
          {{ t('也可以改用预设策略 QcloudCVMReadOnlyAccess、QcloudLighthouseReadOnlyAccess，但费用与余额仍需上面两条 billing 权限，推荐直接用这条自定义策略。IP 限制让密钥泄露后在别处无法使用；面板服务器换 IP 后要同步修改策略，否则同步会提示权限不足。') }}
        </p>
      </div>
      <CommandBlock v-if="showPolicy" :command="policyFor(form.provider)" plain class="policy" />
      <!-- 密钥从哪里获取（用户最常问的问题）：一行步骤 + 控制台链接 -->
      <p class="key-help small">
        <b>{{ t('{label} 从哪里获取：', { label: t(info.idLabel) }) }}</b>{{ t(info.keySteps) }}{{ t('。') }}
        <a :href="info.keyURL" target="_blank" rel="noopener noreferrer">{{ t('打开控制台') }}</a>
        <br><span class="muted">{{ t('密钥只显示一次，请立即复制；不要使用主账号（根账号）的密钥。') }}</span>
      </p>
      <div class="fields">
        <label>{{ t('名称') }}
          <input v-model="form.name" maxlength="64" :placeholder="t('如 {name} 主账户', { name: t(info.label) })" />
          <small v-if="fieldErrors.name" class="err">{{ fieldErrors.name }}</small>
        </label>
        <label>{{ t(info.idLabel) }}
          <input v-model="form.access_key_id" autocomplete="off" spellcheck="false"
            :placeholder="editing === 'new' ? info.idPlaceholder : t('留空保持不变')" />
          <small v-if="fieldErrors.access_key_id" class="err">{{ fieldErrors.access_key_id }}</small>
        </label>
        <template v-if="form.provider === 'oci'">
          <label>{{ t('租户 OCID') }}
            <input v-model="form.tenancy_ocid" autocomplete="off" spellcheck="false"
              :placeholder="editing === 'new' ? 'ocid1.tenancy.oc1..…' : t('留空保持不变')" />
            <small v-if="fieldErrors.tenancy_ocid" class="err">{{ fieldErrors.tenancy_ocid }}</small>
          </label>
          <label>{{ t('指纹（fingerprint）') }}
            <input v-model="form.fingerprint" autocomplete="off" spellcheck="false"
              :placeholder="editing === 'new' ? t('aa:bb:…（16 组）') : t('留空保持不变')" />
            <small v-if="fieldErrors.fingerprint" class="err">{{ fieldErrors.fingerprint }}</small>
          </label>
          <label>{{ t('主区域（home region）') }}
            <input v-model="form.home_region" autocomplete="off" spellcheck="false"
              :placeholder="editing === 'new' ? t('如 ap-tokyo-1') : t('留空保持不变')" />
            <small v-if="fieldErrors.home_region" class="err">{{ fieldErrors.home_region }}</small>
          </label>
        </template>
        <label :class="{ wide: form.provider === 'oci' }">{{ t(info.secretLabel) }}
          <!-- 私钥是多行 PEM：用文本框；不进浏览器自动填充 -->
          <textarea v-if="form.provider === 'oci'" v-model="form.secret" rows="4" autocomplete="off" spellcheck="false" class="mono pem"
            :placeholder="editing === 'new' ? '-----BEGIN PRIVATE KEY-----\n…\n-----END PRIVATE KEY-----' : t('留空保持不变')" />
          <input v-else v-model="form.secret" type="password" autocomplete="new-password" spellcheck="false"
            :placeholder="editing === 'new' ? '' : t('留空保持不变')" />
          <small v-if="fieldErrors.secret" class="err">{{ fieldErrors.secret }}</small>
          <small v-else-if="fieldErrors.credential" class="err">{{ fieldErrors.credential }}</small>
        </label>
        <label class="wide">{{ t('区域') }}
          <input v-model="form.regions" spellcheck="false" :placeholder="t('留空表示全部区域；填区域 ID（不是实例名称），如 {example}', { example: info.regionExample })" />
          <small v-if="fieldErrors.regions" class="err">{{ fieldErrors.regions }}</small>
        </label>
        <label>{{ t('月度预算（{cur}）', { cur: info.currency }) }}
          <input v-model="form.budget" inputmode="decimal" :placeholder="t('不设')" />
          <small v-if="fieldErrors.budget" class="err">{{ fieldErrors.budget }}</small>
        </label>
        <label v-if="info.hasBalance">{{ t('余额低于（{cur}）时提醒', { cur: info.currency }) }}
          <input v-model="form.balance_alert" inputmode="decimal" :placeholder="t('不提醒')" />
          <small v-if="fieldErrors.balance_alert" class="err">{{ fieldErrors.balance_alert }}</small>
        </label>
        <label>{{ t('费用同步间隔') }}
          <select v-model.number="form.cost_interval_h">
            <option :value="6">{{ t('每 6 小时') }}</option>
            <option :value="12">{{ t('每 12 小时') }}</option>
            <option :value="24">{{ t('每天') }}</option>
          </select>
          <small v-if="info.paidCostApi" class="muted">{{ t('AWS Cost Explorer 每次调用收费 0.01 美元，此频率每月约 {n} 美元', { n: intervalCost[form.cost_interval_h] }) }}</small>
          <small v-else class="muted">{{ t('账单与余额接口免费') }}</small>
        </label>
        <label class="check"><input v-model="form.sync_cost" type="checkbox" />{{ t('同步费用') }}</label>
        <label class="check"><input v-model="form.sync_traffic" type="checkbox" />{{ t('同步流量包（{name}，每小时）', { name: t(info.trafficName) }) }}</label>
        <label class="check"><input v-model="form.enabled" type="checkbox" />{{ t('启用') }}</label>
        <label v-if="changingCred">{{ t('当前登录密码') }}
          <input v-model="form.password" type="password" autocomplete="current-password" />
          <small v-if="fieldErrors.password" class="err">{{ fieldErrors.password }}</small>
          <small v-else class="muted">{{ t('添加或更换凭证前确认身份') }}</small>
        </label>
      </div>
      <p v-if="formError" class="err small">{{ formError }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving">{{ saving ? t('保存中…') : t('保存') }}</button>
        <button type="button" class="secondary" @click="editing = null">{{ t('取消') }}</button>
      </div>
    </form>

    <EmptyState v-if="loaded && !accounts.length && editing === null" :text="t('还没有云账户。添加后面板会定时同步费用、实例与流量包用量。')">
      <button type="button" @click="openNew">{{ t('添加账户') }}</button>
    </EmptyState>

    <!-- 账户 -->
    <ul v-if="accounts.length" class="accounts">
      <li v-for="a in accounts" :key="a.id" class="panel account" :class="{ off: !a.enabled }">
        <div class="head">
          <div class="title">
            <span class="type">{{ t(providerOf(a.provider).label) }}</span><b>{{ a.name }}</b>
            <span class="muted small mono">{{ a.credential_hint }}</span>
          </div>
          <div class="acts">
            <button type="button" class="secondary" :disabled="a.syncing || !a.enabled" @click="syncNow(a)">{{ t('立即同步') }}</button>
            <button v-if="isAliyun(a)" type="button" class="text" :disabled="checks[a.id]?.busy" @click="checkNow(a)">
              {{ checks[a.id]?.busy ? t('检查中…') : t('检查权限') }}
            </button>
            <button type="button" class="text" @click="toggle(a)">{{ a.enabled ? t('停用') : t('启用') }}</button>
            <button type="button" class="text" @click="openEdit(a)">{{ t('编辑') }}</button>
            <button type="button" class="text danger-text" @click="deleting = deleting === a.id ? null : a.id; deleteError = ''">{{ t('删除') }}</button>
          </div>
        </div>
        <p class="small status" :class="status(a).tone">{{ status(a).text }}</p>
        <p v-if="syncMsg[a.id]" class="small err">{{ syncMsg[a.id] }}</p>
        <div v-if="checks[a.id] && (checks[a.id].items.length || checks[a.id].error)" class="check">
          <div class="check-head">
            <b class="small">{{ t('权限检查') }}</b>
            <button type="button" class="text small" @click="closeCheck(a)">{{ t('收起') }}</button>
          </div>
          <p v-if="checks[a.id].error" class="small err">{{ checks[a.id].error }}</p>
          <ul v-if="checks[a.id].items.length">
            <li v-for="it in checks[a.id].items" :key="it.name">
              <span class="mark" :class="it.ok ? 'ok' : 'bad'">{{ it.ok ? '✓' : '✗' }}</span>
              <div>
                <div class="small"><b>{{ it.name }}</b> <code class="muted">{{ it.policy }}</code></div>
                <div class="small muted detail">{{ it.detail }}</div>
              </div>
            </li>
          </ul>
          <p v-if="checks[a.id].items.length" class="small muted">{{ t('✗ 的一项：在 RAM 控制台给这个子用户授予后面的系统策略，再点“立即同步”。要找的实例不在 ECS 的台数里：它在别的阿里云账号下，或不在查询的区域中（编辑账户，检查“区域”）。') }}</p>
        </div>

        <div class="stats">
          <div>
            <div class="muted small">{{ t('本月已产生') }}</div>
            <div class="big num">{{ a.current_cost ? fmtMoney(a.current_cost.amount_cents, a.current_cost.currency) : DASH }}</div>
          </div>
          <!-- 阿里云没有费用预测，显示账户余额 -->
          <div v-if="a.current_cost?.balance_cents != null">
            <div class="muted small">{{ t('账户余额') }}</div>
            <div class="big num" :class="{ bad: a.balance_alert_cents > 0 && a.current_cost.balance_cents < a.balance_alert_cents }">
              {{ fmtMoney(a.current_cost.balance_cents, a.current_cost.currency) }}
            </div>
            <div v-if="a.balance_alert_cents" class="muted small">{{ t('低于 {amount} 时提醒', { amount: fmtMoney(a.balance_alert_cents, a.current_cost.currency) }) }}</div>
          </div>
          <div v-else>
            <div class="muted small">{{ t('本月预估') }}</div>
            <div class="big num">{{ a.current_cost ? fmtMoney(a.current_cost.forecast_cents, a.current_cost.currency) : DASH }}</div>
          </div>
          <div>
            <div class="muted small">{{ t('预算') }}</div>
            <div class="big num">{{ a.budget_cents ? fmtMoney(a.budget_cents, a.current_cost?.currency ?? providerOf(a.provider).currency) : t('未设') }}</div>
          </div>
          <div>
            <div class="muted small">{{ t('实例') }}</div>
            <div class="big num">{{ a.instance_count }}</div>
          </div>
        </div>
        <div v-if="budgetPct(a) != null" class="budget">
          <UsageBar :pct="budgetPct(a)!" />
          <span class="small num" :class="budgetPct(a)! >= 100 ? 'bad' : budgetPct(a)! >= 80 ? 'warn' : 'muted'">
            {{ t(a.current_cost?.forecast_cents != null ? '预估占预算 {n}%' : '已产生占预算 {n}%', { n: Math.round(budgetPct(a)!) }) }}
          </span>
        </div>
        <!-- CDT 公网流量免费额度（修订第 181 条）：按账号计算，中国内地与非中国内地各一个池子 -->
        <div v-for="p in a.traffic_pools" :key="p.key" class="budget pool" :title="poolTip(p)">
          <span class="small pool-name">{{ t(p.name) }}</span>
          <UsageBar :pct="poolPct(p)" />
          <span class="small num" :class="poolPct(p) >= 95 ? 'bad' : poolPct(p) >= 80 ? 'warn' : 'muted'">
            {{ fmtTraffic(p.used_bytes, 'binary') }} / {{ fmtTraffic(p.limit_bytes, 'binary') }}
          </span>
        </div>
        <p v-if="a.pool_warning" class="small warn">{{ a.pool_warning }}</p>
        <p v-if="a.cost_warning" class="small warn">{{ a.cost_warning }}</p>
        <p class="muted small">
          {{ a.regions.length ? t('区域：{list}', { list: a.regions.join(t('、')) }) : t('全部已启用的区域') }} ·
          {{ t('费用每 {n} 小时', { n: a.cost_interval_h }) }}{{ a.sync_cost ? '' : t('（已关闭）') }} ·
          {{ t('{tz}，数据有数小时延迟', { tz: t(providerOf(a.provider).billingTZ) }) }}
        </p>

        <form v-if="deleting === a.id" class="confirm" @submit.prevent="remove(a)">
          <span class="small">{{ t('删除账户及同步的费用、实例数据（不影响云上的资源）。输入当前登录密码确认：') }}</span>
          <input v-model="deletePassword" type="password" autocomplete="current-password" />
          <button type="submit" class="danger">{{ t('删除') }}</button>
          <span v-if="deleteError" class="small err">{{ deleteError }}</span>
        </form>
      </li>
    </ul>

    <!-- 实例 -->
    <section v-if="instances.length" class="section">
      <h2>{{ t('实例') }}</h2>
      <ul class="panel list">
        <li v-for="i in instances" :key="i.id">
          <div class="inst-main">
            <div class="ellipsis"><b>{{ i.name || i.instance_id }}</b>
              <span class="muted small meta">{{ kindNames[i.kind] ? t(kindNames[i.kind]) : i.kind }} · {{ i.region }} · {{ i.plan }}</span>
              <span v-if="i.cdt_pool" class="tag-cdt small" :title="t('公网按流量计费，流量走 CDT，使用账号的 CDT 免费额度')">CDT</span>
            </div>
            <div class="muted small ellipsis mono">{{ [i.public_ipv4, i.public_ipv6].filter(Boolean).join(' / ') || t('无公网 IP') }}</div>
          </div>
          <span class="small state">
            <span :class="i.state.toLowerCase() === 'running' ? 'ok' : 'muted'">{{ i.state }}</span>
            <span v-if="i.expire_at" class="expire" :class="expireTone(i)">{{ t('到期') }} {{ fmtDate(i.expire_at) }}</span>
          </span>
          <div v-if="i.traffic_limit_bytes" class="traffic">
            <UsageBar :pct="trafficPct(i)" />
            <span class="small num muted">{{ fmtBytes(i.traffic_used_bytes) }} / {{ fmtBytes(i.traffic_limit_bytes) }}</span>
          </div>
          <!-- CDT（修订第 186 条）：额度按账号与地域类别共用，这里显示所属池子的用量，不是这台实例自己的流量 -->
          <div v-else-if="i.cdt_pool && cdtPool(i)" class="traffic" :title="t('{pool}：账号内同类地域的按流量计费实例共用这份免费额度', { pool: t(cdtPool(i)!.name) })">
            <UsageBar :pct="poolPct(cdtPool(i)!)" />
            <span class="small num" :class="poolPct(cdtPool(i)!) >= 95 ? 'bad' : poolPct(cdtPool(i)!) >= 80 ? 'warn' : 'muted'">
              {{ t('{pool}（共用）', { pool: t(cdtPool(i)!.name.replace('CDT ', '')) }) }}{{ fmtTraffic(cdtPool(i)!.used_bytes, 'binary') }} / {{ fmtTraffic(cdtPool(i)!.limit_bytes, 'binary') }}
            </span>
          </div>
          <span v-else-if="i.cdt_pool" class="traffic muted small" :title="t('需要子用户有 AliyunCDTReadOnlyAccess（RAM → 用户 → 新增授权）；已授权但本月还没有 CDT 流量时也显示这一句')">{{ t('CDT：本月暂无数据') }}</span>
          <span v-else class="traffic muted small">{{ i.account_name }}</span>
          <!-- 与节点关联：已关联 / 建议关联（公网 IP 唯一匹配）/ 手动选择 -->
          <div v-if="i.kind !== 'oci_egress'" class="link small">
            <template v-if="i.server_id">
              {{ t('关联节点') }} <RouterLink :to="`/servers/${i.server_id}`">{{ nodeName(i.server_id) }}</RouterLink>
              <button type="button" class="text" @click="link(i, null)">{{ t('取消关联') }}</button>
              <label v-if="i.traffic_limit_bytes > 0" class="auto-cal">
                <input type="checkbox" :checked="i.auto_calibrate" @change="toggleCalibrate(i, ($event.target as HTMLInputElement).checked)" />
                {{ t('每天按云厂商流量校准节点') }}
              </label>
              <span v-if="i.auto_calibrate && i.calibrate_status" class="muted cal-status">
                {{ i.calibrate_status }}{{ i.calibrated_at > 0 ? `（${fmtTime(i.calibrated_at)}）` : '' }}
              </span>
            </template>
            <template v-else-if="i.suggested_server_id">
              {{ t('公网 IP 与节点') }} <b>{{ nodeName(i.suggested_server_id) }}</b> {{ t('相同') }}
              <button type="button" class="secondary small" @click="link(i, i.suggested_server_id)">{{ t('关联') }}</button>
            </template>
            <select v-else class="small" :aria-label="t('关联节点')" :value="''"
              @change="link(i, Number(($event.target as HTMLSelectElement).value) || null)">
              <option value="">{{ t('关联到节点…') }}</option>
              <option v-for="s in linkable" :key="s.id" :value="s.id">{{ s.name }}</option>
            </select>
          </div>
        </li>
      </ul>
      <p v-if="linkError" class="small err">{{ linkError }}</p>
      <p class="muted small">
        {{ t('Lightsail 流量为本月（UTC）入站 + 出站，按套餐额度计；阿里云、腾讯云轻量为流量包的额度与已用量；阿里云 ECS 公网按流量计费时标有 CDT，显示所属 CDT 免费额度池的用量（按账号计算、同类地域共用，不是单台实例的流量）。关联节点后，节点详情显示云厂商口径的状态、流量包与到期时间，可一键写入节点的到期日。') }}
      </p>
    </section>
  </main>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-4); max-width: 72ch; }
.editor { margin-bottom: var(--space-4); }
.editor h2 { margin-bottom: var(--space-2); }
.policy { margin-bottom: var(--space-3); }
.key-steps { margin-bottom: var(--space-3); max-width: 80ch; }
.key-steps p { margin: 0 0 var(--space-1); }
.key-steps ul { margin: 0 0 var(--space-2); padding-left: var(--space-5); }
.key-steps ol { margin: 0 0 var(--space-2); padding-left: var(--space-5); display: flex; flex-direction: column; gap: var(--space-1); }
.key-steps code { font-family: var(--font-mono); font-size: var(--font-xs); background: var(--surface-2); padding: 0 var(--space-1); border-radius: var(--radius-sm); }
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
.check { margin: var(--space-2) 0 var(--space-3); padding: var(--space-3); border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--surface-2); }
.check-head { display: flex; justify-content: space-between; align-items: center; }
.check ul { list-style: none; margin: var(--space-2) 0; padding: 0; display: grid; gap: var(--space-2); }
.check li { display: flex; gap: var(--space-2); align-items: flex-start; }
.check .mark { font-weight: var(--weight-strong); width: 1em; flex: none; }
.check .mark.ok { color: var(--ok); }
.check .mark.bad { color: var(--bad); }
.check .detail { word-break: break-word; }
.check > p { margin: 0; }
.stats { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--space-3); margin: var(--space-3) 0; }
.big { font-size: var(--font-lg); line-height: var(--line-lg); font-weight: var(--weight-strong); }
.budget { display: flex; align-items: center; gap: var(--space-3); margin-bottom: var(--space-2); }
.budget > :first-child { flex: 1; }
.pool > :first-child { flex: none; }
.pool > :nth-child(2) { flex: 1; }
.pool-name { min-width: 8em; }
.tag-cdt { margin-left: var(--space-2); padding: 0 var(--space-1); border-radius: var(--radius-sm); color: var(--accent);
  border: 1px solid color-mix(in srgb, var(--accent) 40%, transparent); }
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
.auto-cal { display: inline-flex; align-items: center; gap: var(--space-1); margin-left: var(--space-2); }
.auto-cal input { width: auto; }
.cal-status { display: block; margin-top: var(--space-1); }
</style>
