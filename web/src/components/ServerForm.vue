<script setup lang="ts">
// 节点表单（设计 27.2）：新建与编辑共用。基本信息、VPS 信息、安装选项三组；VPS 信息可以以后再填。
// 新建成功后由父组件跳到安装命令页；字段错误显示在对应输入框下（设计 43.6）。
// 编辑模式下提供删除（设计 19.5），需输入节点名称确认。
import { computed, reactive, ref } from 'vue'
import {
  ApiError, createServer, deleteServer, errorText, reauth, revokeAgentToken, updateServer, type CreateServerInput, type EnrollCodeView, type ServerView, type TrafficUnit,
} from '../api'
import { countryOptions } from '../countries'
import { bytesToGB, fmtBytes } from '../format'
import Flag from './Flag.vue'

const countries = countryOptions()

const props = defineProps<{
  /** 编辑的节点；为空时是新建 */
  server?: ServerView
}>()
const emit = defineEmits<{
  /** 新建成功，携带注册码与安装命令 */
  created: [view: EnrollCodeView]
  /** 编辑保存成功 */
  done: []
  /** 节点已删除 */
  deleted: []
  /** 取消，返回列表 */
  cancel: []
  /** Token 失效，需要重新输入 */
}>()
const editing = computed(() => !!props.server)

// 表单状态全部用字符串保存，提交时再转换，空值不提交（表示未填写）。编辑时用节点当前值填充。
const sv = props.server
const f = reactive({
  name: sv?.name ?? '', expected_hostname: sv?.expected_hostname ?? '', expected_ipv4: sv?.expected_ipv4 ?? '',
  expected_ipv6: sv?.expected_ipv6 ?? '', group: sv?.group ?? '', note: sv?.note ?? '',
  provider: sv?.provider ?? '', plan: sv?.plan ?? '', region: sv?.region ?? '', country: sv?.country ?? '',
  bandwidth_mbps: sv?.bandwidth_mbps ? String(sv.bandwidth_mbps) : '',
  // 字节 → 按节点口径的 GB / GiB（设计 5.8）；0 表示不限，显示为空
  traffic_limit_gb: sv?.traffic_limit_bytes ? String(+bytesToGB(sv.traffic_limit_bytes, sv.traffic_unit).toFixed(3)) : '',
  traffic_unit: sv?.traffic_unit ?? 'decimal', traffic_factor: String(sv?.traffic_factor ?? 1),
  traffic_reset_day: String(sv?.traffic_reset_day ?? 1), traffic_count_mode: sv?.traffic_count_mode ?? 'sum',
  traffic_timezone: sv?.traffic_timezone ?? '',
  price: sv?.price_cents ? (sv.price_cents / 100).toFixed(2) : '', currency: sv?.currency || 'USD',
  billing_period: sv?.billing_period ?? '', expire_date: sv?.expire_date ?? '',
  enroll_ttl: '24h', verify_mode: sv?.verify_mode ?? 'warn',
  report_interval_s: String(sv?.report_interval_s || 10),
})
// 常见的服务商计费时区（设计 5.4），也可以输入其他 IANA 时区名
const commonTimezones = ['UTC', 'Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Tokyo', 'Asia/Singapore', 'Asia/Seoul',
  'America/Los_Angeles', 'America/New_York', 'America/Chicago', 'Europe/London', 'Europe/Amsterdam', 'Europe/Berlin',
  'Europe/Paris', 'Australia/Sydney']
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const submitting = ref(false)
// 编辑时如果已经填过 VPS 信息，默认展开
const showVps = ref(!!(sv && (sv.provider || sv.plan || sv.region || sv.bandwidth_mbps || sv.traffic_limit_bytes || sv.price_cents || sv.expire_date)))
// 注册核对：新建时默认收起（大多数情况不需要）；编辑时如果填过预期值则展开
const showVerify = ref(!!(sv && (sv.expected_hostname || sv.expected_ipv4 || sv.expected_ipv6 || sv.verify_mode === 'strict')))

