<script setup lang="ts">
// 服务监控（设计 33.2）：面板按间隔检查网址（HTTP / HTTPS，始终校验证书）、TCP 端口与 DNS 解析；
// 连续失败达到次数判为异常并通知，恢复时再通知一次。异常在前；点开一行查看 24 小时 / 7 天的耗时与可用率。
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  ApiError, checkServiceMonitor, createServiceMonitor, deleteServiceMonitor, errorText, listServiceMonitors, serviceChecks,
  updateServiceMonitor, type ServiceMonitor, type ServiceMonitorInput,
} from '../api'
import { DASH, fmtAvailability, fmtLatency, fmtTime } from '../format'
import { state } from '../store'
import Chart, { type Series } from './Chart.vue'

const items = ref<ServiceMonitor[]>([])
const error = ref('')
const busy = ref<number | null>(null)

const kindNames: Record<string, string> = { http: '网址', tcp: 'TCP 端口', dns: 'DNS' }
const statusNames: Record<string, string> = { up: '正常', down: '异常', unknown: '未知' }
const placeholders: Record<string, string> = {
  http: 'https://example.com/health', tcp: 'db.example.com:5432', dns: 'example.com',
}

let timer: number | undefined
async function load() {
  try {
    items.value = await listServiceMonitors()
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '服务监控加载失败')
  }
  if (timer) clearTimeout(timer)
  timer = window.setTimeout(load, 30_000)
}
onMounted(load)
onBeforeUnmount(() => timer && clearTimeout(timer))

// ---- 添加与编辑 ----
type Form = { name: string; kind: 'http' | 'tcp' | 'dns'; target: string; server_id: number; interval_s: number; timeout_s: number
  fail_threshold: number; severity: 'critical' | 'warning'; expect_status: string; keyword: string; dns_type: 'A' | 'AAAA'
  dns_expect: string; enabled: boolean }
const blank = (): Form => ({ name: '', kind: 'http', target: '', server_id: 0, interval_s: 60, timeout_s: 10, fail_threshold: 2,
  severity: 'critical', expect_status: '', keyword: '', dns_type: 'A', dns_expect: '', enabled: true })
const form = reactive<Form>(blank())
const editing = ref<number | 'new' | null>(null)
const saving = ref(false)
const fieldErrors = ref<Record<string, string>>({})

function openNew() {
  Object.assign(form, blank())
  fieldErrors.value = {}
  editing.value = 'new'
}
function openEdit(m: ServiceMonitor) {
  Object.assign(form, {
    name: m.name, kind: m.kind, target: m.target, server_id: m.server_id, interval_s: m.interval_s, timeout_s: m.timeout_s,
    fail_threshold: m.fail_threshold, severity: m.severity, expect_status: m.expect_status, keyword: m.keyword,
    dns_type: m.dns_type === 'AAAA' ? 'AAAA' : 'A', dns_expect: m.dns_expect, enabled: m.enabled,
  })
  fieldErrors.value = {}
  editing.value = m.id
}

async function save() {
  saving.value = true
  fieldErrors.value = {}
  const body: ServiceMonitorInput = { ...form, name: form.name.trim(), target: form.target.trim() }
  try {
    if (editing.value === 'new') await createServiceMonitor(body)
    else if (typeof editing.value === 'number') await updateServiceMonitor(editing.value, body)
    editing.value = null
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.details.length) {
      fieldErrors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
    } else {
      fieldErrors.value = { _: errorText(e) }
    }
  } finally {
    saving.value = false
  }
}

async function recheck(m: ServiceMonitor) {
  busy.value = m.id
  try {
    const r = await checkServiceMonitor(m.id)
    items.value = items.value.map((x) => (x.id === r.id ? r : x))
    if (open.value === m.id) loadChart()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = null
  }
}

async function remove(m: ServiceMonitor) {
  if (!confirm(`删除服务监控“${m.name}”？检查记录会一并删除。`)) return
  try {
    await deleteServiceMonitor(m.id)
    if (open.value === m.id) open.value = null
    await load()
  } catch (e) {
    error.value = errorText(e)
  }
}

// ---- 展开：耗时曲线 ----
const open = ref<number | null>(null)
const range = ref<'24h' | '7d'>('24h')
const series = ref<Series[]>([])
const chartError = ref('')
async function loadChart() {
  if (open.value == null) return
  chartError.value = ''
  try {
    const pts = await serviceChecks(open.value, range.value)
    series.value = [{ name: '耗时', data: pts.map((p) => [p.ts * 1000, p.latency_ms ?? null]) }]
  } catch (e) {
    chartError.value = errorText(e)
  }
}
function toggle(m: ServiceMonitor) {
  open.value = open.value === m.id ? null : m.id
  series.value = []
  loadChart()
}
function setRange(r: '24h' | '7d') {
  range.value = r
  loadChart()
}

