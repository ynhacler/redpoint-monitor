<script setup lang="ts">
// 流量套餐卡片（设计 41.3 TrafficCard、1.5.8）：已用 / 总量、剩余、距离重置、日均、预计，以及手动校准（设计 5.7）
// 和最近 30 天每日流量。已用、预计都由面板计算（含系数与校准），这里只负责显示，App 与 Web 口径一致。
import { computed, onMounted, ref, watch } from 'vue'
import { calibrateTraffic, errorText, getTrafficDaily, type ServerView, type TrafficDay } from '../api'
import { countModeNames, DASH, fmtPctFine, fmtTraffic, fmtTrafficLeft } from '../format'
import { t } from '../i18n'
import { daysToReset, trafficPct } from '../metrics'
import { isViewer, refresh } from '../store'
import Chart, { type Series } from './Chart.vue'
import UsageBar from './UsageBar.vue'

const props = defineProps<{
  /** 节点 */
  server: ServerView
}>()

const tf = computed(() => props.server.traffic) // 不叫 t：t 是翻译函数
const fmt = (n: number | null | undefined) => fmtTraffic(n, tf.value.unit)
const unitName = computed(() => (tf.value.unit === 'binary' ? 'GiB' : 'GB'))
const pct = computed(() => trafficPct(props.server))
const left = computed(() => daysToReset(props.server))
const calibratedOn = computed(() => {
  if (!tf.value.calibrated_at) return ''
  const d = new Date(tf.value.calibrated_at * 1000)
  return `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
})

// 系数建议（设计 5.7）：多次校准的比例稳定时，提示统计值相对服务商偏低 / 偏高多少
const suggestion = computed(() => {
  const f = tf.value.factor_suggestion
  if (!f) return null
  const diff = Math.round((f / (tf.value.factor || 1) - 1) * 100)
  const vars = { pct: Math.abs(diff), factor: f }
  return diff > 0 ? t('多次校准显示统计值比服务商偏低约 {pct}%，建议把统计系数设为 {factor}。', vars)
    : t('多次校准显示统计值比服务商偏高约 {pct}%，建议把统计系数设为 {factor}。', vars)
})

// 最近 30 天每日流量（设计 19.8）
const days = ref<TrafficDay[]>([])
const dailyError = ref('')
async function loadDaily() {
  try {
    days.value = await getTrafficDaily(props.server.id, 30)
    dailyError.value = ''
  } catch (e) {
    dailyError.value = errorText(e, t('每日流量加载失败'))
  }
}
onMounted(loadDaily)
watch(() => props.server.id, loadDaily)
const dailySeries = computed<Series[]>(() => [
  { name: t('已计流量'), data: days.value.map((d) => [new Date(d.day + 'T00:00:00').getTime(), d.used]) },
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
    calError.value = t('请填写服务商面板显示的已用量（{unit}）', { unit: unitName.value })
    return
  }
  saving.value = true
  calError.value = ''
  try {
    await calibrateTraffic(props.server.id, v, note.value.trim())
    calibrating.value = false
    await refresh() // 列表、卡片立即显示校准后的值
  } catch (e) {
    calError.value = errorText(e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="panel traffic">
    <div class="top">
      <h3>{{ t('本周期流量') }}</h3>
      <span class="muted small">
        {{ countModeNames[server.traffic_count_mode] ? t(countModeNames[server.traffic_count_mode]) : server.traffic_count_mode }} · {{ t('每月 {day} 日重置', { day: server.traffic_reset_day }) }}<template v-if="server.traffic_timezone">（{{ server.traffic_timezone }}）</template>
        <template v-if="tf.factor !== 1"> · {{ t('系数 {n}', { n: tf.factor }) }}</template>
      </span>
    </div>
    <div class="big num">
      {{ fmt(tf.used) }}<span class="muted"> / {{ tf.limit ? fmt(tf.limit) : t('不限') }}</span>
      <span v-if="pct != null" class="pct" :class="{ warn: pct >= 80, bad: pct >= 95 }">{{ fmtPctFine(pct) }}</span>
    </div>
    <UsageBar v-if="pct != null" :pct="pct" />
    <div class="cal small">
      <span v-if="calibratedOn" class="muted" :title="t('统计值 {measured}，校准偏差 {adj}', { measured: fmt(tf.measured), adj: (tf.adjustment >= 0 ? '+' : '−') + fmt(Math.abs(tf.adjustment)) })">
        {{ t('已于 {date} 校准', { date: calibratedOn }) }}
      </span>
      <button v-if="!calibrating && !isViewer" type="button" class="text" @click="openCalibrate">{{ t('校准已用流量') }}</button>
    </div>

    <p v-if="suggestion" class="hint small">
      {{ suggestion }}
      <RouterLink :to="`/servers/${server.id}/edit`">{{ t('去设置') }}</RouterLink>
    </p>

    <form v-if="calibrating" class="cal-form" novalidate @submit.prevent="saveCalibrate">
      <label>{{ t('服务商面板显示已用（{unit}）', { unit: unitName }) }}
        <input v-model="usedInput" type="number" min="0" step="any" autofocus :placeholder="(tf.used / (tf.unit === 'binary' ? 2 ** 30 : 1e9)).toFixed(2)" />
      </label>
      <label>{{ t('备注') }}<input v-model="note" maxlength="200" :placeholder="t('可选')" /></label>
      <p class="muted small">{{ t('只影响本计费周期，周期重置后清零；再次校准会覆盖本次。之后修改系数或计费模式，已用量仍以本次填写值为准。') }}</p>
      <p v-if="calError" class="err small">{{ calError }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving">{{ t('保存') }}</button>
        <button type="button" class="secondary" :disabled="saving" @click="calibrating = false">{{ t('取消') }}</button>
      </div>
    </form>

    <dl class="facts">
      <div><dt>{{ t('剩余') }}</dt><dd class="num">{{ tf.limit ? fmtTrafficLeft(Math.max(0, tf.limit - tf.used), tf.limit, tf.unit) : DASH }}</dd></div>
      <div><dt>{{ t('距离重置') }}</dt><dd class="num">{{ t('{n} 天', { n: left }) }}</dd></div>
      <div :title="t('周期满 7 天后按最近 7 天计算')"><dt>{{ t('日均') }}</dt><dd class="num">{{ tf.forecast ? fmt(tf.forecast.daily) : DASH }}</dd></div>
      <div :title="tf.forecast ? t('按日均估算到周期结束') : t('周期开始不足 3 天，暂不预测')">
        <dt>{{ t('预计周期结束') }}</dt>
        <dd class="num" :class="{ bad: tf.forecast?.over }">{{ tf.forecast ? fmt(tf.forecast.total) : DASH }}</dd>
      </div>
      <div><dt>{{ t('入站 / 出站') }}</dt><dd class="num">{{ fmt(tf.rx) }} / {{ fmt(tf.tx) }}</dd></div>
    </dl>

    <p v-if="dailyError" class="muted small daily">{{ dailyError }}</p>
    <Chart v-else-if="hasDaily" class="daily" :title="t('最近 30 天每日流量')" kind="bar" :series="dailySeries" :format="fmt" />
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
.hint { margin: var(--space-2) 0 0; padding: var(--space-2) var(--space-3); background: var(--surface-2); border-radius: var(--radius-sm); }
.facts { display: grid; grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); gap: var(--space-3); margin: var(--space-4) 0 0; }
.facts dt { font-size: var(--font-xs); color: var(--text-muted); }
.facts dd { margin: 0; }
.daily { margin-top: var(--space-4); padding: 0; border: 0; background: none; }
</style>
