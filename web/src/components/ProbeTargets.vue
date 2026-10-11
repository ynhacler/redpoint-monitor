<script setup lang="ts">
// 三网延迟的探测目标（设计 42.3.2）：内置三大运营商各省地址（勾选启用）与自定义目标。
// 修改后随下一份上报下发给各节点 Agent；启用的目标最多 64 个（Agent 的上限，设计 42.6）。
// 测试频率对所有节点生效：1～30 分钟，Agent 端不短于 1 分钟。
import { computed, onMounted, reactive, ref } from 'vue'
import {
  ApiError, createProbeTarget, deleteProbeTarget, errorText, getProbeSettings, listProbeTargets, putProbeSettings, updateProbeTarget,
  type ProbeSettings, type ProbeTarget, type ProbeTargetInput,
} from '../api'

const MAX_ENABLED = 64
const isps = [
  { key: 'ct', name: '电信' },
  { key: 'cu', name: '联通' },
  { key: 'cm', name: '移动' },
] as const
const ispName = (k: string) => isps.find((i) => i.key === k)?.name ?? k

const items = ref<ProbeTarget[]>([])
const error = ref('')
const busy = ref<number | null>(null)

// 测试频率（秒）
const intervals: ProbeSettings['interval_s'][] = [60, 120, 300, 600, 900, 1800]
const interval = ref<ProbeSettings['interval_s']>(300)
const savingInterval = ref(false)

async function load() {
  try {
    const [list, cfg] = await Promise.all([listProbeTargets(), getProbeSettings()])
    items.value = list
    interval.value = cfg.interval_s
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '探测目标加载失败')
  }
}

async function saveInterval() {
  savingInterval.value = true
  try {
    const r = await putProbeSettings({ interval_s: interval.value })
    interval.value = r.interval_s
    error.value = ''
  } catch (e) {
    error.value = errorText(e)
    await load()
  } finally {
    savingInterval.value = false
  }
}
// 每天的探测轮数，用于提示数据量
const roundsPerDay = computed(() => Math.round(86400 / interval.value))
onMounted(load)

const enabledCount = computed(() => items.value.filter((t) => t.enabled).length)
// 内置目标按地区分行
const provinces = computed(() => {
  const out: { province: string; cells: Partial<Record<string, ProbeTarget>> }[] = []
  for (const t of items.value) {
    if (!t.builtin) continue
    let r = out.find((x) => x.province === t.province)
    if (!r) out.push((r = { province: t.province, cells: {} }))
    r.cells[t.isp] = t
  }
  return out
})
const custom = computed(() => items.value.filter((t) => !t.builtin))
const where = (t: ProbeTarget) => `${t.proto === 'icmp' ? 'ICMP' : `TCP ${t.port}`} → ${t.address}`

async function toggle(t: ProbeTarget) {
  busy.value = t.id
  try {
    const r = await updateProbeTarget(t.id, { enabled: !t.enabled })
    items.value = items.value.map((x) => (x.id === r.id ? r : x))
    error.value = ''
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = null
  }
}

// ---- 自定义目标 ----
const adding = ref(false)
const form = reactive({ isp: 'ct', province: '', address: '', proto: 'tcp', port: 80, note: '' })
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)