// Agent 自动采集的信息（只读展示，设计 27.2）：这些不需要、也不应该手动填写
const r = sv?.latest
const collected = computed(() => {
  if (!sv || sv.enroll_state !== 'enrolled') return []
  const disks = r?.disk ?? []
  return [
    { k: '主机名', v: sv.hostname || r?.system.hostname },
    { k: 'IPv4', v: sv.ipv4, hint: '注册时面板看到的来源地址' },
    { k: 'IPv6', v: sv.ipv6, hint: '注册时面板看到的来源地址' },
    { k: '系统', v: [r?.system.os, r?.system.os_version].filter(Boolean).join(' ') },
    { k: '内核 / 架构', v: [r?.system.kernel, r?.system.arch].filter(Boolean).join(' · ') },
    { k: 'CPU', v: r ? [r.system.cpu_model, `${r.cpu.cores} 核`].filter(Boolean).join(' · ') : '' },
    { k: '内存', v: r ? fmtBytes(r.memory.total) + (r.swap.total ? ` · Swap ${fmtBytes(r.swap.total)}` : '') : '' },
    { k: '磁盘', v: disks.length ? `${fmtBytes(disks.reduce((a, d) => a + d.total, 0))} · ${disks.map((d) => d.mount).join('、')}` : '' },
    { k: 'Agent', v: r?.agent_version },
  ]
})

// 选项文案（设计 1.2.4、1.2.5、27.2、27.6.3）
const countModes = [
  { v: 'sum', t: '入 + 出（RX + TX）' }, { v: 'max', t: '入、出取较大值' },
  { v: 'tx', t: '仅出站（TX）' }, { v: 'rx', t: '仅入站（RX）' },
]
const periods = [
  { v: '', t: '未填' }, { v: 'monthly', t: '月付' }, { v: 'quarterly', t: '季付' },
  { v: 'semiannually', t: '半年付' }, { v: 'annually', t: '年付' }, { v: 'biennially', t: '两年付' },
  { v: 'triennially', t: '三年付' }, { v: 'one_time', t: '一次性' },
]

function buildInput(): CreateServerInput {
  // 【注意】Vue 的 v-model 在 type="number" 的输入框上会把值转成数字（清空时为 ''），
  // 因此这里统一先转成字符串再处理，否则修改任何数字字段后提交会因 .trim() 报错而无反应
  const text = (v: string | number) => String(v ?? '').trim() || undefined
  const num = (v: string | number) => (String(v ?? '').trim() === '' ? undefined : Number(v))
  const price = num(f.price)
  return {
    name: f.name.trim(),
    expected_hostname: text(f.expected_hostname),
    expected_ipv4: text(f.expected_ipv4),
    expected_ipv6: text(f.expected_ipv6),
    group: text(f.group),
    note: text(f.note),
    provider: text(f.provider),
    plan: text(f.plan),
    region: text(f.region),
    country: f.country || undefined,
    bandwidth_mbps: num(f.bandwidth_mbps),
    report_interval_s: num(f.report_interval_s) as CreateServerInput['report_interval_s'],
    traffic_limit_gb: num(f.traffic_limit_gb),
    traffic_reset_day: num(f.traffic_reset_day),
    traffic_timezone: f.traffic_timezone.trim(),
    traffic_unit: f.traffic_unit as TrafficUnit,
    traffic_factor: num(f.traffic_factor),
    traffic_count_mode: f.traffic_count_mode as CreateServerInput['traffic_count_mode'],
    price,
    currency: price ? text(f.currency) : undefined, // 不填价格时不提交币种，避免无意义的数据
    billing_period: (f.billing_period || undefined) as CreateServerInput['billing_period'],
    expire_date: text(f.expire_date),
    enroll_ttl: f.enroll_ttl as CreateServerInput['enroll_ttl'],
    verify_mode: f.verify_mode as CreateServerInput['verify_mode'],
  }
}

async function submit() {
  fieldErrors.value = {}
  formError.value = ''
  if (!f.name.trim()) {
    fieldErrors.value = { name: '请填写名称' }
    return
  }
  submitting.value = true
  try {
    if (props.server) {
      await updateServer(props.server.id, buildInput())
      emit('done')
    } else {
      emit('created', await createServer(buildInput()))
    }
  } catch (e) {
    if (e instanceof ApiError && e.details.length) {
      // 422 / 409：在对应字段下显示错误；VPS 信息组有错误时自动展开
      fieldErrors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
      const verifyFields = ['expected_hostname', 'expected_ipv4', 'expected_ipv6', 'verify_mode']
      if (e.details.some((d) => verifyFields.includes(d.field))) showVerify.value = true
      if (e.details.some((d) => ![...verifyFields, 'name', 'country', 'group', 'note'].includes(d.field))) {
        showVps.value = true
      }
    } else {
      formError.value = errorText(e)
    }
  } finally {
    submitting.value = false
  }
}

