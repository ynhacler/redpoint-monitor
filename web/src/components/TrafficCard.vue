<script setup lang="ts">
// 流量套餐卡片（设计 41.3 TrafficCard、1.5.8）：已用 / 总量、剩余、距离重置、日均、预计，以及手动校准（设计 5.7）
// 和最近 30 天每日流量。已用、预计都由面板计算（含系数与校准），这里只负责显示，App 与 Web 口径一致。
import { computed, onMounted, ref, watch } from 'vue'
import { ApiError, calibrateTraffic, getTrafficDaily, UnauthorizedError, type ServerView, type TrafficDay } from '../api'
import { countModeNames, DASH, fmtPct, fmtTraffic } from '../format'
import { daysToReset, trafficPct } from '../metrics'
import { refresh } from '../store'
import Chart, { type Series } from './Chart.vue'
import UsageBar from './UsageBar.vue'

const props = defineProps<{
  /** 节点 */
  server: ServerView
}>()
const emit = defineEmits<{ unauthorized: [] }>()

const t = computed(() => props.server.traffic)
const fmt = (n: number | null | undefined) => fmtTraffic(n, t.value.unit)
const unitName = computed(() => (t.value.unit === 'binary' ? 'GiB' : 'GB'))
const pct = computed(() => trafficPct(props.server))
const left = computed(() => daysToReset(props.server))
const calibratedOn = computed(() => {
  if (!t.value.calibrated_at) return ''
  const d = new Date(t.value.calibrated_at * 1000)
  return `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
})

// 最近 30 天每日流量（设计 19.8）
const days = ref<TrafficDay[]>([])
const dailyError = ref('')
async function loadDaily() {
  try {
    days.value = await getTrafficDaily(props.server.id, 30)
    dailyError.value = ''
  } catch (e) {
    if (e instanceof UnauthorizedError) emit('unauthorized')
    else dailyError.value = '每日流量加载失败'
  }
}
onMounted(loadDaily)
watch(() => props.server.id, loadDaily)
const dailySeries = computed<Series[]>(() => [
  { name: '已计流量', data: days.value.map((d) => [new Date(d.day + 'T00:00:00').getTime(), d.used]) },
])
const hasDaily = computed(() => days.value.some((d) => d.used > 0))

// 手动校准：填写服务商面板显示的已用量，面板记录偏差并只作用于本周期（设计 5.7）
const calibrating = ref(false)
const usedInput = ref('')
const note = ref('')
const calError = ref('')
const saving = ref(false)
function openCalibrate() {
  usedInput.value = ''
  note.value = ''
  calError.value = ''
  calibrating.value = true
}
async function saveCalibrate() {
  const v = Number(usedInput.value)
  if (usedInput.value === '' || !Number.isFinite(v) || v < 0) {
    calError.value = `请填写服务商面板显示的已用量（${unitName.value}）`
    return
  }
  saving.value = true
  calError.value = ''
  try {
    await calibrateTraffic(props.server.id, v, note.value.trim())
    calibrating.value = false
    await refresh() // 列表、卡片立即显示校准后的值
  } catch (e) {
    if (e instanceof UnauthorizedError) emit('unauthorized')
    else if (e instanceof ApiError) calError.value = e.details[0]?.message ?? e.message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="panel traffic">
    <div class="top">
      <h3>本周期流量</h3>
      <span class="muted small">
        {{ countModeNames[server.traffic_count_mode] ?? server.traffic_count_mode }} · 每月 {{ server.traffic_reset_day }} 日重置
        <template v-if="t.factor !== 1"> · 系数 {{ t.factor }}</template>
      </span>
    </div>
    <div class="big num">
      {{ fmt(t.used) }}<span class="muted"> / {{ t.limit ? fmt(t.limit) : '不限' }}</span>
      <span v-if="pct != null" class="pct" :class="{ warn: pct >= 80, bad: pct >= 95 }">{{ fmtPct(pct) }}</span>
    </div>
    <UsageBar v-if="pct != null" :pct="pct" />
    <div class="cal small">
      <span v-if="calibratedOn" class="muted" :title="`统计值 ${fmt(t.measured)}，校准偏差 ${t.adjustment >= 0 ? '+' : '−'}${fmt(Math.abs(t.adjustment))}`">
        已于 {{ calibratedOn }} 校准
      </span>
      <button v-if="!calibrating" type="button" class="text" @click="openCalibrate">校准已用流量</button>
    </div>

    <form v-if="calibrating" class="cal-form" novalidate @submit.prevent="saveCalibrate">
      <label>服务商面板显示已用（{{ unitName }}）
        <input v-model="usedInput" type="number" min="0" step="any" autofocus :placeholder="(t.used / (t.unit === 'binary' ? 2 ** 30 : 1e9)).toFixed(2)" />
      </label>
      <label>备注<input v-model="note" maxlength="200" placeholder="可选" /></label>
      <p class="muted small">只影响本计费周期，周期重置后清零；再次校准会覆盖本次。</p>
      <p v-if="calError" class="err small">{{ calError }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving">保存</button>
        <button type="button" class="secondary" :disabled="saving" @click="calibrating = false">取消</button>
      </div>
    </form>

    <dl class="facts">
      <div><dt>剩余</dt><dd class="num">{{ t.limit ? fmt(Math.max(0, t.limit - t.used)) : DASH }}</dd></div>
      <div><dt>距离重置</dt><dd class="num">{{ left }} 天</dd></div>
      <div title="周期满 7 天后按最近 7 天计算"><dt>日均</dt><dd class="num">{{ t.forecast ? fmt(t.forecast.daily) : DASH }}</dd></div>
      <div :title="t.forecast ? '按日均估算到周期结束' : '周期开始不足 3 天，暂不预测'">
        <dt>预计周期结束</dt>
        <dd class="num" :class="{ bad: t.forecast?.over }">{{ t.forecast ? fmt(t.forecast.total) : DASH }}</dd>
      </div>
      <div><dt>入站 / 出站</dt><dd class="num">{{ fmt(t.rx) }} / {{ fmt(t.tx) }}</dd></div>
    </dl>

    <p v-if="dailyError" class="muted small daily">{{ dailyError }}</p>
    <Chart v-else-if="hasDaily" class="daily" title="最近 30 天每日流量" kind="bar" :series="dailySeries" :format="fmt" />
  </div>
</template>

<style scoped>
.top { display: flex; justify-content: space-between; align-items: baseline; gap: var(--space-2); flex-wrap: wrap; }
.big { font-size: var(--font-num); line-height: var(--line-num); margin: var(--space-3) 0; display: flex; align-items: baseline; gap: var(--space-2); flex-wrap: wrap; }
.big .muted { font-size: var(--font-lg); }
.pct { font-size: var(--font-md); margin-left: auto; }
.cal { display: flex; justify-content: flex-end; align-items: baseline; gap: var(--space-3); margin-top: var(--space-2); }
.cal-form { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: var(--space-3); margin-top: var(--space-3);
  padding: var(--space-3); background: var(--surface-2); border-radius: var(--radius-sm); }
.cal-form label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.cal-form p, .cal-form .actions { grid-column: 1 / -1; margin: 0; }
.actions { display: flex; gap: var(--space-2); }
.err { color: var(--bad); }
.facts { display: grid; grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); gap: var(--space-3); margin: var(--space-4) 0 0; }
.facts dt { font-size: var(--font-xs); color: var(--text-muted); }
.facts dd { margin: 0; }
.daily { margin-top: var(--space-4); padding: 0; border: 0; background: none; }
</style>
