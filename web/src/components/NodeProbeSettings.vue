<script setup lang="ts">
// 节点自己的三网延迟设置（设计 42.3.2，修订第 121 条）：跟随全局 / 自定义目标与频率 / 关闭。
// 保存后随这台节点的下一份上报下发；节点本机的 no-probe 文件优先于这里的设置（设计 42.6）。
import { computed, onMounted, ref } from 'vue'
import {
  ApiError, errorText, getNodeProbe, getProbeSettings, listProbeTargets, putNodeProbe,
  type NodeProbe, type ProbeTarget,
} from '../api'
import ProbeTargetPicker from './ProbeTargetPicker.vue'

const props = defineProps<{ serverId: number }>()
const emit = defineEmits<{ saved: []; cancel: [] }>()

const MAX = 64
const intervals: NodeProbe['interval_s'][] = [0, 60, 120, 300, 600, 900, 1800]
const targets = ref<ProbeTarget[]>([])
const mode = ref<NodeProbe['mode']>('default')
const picked = ref<number[]>([])
const interval = ref<NodeProbe['interval_s']>(0)
const globalInterval = ref(300)
const error = ref('')
const saving = ref(false)
const loaded = ref(false)

onMounted(async () => {
  try {
    const [np, list, g] = await Promise.all([getNodeProbe(props.serverId), listProbeTargets(), getProbeSettings()])
    targets.value = list
    globalInterval.value = g.interval_s
    mode.value = np.mode
    interval.value = np.interval_s
    // 自定义尚未选择时，以全局启用的目标为起点
    picked.value = np.mode === 'custom' ? np.target_ids : list.filter((t) => t.enabled).map((t) => t.id)
    loaded.value = true
  } catch (e) {
    error.value = errorText(e, '设置加载失败')
  }
})

function toggle(t: ProbeTarget) {
  picked.value = picked.value.includes(t.id) ? picked.value.filter((id) => id !== t.id) : [...picked.value, t.id]
}
const label = (s: number) => (s === 0 ? `跟随全局（每 ${globalInterval.value / 60} 分钟）` : `每 ${s / 60} 分钟`)
const tooMany = computed(() => picked.value.length > MAX)

async function save() {
  saving.value = true
  error.value = ''
  try {
    const custom = mode.value === 'custom'
    await putNodeProbe(props.serverId, {
      mode: mode.value, target_ids: custom ? picked.value : [], interval_s: custom ? interval.value : 0,
    })
    emit('saved')
  } catch (e) {
    error.value = e instanceof ApiError && e.details.length ? e.details.map((d) => d.message).join('；') : errorText(e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <form class="settings" novalidate @submit.prevent="save">
    <p v-if="!loaded && !error" class="small muted">加载中…</p>
    <template v-if="loaded">
      <div class="modes" role="radiogroup" aria-label="这台节点的三网延迟">
        <label><input v-model="mode" type="radio" value="default" />跟随全局<small class="muted">“告警 → 三网”中启用的目标与频率</small></label>
        <label><input v-model="mode" type="radio" value="custom" />自定义<small class="muted">这台节点自己选择目标与频率</small></label>
        <label><input v-model="mode" type="radio" value="off" />关闭<small class="muted">这台节点不测</small></label>
      </div>
      <template v-if="mode === 'custom'">
        <label class="freq small">测试频率
          <select v-model.number="interval">
            <option v-for="s in intervals" :key="s" :value="s">{{ label(s) }}</option>
          </select>
        </label>
        <p class="small" :class="tooMany ? 'bad' : 'muted'">已选 <b class="num">{{ picked.length }}</b> / {{ MAX }}（全局未启用的目标也可以选）</p>
        <ProbeTargetPicker :targets="targets" :selected="picked" @toggle="toggle" />
      </template>
    </template>
    <p v-if="error" class="small bad">{{ error }}</p>
    <div class="actions">
      <button type="submit" :disabled="!loaded || saving || tooMany || (mode === 'custom' && !picked.length)">{{ saving ? '保存中…' : '保存' }}</button>
      <button type="button" class="secondary" @click="emit('cancel')">取消</button>
      <span class="small muted">保存后随这台节点的下一次上报生效</span>
    </div>
  </form>
</template>

<style scoped>
.settings { padding: var(--space-3) 0 var(--space-4); border-bottom: 1px solid var(--border); margin-bottom: var(--space-4); }
.modes { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-5); margin-bottom: var(--space-3); }
.modes label { display: inline-flex; align-items: baseline; gap: var(--space-2); cursor: pointer; }
.modes input { width: auto; margin: 0; }
.modes small { margin-left: var(--space-1); }
.freq { display: flex; align-items: center; gap: var(--space-2); margin-bottom: var(--space-2); }
.freq select { width: auto; }
.actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); margin-top: var(--space-3); }
.muted { color: var(--text-muted); }
.bad { color: var(--bad); }
@media (max-width: 600px) {
  .modes { flex-direction: column; }
  .modes small { display: block; }
}
</style>