const nodeName = (id: number) => state.servers.find((s) => s.id === id)?.name ?? `#${id}`
const servers = computed(() => [...state.servers].filter((s) => s.status !== 'pending').sort((a, b) => a.name.localeCompare(b.name)))
const tone = (m: ServiceMonitor) => (!m.enabled ? 'muted' : m.status === 'down' ? 'bad' : m.status === 'up' ? 'ok' : 'warn')
const uptimeTone = (p: number | null) => (p == null ? 'muted' : p >= 99.9 ? 'ok' : p >= 99 ? 'warn' : 'bad')
</script>

<template>
  <div>
    <p class="muted small intro">
      面板按间隔检查网址（HTTP / HTTPS，始终校验证书）、TCP 端口与 DNS 解析。连续失败达到设定次数判为异常并通知，
      恢复时再通知一次；通知经“通知”中的渠道与 App 推送发送，遵守免打扰。检查记录保留 7 天。
    </p>
    <p v-if="error" class="banner">{{ error }}</p>
    <div class="toolbar">
      <button type="button" @click="openNew">添加服务</button>
    </div>

    <form v-if="editing !== null" class="panel form" novalidate @submit.prevent="save">
      <h3>{{ editing === 'new' ? '添加服务' : '编辑服务' }}</h3>
      <div class="grid">
        <label>名称<input v-model="form.name" maxlength="64" placeholder="官网、主库…" />
          <small v-if="fieldErrors.name" class="bad">{{ fieldErrors.name }}</small></label>
        <label>类型
          <select v-model="form.kind">
            <option value="http">网址（HTTP / HTTPS）</option>
            <option value="tcp">TCP 端口</option>
            <option value="dns">DNS 解析</option>
          </select></label>
        <label class="wide">目标<input v-model="form.target" spellcheck="false" :placeholder="placeholders[form.kind]" />
          <small v-if="fieldErrors.target" class="bad">{{ fieldErrors.target }}</small></label>
        <template v-if="form.kind === 'http'">
          <label>期望状态码<input v-model="form.expect_status" placeholder="默认 200-399" spellcheck="false" />
            <small v-if="fieldErrors.expect_status" class="bad">{{ fieldErrors.expect_status }}</small></label>
          <label>响应包含（可选）<input v-model="form.keyword" maxlength="100" placeholder="如 ok" />
            <small v-if="fieldErrors.keyword" class="bad">{{ fieldErrors.keyword }}</small></label>
        </template>
        <template v-if="form.kind === 'dns'">
          <label>记录类型
            <select v-model="form.dns_type"><option value="A">A（IPv4）</option><option value="AAAA">AAAA（IPv6）</option></select></label>
          <label>必须包含的 IP（可选）<input v-model="form.dns_expect" spellcheck="false" placeholder="203.0.113.10" />
            <small v-if="fieldErrors.dns_expect" class="bad">{{ fieldErrors.dns_expect }}</small></label>
        </template>
        <label>检查间隔（秒）<input v-model.number="form.interval_s" type="number" min="30" max="3600" />
          <small v-if="fieldErrors.interval_s" class="bad">{{ fieldErrors.interval_s }}</small></label>
        <label>超时（秒）<input v-model.number="form.timeout_s" type="number" min="1" max="30" />
          <small v-if="fieldErrors.timeout_s" class="bad">{{ fieldErrors.timeout_s }}</small></label>
        <label>连续失败几次告警<input v-model.number="form.fail_threshold" type="number" min="1" max="10" />
          <small v-if="fieldErrors.fail_threshold" class="bad">{{ fieldErrors.fail_threshold }}</small></label>
        <label>级别
          <select v-model="form.severity"><option value="critical">严重</option><option value="warning">警告</option></select></label>
        <label>关联节点（可选）
          <select v-model.number="form.server_id">
            <option :value="0">不关联</option>
            <option v-for="s in servers" :key="s.id" :value="s.id">{{ s.name }}</option>
          </select>
          <small class="muted">关联后，只能查看该节点的 App 设备也会收到推送</small></label>
        <label class="check"><input v-model="form.enabled" type="checkbox" />启用</label>
      </div>
      <p v-if="fieldErrors._" class="bad small">{{ fieldErrors._ }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving || !form.name.trim() || !form.target.trim()">{{ saving ? '保存并检查中…' : '保存' }}</button>
        <button type="button" class="secondary" @click="editing = null">取消</button>
      </div>
    </form>

    <ul v-if="items.length" class="panel list">
      <li v-for="m in items" :key="m.id" :class="{ off: !m.enabled }">
        <div class="row">
          <button type="button" class="main" :aria-expanded="open === m.id" @click="toggle(m)">
            <span class="name">
              <span class="dot" :class="tone(m)" aria-hidden="true">●</span>
              <strong>{{ m.name }}</strong>
              <span class="small" :class="tone(m)">{{ m.enabled ? statusNames[m.status] : '已停用' }}</span>
            </span>
            <span class="small muted target">{{ kindNames[m.kind] }} · {{ m.target }}<template v-if="m.server_id"> · {{ nodeName(m.server_id) }}</template></span>
            <span v-if="m.last_error && m.enabled" class="small bad">{{ m.last_error }}<template v-if="m.fails > 1">（连续 {{ m.fails }} 次）</template></span>
          </button>
          <div class="nums small">
            <span><span class="muted">耗时</span> <b class="num">{{ fmtLatency(m.last_latency_ms) }}</b></span>
            <span><span class="muted">24 小时</span> <b class="num" :class="uptimeTone(m.uptime_24h)">{{ fmtAvailability(m.uptime_24h) }}</b></span>
            <span><span class="muted">7 天</span> <b class="num" :class="uptimeTone(m.uptime_7d)">{{ fmtAvailability(m.uptime_7d) }}</b></span>
            <span class="muted">{{ m.checked_at ? `检查于 ${fmtTime(m.checked_at)}` : DASH }}</span>
          </div>
          <div class="ops">
            <button type="button" class="secondary small" :disabled="busy === m.id" @click="recheck(m)">{{ busy === m.id ? '检查中…' : '检查' }}</button>
            <button type="button" class="text small" @click="openEdit(m)">编辑</button>
            <button type="button" class="text small del" @click="remove(m)">删除</button>
          </div>
        </div>
        <div v-if="open === m.id" class="detail">
          <div class="segmented range" role="tablist">
            <button type="button" role="tab" :class="{ active: range === '24h' }" @click="setRange('24h')">24 小时</button>
            <button type="button" role="tab" :class="{ active: range === '7d' }" @click="setRange('7d')">7 天</button>
          </div>
          <p v-if="chartError" class="bad small">{{ chartError }}</p>
          <Chart v-else-if="series.length && series[0].data.length" title="响应耗时（失败处断开）" :series="series" :format="fmtLatency" hide-legend />
          <p v-else class="muted small">还没有检查记录。</p>
        </div>
      </li>
    </ul>
    <p v-else-if="editing === null" class="muted small">还没有监控的服务。</p>
  </div>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-3); max-width: 80ch; }
