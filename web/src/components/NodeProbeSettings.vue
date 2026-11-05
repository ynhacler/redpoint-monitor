<script setup lang="ts">
// 节点自己的测速设置（设计 42.3.2，修订第 121、123 条）：跟随全局 / 自定义目标与频率 / 关闭。
// 放在节点“编辑”页中，随“保存”一起提交（父组件调用 save）。自定义时可以添加只属于这台节点的测试目标。
// 节点本机的 no-probe 文件优先于这里的设置（设计 42.6）。
import { computed, onMounted, reactive, ref } from 'vue'
import {
  ApiError, createProbeTarget, deleteProbeTarget, errorText, getNodeProbe, getProbeSettings, listProbeTargets, putNodeProbe,
  type NodeProbe, type ProbeTarget, type ProbeTargetInput,
} from '../api'
import { t } from '../i18n'
import ProbeTargetHint from './ProbeTargetHint.vue'
import ProbeTargetPicker from './ProbeTargetPicker.vue'

const props = defineProps<{ serverId: number }>()

const MAX = 64
const intervals: NodeProbe['interval_s'][] = [0, 60, 120, 300, 600, 900, 1800]
const targets = ref<ProbeTarget[]>([])
const mode = ref<NodeProbe['mode']>('default')
const picked = ref<number[]>([])
const interval = ref<NodeProbe['interval_s']>(0)
const globalInterval = ref(300)
// 超时与样本间隔（修订第 133 条）：0 为跟随全局
const timingOptions = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
const timeout = ref(0)
const gap = ref(0)
const globalTiming = ref({ timeout: 4, gap: 1 })
const error = ref('')
const loaded = ref(false)

onMounted(async () => {
  try {
    const [np, list, g] = await Promise.all([getNodeProbe(props.serverId), listProbeTargets(props.serverId), getProbeSettings()])
    targets.value = list
    globalInterval.value = g.interval_s
    globalTiming.value = { timeout: g.timeout_s || 4, gap: g.gap_s || 1 }
    timeout.value = np.timeout_s ?? 0
    gap.value = np.gap_s ?? 0
    mode.value = np.mode
    interval.value = np.interval_s
    // 自定义尚未选择时，以全局启用的目标为起点
    picked.value = np.mode === 'custom' ? np.target_ids : list.filter((t) => t.enabled).map((t) => t.id)
    loaded.value = true
  } catch (e) {
    error.value = errorText(e, t('测速设置加载失败'))
  }
})

function toggle(t: ProbeTarget) {
  picked.value = picked.value.includes(t.id) ? picked.value.filter((id) => id !== t.id) : [...picked.value, t.id]
}
const label = (s: number) => (s === 0 ? t('跟随全局（每 {n} 分钟）', { n: globalInterval.value / 60 }) : t('每 {n} 分钟', { n: s / 60 }))
const tooMany = computed(() => picked.value.length > MAX)

// ---- 这台节点自己的测试目标（修订第 123 条）----
const adding = ref(false)
const form = reactive({ isp: 'ct', province: '', address: '', proto: 'tcp', port: 80, note: '' })
const addErrors = ref<Record<string, string>>({})
const addBusy = ref(false)

async function addOwn() {
  addBusy.value = true
  addErrors.value = {}
  const body: ProbeTargetInput = {
    isp: form.isp as ProbeTargetInput['isp'], province: form.province.trim(), address: form.address.trim(),
    proto: form.proto as ProbeTargetInput['proto'], port: form.proto === 'tcp' ? form.port : undefined,
    note: form.note.trim(), server_id: props.serverId,
  }
  try {
    const nt = await createProbeTarget(body)
    targets.value = [...targets.value, nt]
    picked.value = [...picked.value, nt.id] // 添加即选中
    adding.value = false
    Object.assign(form, { province: '', address: '', note: '' })
  } catch (e) {
    addErrors.value = e instanceof ApiError && e.details.length
      ? Object.fromEntries(e.details.map((d) => [d.field, d.message]))
      : { _: errorText(e) }
  } finally {
    addBusy.value = false
  }
}

async function removeOwn(pt: ProbeTarget) {
  if (!confirm(t('删除这台节点的测试目标“{name}”？它的探测记录会一并删除。', { name: `${pt.province} ${pt.address}` }))) return
  try {
    await deleteProbeTarget(pt.id)
    targets.value = targets.value.filter((x) => x.id !== pt.id)
    picked.value = picked.value.filter((id) => id !== pt.id)
  } catch (e) {
    error.value = errorText(e)
  }
}

/** 由“编辑”页的保存调用；校验失败或保存失败时抛出错误（消息已按界面语言翻译） */
async function save() {
  if (!loaded.value) return
  const custom = mode.value === 'custom'
  if (custom && !picked.value.length) throw new Error(t('测速：自定义时请至少选择一个目标；不测请选“关闭”'))
  if (tooMany.value) throw new Error(t('测速：最多选择 {n} 个目标', { n: MAX }))
  try {
    await putNodeProbe(props.serverId, {
      mode: mode.value, target_ids: custom ? picked.value : [], interval_s: custom ? interval.value : 0,
      timeout_s: custom ? timeout.value : 0, gap_s: custom ? gap.value : 0,
    })
  } catch (e) {
    throw new Error(t('测速：{msg}', { msg: e instanceof ApiError && e.details.length ? e.details.map((d) => d.message).join(t('；')) : errorText(e) }))
  }
}
defineExpose({ save })
</script>