async function add() {
  saving.value = true
  fieldErrors.value = {}
  const body: ProbeTargetInput = {
    isp: form.isp as ProbeTargetInput['isp'], province: form.province.trim(), address: form.address.trim(),
    proto: form.proto as ProbeTargetInput['proto'], port: form.proto === 'tcp' ? form.port : undefined, note: form.note.trim(),
  }
  try {
    await createProbeTarget(body)
    adding.value = false
    Object.assign(form, { province: '', address: '', note: '' })
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

async function remove(t: ProbeTarget) {
  if (!confirm(`删除目标“${t.province} ${ispName(t.isp)} ${t.address}”？各节点到它的探测记录会一并删除。`)) return
  try {
    await deleteProbeTarget(t.id)
    await load()
  } catch (e) {
    error.value = errorText(e)
  }
}
</script>

<template>
  <div>
    <p class="muted small intro">
      各节点的 Agent 按下面的频率测一轮到这些地址的延迟与丢包（TCP 连接，不发送数据；或 ICMP），结果在节点详情的“三网延迟”中查看，保留 7 天。
      这是从 VPS 出发的方向，反映回程质量。目标只能是公网 IP；节点本机可创建 /etc/vpsmon-agent/no-probe 关闭探测。
    </p>
    <p v-if="error" class="banner">{{ error }}</p>
    <div class="toolbar">
      <label class="freq small">测试频率
        <select v-model.number="interval" :disabled="savingInterval" @change="saveInterval">
          <option v-for="s in intervals" :key="s" :value="s">每 {{ s / 60 }} 分钟{{ s === 300 ? '（默认）' : '' }}</option>
        </select>
        <span class="muted">每个节点每天 {{ roundsPerDay }} 轮 × 已启用 {{ enabledCount }} 个目标</span>
      </label>
      <span class="small" :class="enabledCount > MAX_ENABLED - 4 ? 'warn' : 'muted'">已启用 <b class="num">{{ enabledCount }}</b> / {{ MAX_ENABLED }}</span>
      <button type="button" @click="adding = !adding">{{ adding ? '取消' : '添加自定义目标' }}</button>
    </div>

    <form v-if="adding" class="panel form" novalidate @submit.prevent="add">
      <div class="grid">
        <label>运营商
          <select v-model="form.isp"><option v-for="i in isps" :key="i.key" :value="i.key">{{ i.name }}</option></select>
          <small v-if="fieldErrors.isp" class="bad">{{ fieldErrors.isp }}</small></label>
        <label>地区<input v-model="form.province" maxlength="16" placeholder="如 杭州" />
          <small v-if="fieldErrors.province" class="bad">{{ fieldErrors.province }}</small></label>
        <label>IP 地址<input v-model="form.address" spellcheck="false" placeholder="公网 IP，如 115.220.14.91" />
          <small v-if="fieldErrors.address" class="bad">{{ fieldErrors.address }}</small></label>
        <label>协议
          <select v-model="form.proto"><option value="tcp">TCP 连接</option><option value="icmp">ICMP（Ping）</option></select>
          <small v-if="fieldErrors.proto" class="bad">{{ fieldErrors.proto }}</small></label>
        <label v-if="form.proto === 'tcp'">端口<input v-model.number="form.port" type="number" min="1" max="65535" />
          <small v-if="fieldErrors.port" class="bad">{{ fieldErrors.port }}</small></label>
        <label>备注（可选）<input v-model="form.note" maxlength="64" />
          <small v-if="fieldErrors.note" class="bad">{{ fieldErrors.note }}</small></label>
      </div>
      <p v-if="fieldErrors.enabled || fieldErrors._" class="bad small">{{ fieldErrors.enabled || fieldErrors._ }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving || !form.province.trim() || !form.address.trim()">{{ saving ? '保存中…' : '保存' }}</button>
      </div>
    </form>

    <section v-if="custom.length" class="panel block">
      <h3>自定义</h3>
      <ul class="custom">
        <li v-for="t in custom" :key="t.id" :class="{ off: !t.enabled }">
          <label class="check"><input type="checkbox" :checked="t.enabled" :disabled="busy === t.id" @change="toggle(t)" />
            <span>{{ t.province }} · {{ ispName(t.isp) }}</span></label>
          <span class="small muted mono">{{ where(t) }}</span>
          <span v-if="t.note" class="small muted">{{ t.note }}</span>
          <button type="button" class="text small del" @click="remove(t)">删除</button>
        </li>
      </ul>
    </section>

    <section v-if="provinces.length" class="panel block">
      <h3>内置（三大运营商各省地址）</h3>
      <table>
        <thead><tr><th scope="col">地区</th><th v-for="i in isps" :key="i.key" scope="col">{{ i.name }}</th></tr></thead>
        <tbody>
          <tr v-for="r in provinces" :key="r.province">
            <th scope="row">{{ r.province }}</th>
            <td v-for="i in isps" :key="i.key">
              <label v-if="r.cells[i.key]" class="check" :title="where(r.cells[i.key]!)">
                <input type="checkbox" :checked="r.cells[i.key]!.enabled" :disabled="busy === r.cells[i.key]!.id"
                  :aria-label="`${r.province}${i.name}`" @change="toggle(r.cells[i.key]!)" />
                <span class="small muted mono addr">{{ r.cells[i.key]!.address }}</span>
              </label>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-3); max-width: 80ch; }
.toolbar { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); margin-bottom: var(--space-3); flex-wrap: wrap; }
.freq { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; flex-basis: 100%; }
.freq select { width: auto; }
.form { padding: var(--space-4); margin-bottom: var(--space-4); }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: var(--space-3); }
.grid label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.actions { margin-top: var(--space-3); }
.block { padding: var(--space-4); margin-bottom: var(--space-4); }
.block h3 { margin: 0 0 var(--space-3); }
table { width: 100%; border-collapse: collapse; font-size: var(--font-sm); }
th, td { padding: var(--space-2) var(--space-3); text-align: left; border-bottom: 1px solid var(--border); }
th:first-child { padding-left: 0; width: 5em; }
thead th { color: var(--text-muted); font-weight: var(--weight-regular); }
tbody th { font-weight: var(--weight-regular); }
.check { display: inline-flex; align-items: center; gap: var(--space-2); cursor: pointer; }
.check input { width: auto; margin: 0; }
.mono { font-family: var(--font-mono); }
.custom { list-style: none; margin: 0; padding: 0; }
.custom li { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2) var(--space-4); padding: var(--space-2) 0; }
.custom li + li { border-top: 1px solid var(--border); }
.custom li.off { opacity: 0.6; }
.custom .del { margin-left: auto; }
.warn { color: var(--warn); }
.bad, .del { color: var(--bad); }
.muted { color: var(--text-muted); }
@media (max-width: 600px) {
  th, td { padding: var(--space-2) var(--space-1); }
  .addr { display: none; } /* 窄屏只显示勾选框，地址在悬停提示中 */
}
</style>