.toolbar { margin-bottom: var(--space-3); }
.form { padding: var(--space-4); margin-bottom: var(--space-4); }
.form h3 { margin: 0 0 var(--space-3); }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: var(--space-3); }
.grid label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.grid .wide { grid-column: 1 / -1; }
.grid .check { flex-direction: row; align-items: center; gap: var(--space-2); }
.grid .check input { width: auto; }
.actions { display: flex; gap: var(--space-2); margin-top: var(--space-3); }
.list { list-style: none; margin: 0; padding: 0 var(--space-4); }
.list > li + li { border-top: 1px solid var(--border); }
.list > li.off { opacity: 0.6; }
.row { display: flex; gap: var(--space-3); align-items: center; padding: var(--space-3) 0; flex-wrap: wrap; }
.main { flex: 1 1 260px; min-width: 0; display: flex; flex-direction: column; align-items: flex-start; gap: 2px; text-align: left;
  background: none; border: 0;
  padding: 0; color: inherit; cursor: pointer; font: inherit; overflow-wrap: anywhere; }
.name { display: flex; align-items: baseline; gap: var(--space-2); }
.dot { font-size: var(--font-xs); }
.nums { display: flex; flex-wrap: wrap; gap: var(--space-1) var(--space-4); align-items: baseline; }
.ops { display: flex; gap: var(--space-2); align-items: center; }
.detail { padding: 0 0 var(--space-4); }
.range { margin-bottom: var(--space-2); }
.ok { color: var(--ok); }
.warn { color: var(--warn); }
.bad, .del { color: var(--bad); }
.muted { color: var(--text-muted); }
</style>