// 吊销 Agent Token：Token 泄露时立即切断上报（设计 17.2）。敏感操作，重新输入密码确认。
const revokePassword = ref('')
const revokeError = ref('')
const revokeDone = ref(false)
const revoking = ref(false)
async function revoke() {
  if (!props.server || !revokePassword.value) return
  revoking.value = true
  revokeError.value = ''
  try {
    await reauth(revokePassword.value)
    await revokeAgentToken(props.server.id)
    revokeDone.value = true
  } catch (e) {
    revokeError.value = errorText(e)
  } finally {
    revokePassword.value = ''
    revoking.value = false
  }
}

// 删除是敏感操作：重新输入密码确认（设计 17.4），同时防止误删。
const deletePassword = ref('')
const deleteError = ref('')
const deleting = ref(false)
async function remove() {
  if (!props.server || !deletePassword.value) return
  deleting.value = true
  deleteError.value = ''
  try {
    await reauth(deletePassword.value)
    await deleteServer(props.server.id)
    emit('deleted')
  } catch (e) {
    deleteError.value = errorText(e)
  } finally {
    deletePassword.value = ''
    deleting.value = false
  }
}
</script>

<template>
  <form class="panel" novalidate @submit.prevent="submit">
    <h2>{{ editing ? '编辑节点' : '新建节点' }}</h2>
    <p v-if="!editing" class="muted">
      只需填写 Agent 采集不到的信息。主机名、IP、系统、CPU、内存、磁盘等在安装 Agent 后自动采集，无需填写。
      保存后节点显示为“待安装”，页面会给出在主机上执行的安装命令。
    </p>

    <fieldset>
      <legend>基本信息</legend>
      <div class="fields">
        <label class="wide">名称 *
          <input v-model="f.name" placeholder="如 DMIT-HK" autofocus />
          <small v-if="fieldErrors.name" class="err">{{ fieldErrors.name }}</small>
        </label>
        <label>国家 / 地区
          <div class="row">
            <Flag :code="f.country" />
            <select v-model="f.country">
              <option value="">未选择</option>
              <optgroup label="常用">
                <option v-for="c in countries.common" :key="c.code" :value="c.code">{{ c.name }}（{{ c.code }}）</option>
              </optgroup>
              <optgroup label="全部">
                <option v-for="c in countries.all" :key="c.code" :value="c.code">{{ c.name }}（{{ c.code }}）</option>
              </optgroup>
            </select>
          </div>
          <small v-if="fieldErrors.country" class="err">{{ fieldErrors.country }}</small>
          <small v-else class="muted">节点卡片上显示国旗</small>
        </label>
        <label>分组
          <input v-model="f.group" placeholder="如 香港、落地" />
          <small v-if="fieldErrors.group" class="err">{{ fieldErrors.group }}</small>
        </label>
        <!-- 采样间隔（设计 4.2、6.1）：随上报响应下发，Agent 下一轮即生效；间隔越长，Agent 自身的流量与 CPU 越少 -->
        <label>采样间隔
          <select v-model="f.report_interval_s">
            <option v-for="s in [5, 10, 15, 30, 60]" :key="s" :value="String(s)">{{ s }} 秒{{ s === 10 ? '（默认）' : '' }}</option>
          </select>
          <small v-if="fieldErrors.report_interval_s" class="err">{{ fieldErrors.report_interval_s }}</small>
          <small v-else class="muted">间隔越长越省流量；离线判定随之放宽</small>
        </label>
        <label class="wide">备注
          <input v-model="f.note" />
          <small v-if="fieldErrors.note" class="err">{{ fieldErrors.note }}</small>
        </label>
      </div>
    </fieldset>

    <!-- Agent 自动采集（只读）：让用户清楚哪些不需要填写（设计 27.2） -->
    <fieldset v-if="collected.length">
      <legend>Agent 自动采集<span class="muted">（只读，随上报更新）</span></legend>
      <dl class="collected">
        <template v-for="c in collected" :key="c.k">
          <dt>{{ c.k }}</dt><dd :title="c.hint">{{ c.v || '—' }}</dd>
        </template>
      </dl>
    </fieldset>

    <fieldset>
      <legend>
        <button type="button" class="link" @click="showVps = !showVps">
          {{ showVps ? '▾' : '▸' }} VPS 信息<span class="muted">（服务商提供，Agent 采集不到；可以以后再填）</span>
        </button>
      </legend>
      <div v-show="showVps" class="fields">
        <label>供应商<input v-model="f.provider" placeholder="如 DMIT" /></label>
        <label>套餐<input v-model="f.plan" /></label>
        <label>城市 / 机房<input v-model="f.region" placeholder="如 东京、Equinix TY8" /></label>
        <label>带宽（Mbps）
          <input v-model="f.bandwidth_mbps" type="number" min="0" step="1" placeholder="如 1000" />
          <small v-if="fieldErrors.bandwidth_mbps" class="err">{{ fieldErrors.bandwidth_mbps }}</small>
          <small v-else class="muted">服务商标称的端口速率</small>
        </label>
        <label>月流量（{{ f.traffic_unit === 'binary' ? 'GiB' : 'GB' }}）
          <input v-model="f.traffic_limit_gb" type="number" min="0" step="any" placeholder="不填表示不限" />
          <small v-if="fieldErrors.traffic_limit_gb" class="err">{{ fieldErrors.traffic_limit_gb }}</small>
        </label>
        <label>计量单位
          <select v-model="f.traffic_unit">
            <option value="decimal">十进制：1 GB = 10⁹ 字节</option>
            <option value="binary">二进制：1 GiB = 2³⁰ 字节</option>
          </select>
          <small v-if="fieldErrors.traffic_unit" class="err">{{ fieldErrors.traffic_unit }}</small>
          <small v-else class="muted">与服务商面板一致；多数服务商用十进制</small>
        </label>
        <label>流量重置日
          <input v-model="f.traffic_reset_day" type="number" min="1" max="31" />
          <small v-if="fieldErrors.traffic_reset_day" class="err">{{ fieldErrors.traffic_reset_day }}</small>
        </label>
        <!-- 计费时区（设计 5.4）：服务商按自己的时区换月与计日；只影响之后的流量 -->
        <label>计费时区
          <input v-model="f.traffic_timezone" list="tz-list" placeholder="留空 = 面板时区" autocomplete="off" />
          <datalist id="tz-list">
            <option v-for="tz in commonTimezones" :key="tz" :value="tz" />
          </datalist>
          <small v-if="fieldErrors.traffic_timezone" class="err">{{ fieldErrors.traffic_timezone }}</small>
          <small v-else class="muted">如 America/Los_Angeles；修改后只影响之后的流量</small>
        </label>
        <label>计费模式
          <select v-model="f.traffic_count_mode">
            <option v-for="m in countModes" :key="m.v" :value="m.v">{{ m.t }}</option>
          </select>
        </label>
        <label>统计系数
          <input v-model="f.traffic_factor" type="number" min="0.5" max="2" step="0.01" />
          <small v-if="fieldErrors.traffic_factor" class="err">{{ fieldErrors.traffic_factor }}</small>
          <small v-else class="muted">统计值长期比服务商偏低 3% 时填 1.03；一般保持 1</small>
        </label>
        <label>续费价格
          <div class="row">
            <input v-model="f.price" type="number" min="0" step="0.01" />
            <input v-model="f.currency" class="currency" maxlength="3" placeholder="USD" />
          </div>
          <small v-if="fieldErrors.price || fieldErrors.currency" class="err">{{ fieldErrors.price || fieldErrors.currency }}</small>
        </label>
        <label>续费周期
          <select v-model="f.billing_period">
            <option v-for="p in periods" :key="p.v" :value="p.v">{{ p.t }}</option>
          </select>
        </label>
        <label>到期日期
          <input v-model="f.expire_date" type="date" />
          <small v-if="fieldErrors.expire_date" class="err">{{ fieldErrors.expire_date }}</small>
        </label>
      </div>
    </fieldset>

    <fieldset>
      <legend>
        <button type="button" class="link" @click="showVerify = !showVerify">
          {{ showVerify ? '▾' : '▸' }} 注册核对<span class="muted">（可选：填写预期值，Agent 注册时与实际值比对）</span>
        </button>
      </legend>
      <div v-show="showVerify" class="fields">
        <label>预期主机名
          <input v-model="f.expected_hostname" placeholder="hostname 命令的输出" />
          <small v-if="fieldErrors.expected_hostname" class="err">{{ fieldErrors.expected_hostname }}</small>
        </label>
        <label>预期 IPv4
          <input v-model="f.expected_ipv4" placeholder="103.1.2.3" inputmode="decimal" />
          <small v-if="fieldErrors.expected_ipv4" class="err">{{ fieldErrors.expected_ipv4 }}</small>
        </label>
        <label>预期 IPv6
          <input v-model="f.expected_ipv6" placeholder="2001:db8::1" />
          <small v-if="fieldErrors.expected_ipv6" class="err">{{ fieldErrors.expected_ipv6 }}</small>
        </label>
        <label>核对严格程度
          <select v-model="f.verify_mode">
            <option value="warn">不一致时仅提示</option>
            <option value="strict">不一致时拒绝注册</option>
          </select>
          <small class="muted">NAT、IPv6-only 主机建议仅提示</small>
        </label>
      </div>
    </fieldset>

    <fieldset v-if="!editing">
      <legend>安装选项</legend>
      <div class="fields">
        <label>注册码有效期
          <select v-model="f.enroll_ttl">
            <option value="1h">1 小时</option>
            <option value="24h">24 小时</option>
            <option value="7d">7 天</option>
          </select>
        </label>
      </div>
    </fieldset>

    <p v-if="formError" class="banner">{{ formError }}</p>
    <div class="actions">
      <button type="submit" :disabled="submitting">
        {{ submitting ? '保存中…' : editing ? '保存' : '保存并生成安装命令' }}
      </button>
      <button type="button" class="secondary" @click="emit('cancel')">取消</button>
    </div>

    <fieldset v-if="server && server.status !== 'pending'" class="danger">
      <legend>吊销 Agent Token</legend>
      <template v-if="revokeDone">
        <p>已吊销，主机上的 Agent 无法再上报，节点回到“待安装”，历史数据保留。</p>
        <p class="muted">恢复上报：在<RouterLink :to="`/servers/${server.id}/install`">安装命令页</RouterLink>生成注册码，
          在主机上执行“更换 Token”命令（<code>sudo vpsmon-agent rotate-token --enroll …</code>）。</p>
      </template>
      <template v-else>
        <p class="muted">怀疑 Token 泄露时使用：立即使该节点的 Token 失效，历史数据保留。之后凭新的注册码在主机上更换 Token 即可恢复。</p>
        <div class="row">
          <input v-model="revokePassword" type="password" placeholder="输入登录密码以确认" autocomplete="current-password"
            @keydown.enter.prevent="revoke" />
          <button type="button" class="danger-btn" :disabled="!revokePassword || revoking" @click="revoke">
            {{ revoking ? '吊销中…' : '吊销 Token' }}
          </button>
        </div>
        <small v-if="revokeError" class="err">{{ revokeError }}</small>
      </template>
    </fieldset>

    <fieldset v-if="server" class="danger">
      <legend>删除节点</legend>
      <p class="muted">删除后该节点的历史指标与流量统计一并删除，不可恢复；主机上的 Agent 将无法再上报。</p>
      <div class="row">
        <input v-model="deletePassword" type="password" placeholder="输入登录密码以确认" autocomplete="current-password"
          @keydown.enter.prevent="remove" />
        <button type="button" class="danger-btn" :disabled="!deletePassword || deleting" @click="remove">
          {{ deleting ? '删除中…' : `删除 ${server.name}` }}
        </button>
      </div>
      <small v-if="deleteError" class="err">{{ deleteError }}</small>
    </fieldset>
  </form>
