<script setup lang="ts">
// 流量记录（设计 19.8、5.7）：按计费周期的月度流量与手动校准历史。放在流量卡片下方；校准后自动刷新。
import { computed, onMounted, ref, watch } from 'vue'
import {
  getTrafficAdjustments, getTrafficMonthly, UnauthorizedError,
  type ServerView, type TrafficAdjustment, type TrafficCycle,
} from '../api'
import { DASH, fmtDateTime, fmtPct, fmtTraffic } from '../format'
import EmptyState from './EmptyState.vue'

const props = defineProps<{
  /** 节点 */
  server: ServerView
}>()
const emit = defineEmits<{ unauthorized: [] }>()

const tab = ref<'cycles' | 'adjustments'>('cycles')
const cycles = ref<TrafficCycle[]>([])
const adjustments = ref<TrafficAdjustment[]>([])
const error = ref('')
const loaded = ref(false)
const unit = computed(() => props.server.traffic.unit)
const fmt = (n: number) => fmtTraffic(n, unit.value)

async function load() {
  try {
    ;[cycles.value, adjustments.value] = await Promise.all([
      getTrafficMonthly(props.server.id, 12), getTrafficAdjustments(props.server.id),
    ])
    error.value = ''
  } catch (e) {
    if (e instanceof UnauthorizedError) emit('unauthorized')
    else error.value = '流量记录加载失败'
  } finally {
    loaded.value = true
  }
}
onMounted(load)
// 换节点或刚校准过（calibrated_at 变化）时重新加载
watch(() => [props.server.id, props.server.traffic.calibrated_at], load)

/** 周期显示为“首日 ～ 末日”：cycle_end 是下一周期的开始日，不含 */
function range(c: TrafficCycle): string {
  const end = new Date(c.cycle_end + 'T00:00:00')
  end.setDate(end.getDate() - 1)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${c.cycle_start} ～ ${end.getFullYear()}-${p(end.getMonth() + 1)}-${p(end.getDate())}`
}
const signed = (n: number) => (n >= 0 ? '+' : '−') + fmt(Math.abs(n))
// 只显示有流量或有校准的周期；最新（当前）周期总是显示
const shownCycles = computed(() => cycles.value.filter((c, i) => i === 0 || c.rx + c.tx > 0 || c.adjustment !== 0))
</script>

<template>
  <div class="panel history">
    <div class="top">
      <h3>流量记录</h3>
      <div class="segmented" role="tablist">
        <button type="button" role="tab" :class="{ active: tab === 'cycles' }" @click="tab = 'cycles'">按计费周期</button>
        <button type="button" role="tab" :class="{ active: tab === 'adjustments' }" @click="tab = 'adjustments'">
          校准记录<template v-if="adjustments.length">（{{ adjustments.length }}）</template>
        </button>
      </div>
    </div>

    <p v-if="error" class="muted small">{{ error }}</p>
    <template v-else-if="loaded && tab === 'cycles'">
      <ul class="list">
        <li v-for="(c, i) in shownCycles" :key="c.cycle_start">
          <div class="row-main">
            <span class="num">{{ range(c) }}</span>
            <span v-if="i === 0" class="tag">本周期</span>
          </div>
          <div class="row-facts small">
            <span><span class="muted">计费</span> <b class="num">{{ fmt(c.used) }}</b>
              <span v-if="c.limit" class="muted limit">/ {{ fmt(c.limit) }} · {{ fmtPct((c.used / c.limit) * 100) }}</span>
            </span>
            <span><span class="muted">入</span> <span class="num">{{ fmt(c.rx) }}</span></span>
            <span><span class="muted">出</span> <span class="num">{{ fmt(c.tx) }}</span></span>
            <span v-if="c.adjustment"><span class="muted">校准</span> <span class="num">{{ signed(c.adjustment) }}</span></span>
          </div>
        </li>
      </ul>
      <p class="muted small note">历史周期按当前的计费模式、系数与额度计算（这些设置没有保存历史版本）。</p>
    </template>
    <template v-else-if="loaded">
      <EmptyState v-if="!adjustments.length" text="还没有校准过。在上方点击“校准已用流量”，填写服务商面板显示的数值。" />
      <ul v-else class="list">
        <li v-for="a in adjustments" :key="a.id">
          <div class="row-main">
            <span class="num">{{ fmtDateTime(a.created_at) }}</span>
            <span class="muted small">周期 {{ a.cycle_start }} 起</span>
          </div>
          <div class="row-facts small">
            <span><span class="muted">服务商</span> <b class="num">{{ fmt(a.reported_bytes) }}</b></span>
            <span><span class="muted">统计</span> <span class="num">{{ fmt(a.measured_bytes) }}</span></span>
            <span><span class="muted">偏差</span> <span class="num">{{ signed(a.adjustment_bytes) }}</span>
              <span v-if="a.measured_bytes" class="muted">（{{ a.adjustment_bytes >= 0 ? '+' : '' }}{{ ((a.adjustment_bytes / a.measured_bytes) * 100).toFixed(1) }}%）</span>
            </span>
            <span v-if="a.note" class="muted">{{ a.note }}</span>
            <span v-else class="muted">{{ DASH }}</span>
          </div>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.top { display: flex; justify-content: space-between; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.top h3 { margin: 0; }
.list { list-style: none; margin: var(--space-3) 0 0; padding: 0; }
.list li { padding: var(--space-3) 0; }
.list li + li { border-top: 1px solid var(--border); }
.row-main { display: flex; align-items: baseline; gap: var(--space-2); flex-wrap: wrap; }
.row-facts { display: flex; flex-wrap: wrap; gap: var(--space-1) var(--space-4); margin-top: var(--space-1); }
.tag { font-size: var(--font-xs); color: var(--accent); border: 1px solid var(--accent); border-radius: var(--radius-sm); padding: 0 var(--space-1); }
.note { margin: var(--space-2) 0 0; }
.limit { margin-left: var(--space-1); }
</style>