<template>
  <div class="settings">
    <p v-if="!loaded && !error" class="small muted">{{ t('加载中…') }}</p>
    <template v-if="loaded">
      <div class="modes" role="radiogroup" :aria-label="t('这台节点的测速')">
        <label><input v-model="mode" type="radio" value="default" />{{ t('跟随全局') }}<small class="muted">{{ t('“系统 → 监测”中启用的目标与频率') }}</small></label>
        <label><input v-model="mode" type="radio" value="custom" />{{ t('自定义') }}<small class="muted">{{ t('这台节点自己选择目标与频率') }}</small></label>
        <label><input v-model="mode" type="radio" value="off" />{{ t('关闭') }}<small class="muted">{{ t('这台节点不测') }}</small></label>
      </div>
      <template v-if="mode === 'custom'">
        <label class="freq small">{{ t('测试频率') }}
          <select v-model.number="interval">
            <option v-for="s in intervals" :key="s" :value="s">{{ label(s) }}</option>
          </select>
        </label>
        <div class="freq small">
          <label>{{ t('超时') }}
            <select v-model.number="timeout">
              <option v-for="s in timingOptions" :key="s" :value="s">{{ s ? t('{n} 秒', { n: s }) : t('跟随全局（{n} 秒）', { n: globalTiming.timeout }) }}</option>
            </select>
          </label>
          <label>{{ t('样本间隔') }}
            <select v-model.number="gap">
              <option v-for="s in timingOptions" :key="s" :value="s">{{ s ? t('{n} 秒', { n: s }) : t('跟随全局（{n} 秒）', { n: globalTiming.gap }) }}</option>
            </select>
          </label>
        </div>
        <p class="small" :class="tooMany ? 'bad' : 'muted'">{{ t('已选 {n} / {max}（全局未启用的目标也可以选）', { n: picked.length, max: MAX }) }}</p>
        <ProbeTargetPicker :targets="targets" :selected="picked" @toggle="toggle">
          <template #custom-extra="{ target }">
            <template v-if="target.server_id">
              <span class="tag small">{{ t('本机') }}</span>
              <button type="button" class="text small del" @click="removeOwn(target)">{{ t('删除') }}</button>
            </template>
            <span v-else class="tag small muted">{{ t('共用') }}</span>
          </template>
        </ProbeTargetPicker>

        <div class="own">
          <button v-if="!adding" type="button" class="secondary small" @click="adding = true">{{ t('添加这台节点自己的测试目标') }}</button>
          <div v-else class="own-form">
            <div class="grid">
              <label>{{ t('运营商') }}
                <select v-model="form.isp"><option value="ct">{{ t('电信') }}</option><option value="cu">{{ t('联通') }}</option><option value="cm">{{ t('移动') }}</option><option value="intl">{{ t('境外') }}</option></select></label>
              <label>{{ form.isp === 'intl' ? t('城市') : t('地区') }}<input v-model="form.province" maxlength="16" :placeholder="form.isp === 'intl' ? t('如 台北') : t('如 杭州')" />
                <small v-if="addErrors.province" class="bad">{{ addErrors.province }}</small></label>
              <label>{{ t('IP 或域名') }}<input v-model="form.address" spellcheck="false" autocapitalize="off" :placeholder="t('公网 IPv4 / IPv6 或域名')" />
                <small v-if="addErrors.address" class="bad">{{ addErrors.address }}</small></label>
              <label>{{ t('协议') }}
                <select v-model="form.proto"><option value="tcp">{{ t('TCP 连接') }}</option><option value="icmp">ICMP（Ping）</option></select></label>
              <label v-if="form.proto === 'tcp'">{{ t('端口') }}<input v-model.number="form.port" type="number" min="1" max="65535" />
                <small v-if="addErrors.port" class="bad">{{ addErrors.port }}</small></label>
              <label>{{ t('备注（可选）') }}<input v-model="form.note" maxlength="64" /></label>
            </div>
            <p v-if="addErrors._ || addErrors.isp || addErrors.server_id" class="small bad">{{ addErrors._ || addErrors.isp || addErrors.server_id }}</p>
            <div class="actions">
              <button type="button" class="small" :disabled="addBusy || !form.province.trim() || !form.address.trim()" @click="addOwn">{{ addBusy ? t('添加中…') : t('添加') }}</button>
              <button type="button" class="secondary small" @click="adding = false">{{ t('取消') }}</button>
              <span class="small muted">{{ t('只有这台节点测，不出现在全局列表中；最多 20 个') }}</span>
            </div>
            <ProbeTargetHint />
          </div>
        </div>
      </template>
    </template>
    <p v-if="error" class="small bad">{{ error }}</p>
  </div>
</template>

<style scoped>
.modes { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-5); margin-bottom: var(--space-3); }
.modes label { display: inline-flex; align-items: baseline; gap: var(--space-2); cursor: pointer; }
.modes input { width: auto; margin: 0; }
.modes small { margin-left: var(--space-1); }
.freq { display: flex; align-items: center; gap: var(--space-2); margin-bottom: var(--space-2); }
.freq select { width: auto; }
.freq label { display: inline-flex; align-items: center; gap: var(--space-2); }
.freq { flex-wrap: wrap; }
.tag { padding: 0 var(--space-2); border-radius: 99px; background: var(--surface-2); }
.own { margin-top: var(--space-3); }
.own-form { padding: var(--space-3); border-radius: var(--radius-sm); background: var(--surface-2); }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: var(--space-3); }
.grid label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); margin-top: var(--space-3); }
.muted { color: var(--text-muted); }
.bad, .del { color: var(--bad); }
@media (max-width: 600px) {
  .modes { flex-direction: column; }
}
</style>
