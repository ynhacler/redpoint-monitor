<script setup lang="ts">
// 节点表单（设计 27.2）：新建与编辑共用。基本信息、VPS 信息、安装选项三组；VPS 信息可以以后再填。
// 新建成功后由父组件跳到安装命令页；字段错误显示在对应输入框下（设计 43.6）。
// 编辑模式下提供删除（设计 19.5），需输入节点名称确认。
import { computed, reactive, ref } from 'vue'
import {
  ApiError, createServer, deleteServer, reauth, updateServer, UnauthorizedError,
  type CreateServerInput, type EnrollCodeView, type ServerView,
} from '../api'
import { countryOptions } from '../countries'
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
  unauthorized: []
}>()
const editing = computed(() => !!props.server)

// 表单状态全部用字符串保存，提交时再转换，空值不提交（表示未填写）。编辑时用节点当前值填充。
const sv = props.server
const f = reactive({
  name: sv?.name ?? '', expected_hostname: sv?.expected_hostname ?? '', expected_ipv4: sv?.expected_ipv4 ?? '',
  expected_ipv6: sv?.expected_ipv6 ?? '', group: sv?.group ?? '', note: sv?.note ?? '',
  provider: sv?.provider ?? '', plan: sv?.plan ?? '', region: sv?.region ?? '', country: sv?.country ?? '',
  // 字节 → 十进制 GB（设计 5.8）；0 表示不限，显示为空
  traffic_limit_gb: sv?.traffic_limit_bytes ? String(sv.traffic_limit_bytes / 1e9) : '',
  traffic_reset_day: String(sv?.traffic_reset_day ?? 1), traffic_count_mode: sv?.traffic_count_mode ?? 'sum',
  price: sv?.price_cents ? (sv.price_cents / 100).toFixed(2) : '', currency: sv?.currency || 'USD',
  billing_period: sv?.billing_period ?? '', expire_date: sv?.expire_date ?? '',
  enroll_ttl: '24h', verify_mode: sv?.verify_mode ?? 'warn',
})
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const submitting = ref(false)
// 编辑时如果已经填过 VPS 信息，默认展开
const showVps = ref(!!(sv && (sv.provider || sv.plan || sv.region || sv.traffic_limit_bytes || sv.price_cents || sv.expire_date)))

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
  const text = (v: string) => v.trim() || undefined
  const num = (v: string) => (v.trim() === '' ? undefined : Number(v))
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
    traffic_limit_gb: num(f.traffic_limit_gb),
    traffic_reset_day: num(f.traffic_reset_day),
    traffic_count_mode: f.traffic_count_mode as CreateServerInput['traffic_count_mode'],
    price,
    currency: price ? text(f.currency) : undefined, // 不填价格时不提交币种，避免无意义的数据
    billing_period: f.billing_period || undefined,
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
    if (e instanceof UnauthorizedError) {
      emit('unauthorized')
    } else if (e instanceof ApiError && e.details.length) {
      // 422 / 409：在对应字段下显示错误；VPS 信息组有错误时自动展开
      fieldErrors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
      if (e.details.some((d) => !['name', 'expected_hostname', 'expected_ipv4', 'expected_ipv6', 'group', 'note'].includes(d.field))) {
        showVps.value = true
      }
    } else if (e instanceof ApiError) {
      formError.value = e.requestId ? `${e.message}（编号 ${e.requestId}）` : e.message
    }
  } finally {
    submitting.value = false
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
    if (e instanceof UnauthorizedError) emit('unauthorized')
    else if (e instanceof ApiError) deleteError.value = e.details[0]?.message ?? e.message
  } finally {
    deletePassword.value = ''
    deleting.value = false
  }
}
</script>

<template>
  <form class="panel" novalidate @submit.prevent="submit">
    <h2>{{ editing ? '编辑节点' : '新建节点' }}</h2>
    <p v-if="!editing" class="muted">保存后节点显示为“待安装”，页面会给出在主机上执行的安装命令。</p>

    <fieldset>
      <legend>基本信息</legend>
      <div class="fields">
        <label class="wide">名称 *
          <input v-model="f.name" placeholder="如 DMIT-HK" autofocus />
          <small v-if="fieldErrors.name" class="err">{{ fieldErrors.name }}</small>
        </label>
        <label>主机名
          <input v-model="f.expected_hostname" placeholder="hostname 命令的输出" />
          <small v-if="fieldErrors.expected_hostname" class="err">{{ fieldErrors.expected_hostname }}</small>
          <small v-else class="muted">注册时核对</small>
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
        <label>IPv4
          <input v-model="f.expected_ipv4" placeholder="103.1.2.3" inputmode="decimal" />
          <small v-if="fieldErrors.expected_ipv4" class="err">{{ fieldErrors.expected_ipv4 }}</small>
        </label>
        <label>IPv6
          <input v-model="f.expected_ipv6" placeholder="2001:db8::1" />
          <small v-if="fieldErrors.expected_ipv6" class="err">{{ fieldErrors.expected_ipv6 }}</small>
        </label>
        <label class="wide">备注
          <input v-model="f.note" />
          <small v-if="fieldErrors.note" class="err">{{ fieldErrors.note }}</small>
        </label>
      </div>
    </fieldset>

    <fieldset>
      <legend>
        <button type="button" class="link" @click="showVps = !showVps">
          {{ showVps ? '▾' : '▸' }} VPS 信息<span class="muted">（流量套餐、费用，可以以后再填）</span>
        </button>
      </legend>
      <div v-show="showVps" class="fields">
        <label>供应商<input v-model="f.provider" placeholder="如 DMIT" /></label>
        <label>套餐<input v-model="f.plan" /></label>
        <label>地区<input v-model="f.region" placeholder="如 香港" /></label>
        <label>月流量（GB）
          <input v-model="f.traffic_limit_gb" type="number" min="0" step="any" placeholder="不填表示不限" />
          <small v-if="fieldErrors.traffic_limit_gb" class="err">{{ fieldErrors.traffic_limit_gb }}</small>
          <small v-else class="muted">按 1 GB = 10⁹ 字节计算</small>
        </label>
        <label>流量重置日
          <input v-model="f.traffic_reset_day" type="number" min="1" max="31" />
          <small v-if="fieldErrors.traffic_reset_day" class="err">{{ fieldErrors.traffic_reset_day }}</small>
        </label>
        <label>计费模式
          <select v-model="f.traffic_count_mode">
            <option v-for="m in countModes" :key="m.v" :value="m.v">{{ m.t }}</option>
          </select>
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
      <legend>安装选项</legend>
      <div class="fields">
        <label v-if="!editing">注册码有效期
          <select v-model="f.enroll_ttl">
            <option value="1h">1 小时</option>
            <option value="24h">24 小时</option>
            <option value="7d">7 天</option>
          </select>
        </label>
        <label>主机信息核对
          <select v-model="f.verify_mode">
            <option value="warn">不一致时仅提示</option>
            <option value="strict">不一致时拒绝注册</option>
          </select>
          <small class="muted">NAT、IPv6-only 主机建议仅提示</small>
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
.danger { margin-top: var(--space-7); padding-top: var(--space-4); border-top: 1px solid var(--border); }
.danger legend { color: var(--bad); }
.danger-btn { background: var(--bad); color: var(--on-accent); }
</style>