</template>

<style scoped>
fieldset { border: 0; padding: 0; margin: var(--space-5) 0 0; }
legend { font-weight: var(--weight-strong); padding: 0; margin-bottom: var(--space-3); }
.fields { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: var(--space-3) var(--space-4); }
.fields label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.fields .wide { grid-column: 1 / -1; }
/* 全局样式中输入框是 flex: 1（用于横排），在纵向的字段里会被拉高，这里取消 */
.fields > label > input, .fields > label > select { flex: none; }
.fields .row select { flex: 1; }
.currency { flex: 0 0 72px; text-transform: uppercase; }
small { font-size: var(--font-sm); }
.err { color: var(--bad); }
.link { background: none; color: var(--text); padding: 0; font: inherit; font-weight: 600; }
.actions { display: flex; gap: var(--space-2); margin-top: var(--space-6); }
.collected { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--space-2) var(--space-4); margin: 0;
  padding: var(--space-3) var(--space-4); background: var(--surface-2); border-radius: var(--radius-sm); font-size: var(--font-sm); }
.collected dt { color: var(--text-muted); }
.collected dd { margin: 0; overflow-wrap: anywhere; }
.danger { margin-top: var(--space-7); padding-top: var(--space-4); border-top: 1px solid var(--border); }
.danger legend { color: var(--bad); }
.danger-btn { background: var(--bad); color: var(--on-accent); }
</style>
