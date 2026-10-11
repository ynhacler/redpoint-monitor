<script setup lang="ts">
// 测速的探测目标（设计 42.3.2）：全部由用户自己添加（内置地址已删除，修订第 128 条），勾选的对“跟随全局”的节点生效。
// 修改后随下一份上报下发给各节点 Agent；启用的目标最多 64 个（Agent 的上限，设计 42.6）。
// 测试频率对所有节点生效：1～30 分钟，Agent 端不短于 1 分钟。
import { computed, onMounted, reactive, ref } from 'vue'
import ProbeTargetHint from './ProbeTargetHint.vue'
import ProbeTargetPicker from './ProbeTargetPicker.vue'
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
const ispName = (k: string) => (k === 'intl' ? '境外' : isps.find((i) => i.key === k)?.name ?? k)

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
const selected = computed(() => items.value.filter((t) => t.enabled).map((t) => t.id))

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
      各节点的 Agent 按下面的频率测一轮到这些地址的延迟与丢包（TCP 连接，不发送数据；或 ICMP），结果在节点详情的“测速”中查看，保留 7 天。
      这是从 VPS 出发的方向，反映回程质量。目标是公网 IPv4 / IPv6 或域名（域名由 Agent 每轮解析，只连接公网地址）；节点本机可创建 /etc/vpsmon-agent/no-probe 关闭探测。
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
      <button type="button" @click="adding = !adding">{{ adding ? '取消' : '添加目标' }}</button>
    </div>

    <form v-if="adding" class="panel form" novalidate @submit.prevent="add">
      <div class="grid">
        <label>运营商
          <select v-model="form.isp"><option v-for="i in isps" :key="i.key" :value="i.key">{{ i.name }}</option><option value="intl">境外</option></select>
          <small v-if="fieldErrors.isp" class="bad">{{ fieldErrors.isp }}</small></label>
        <label>{{ form.isp === 'intl' ? '城市' : '地区' }}<input v-model="form.province" maxlength="16" :placeholder="form.isp === 'intl' ? '如 台北' : '如 杭州'" />
          <small v-if="fieldErrors.province" class="bad">{{ fieldErrors.province }}</small></label>
        <label>IP 或域名<input v-model="form.address" spellcheck="false" autocapitalize="off" placeholder="如 115.220.14.91、2408:8000::1 或域名" />
          <small v-if="fieldErrors.address" class="bad">{{ fieldErrors.address }}</small></label>
        <label>协议
          <select v-model="form.proto"><option value="tcp">TCP 连接</option><option value="icmp">ICMP（Ping）</option></select>
          <small v-if="fieldErrors.proto" class="bad">{{ fieldErrors.proto }}</small></label>
        <label v-if="form.proto === 'tcp'">端口<input v-model.number="form.port" type="number" min="1" max="65535" />
          <small v-if="fieldErrors.port" class="bad">{{ fieldErrors.port }}</small></label>
        <label>{{ form.isp === 'intl' ? '提供商（可选）' : '备注（可选）' }}<input v-model="form.note" maxlength="64" />
          <small v-if="fieldErrors.note" class="bad">{{ fieldErrors.note }}</small></label>
      </div>
      <p v-if="fieldErrors.enabled || fieldErrors._" class="bad small">{{ fieldErrors.enabled || fieldErrors._ }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving || !form.province.trim() || !form.address.trim()">{{ saving ? '保存中…' : '保存' }}</button>
      </div>
    </form>

    <p v-if="!items.length && !adding" class="panel empty small">
      还没有测试目标。点击“添加目标”，填写电信、联通、移动在各地的公网 IP 或域名，境外地址运营商选“境外”。
    </p>
    <ProbeTargetHint v-if="!items.length || adding" class="hint-line" />
    <section v-if="items.length" class="panel block">
      <h3>目标（勾选的对所有“跟随全局”的节点生效）</h3>
      <ProbeTargetPicker :targets="items" :selected="selected" :busy="busy" @toggle="toggle">
        <template #custom-extra="{ target }">
          <span v-if="target.note" class="small muted">{{ target.note }}</span>
          <button type="button" class="text small del" @click="remove(target)">删除</button>
        </template>
      </ProbeTargetPicker>
      <p class="small muted foot">单台节点可在节点的“编辑 → 测速”中自己选择目标与频率，或关闭。</p>
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
.empty { padding: var(--space-4); margin: 0 0 var(--space-2); }
.hint-line { margin: 0 0 var(--space-4); }
.block h3 { margin: 0 0 var(--space-3); }
.foot { margin: var(--space-3) 0 0; }
.warn { color: var(--warn); }
.bad, .del { color: var(--bad); }
.muted { color: var(--text-muted); }
</style>
