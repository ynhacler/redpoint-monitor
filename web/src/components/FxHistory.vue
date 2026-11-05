<script setup lang="ts">
// 汇率走势（手机 UI 规范 P2，修订第 295 条）：面板每次刷新汇率时记下订阅用到的外币，每天一条，保留两年。
// 来源没有历史接口，记录从升级或开始使用该币种起。这里只看走势，订阅金额的换算仍用最新汇率。
import { computed, ref, watch } from 'vue'
import { errorText, listFxHistory } from '../api'
import { t } from '../i18n'
import Chart from './Chart.vue'

const props = defineProps<{
  /** 订阅用到的外币 */
  currencies: string[]
}>()

type Row = Awaited<ReturnType<typeof listFxHistory>>[number]
const cur = ref(props.currencies[0] ?? '')
const rows = ref<Row[]>([])
const loading = ref(true)
const error = ref('')

watch(cur, load, { immediate: true })
// 列表变化后（删除了最后一个该币种的订阅）选中的币种可能不在其中
watch(() => props.currencies, (list) => {
  if (!list.includes(cur.value)) cur.value = list[0] ?? ''
})

async function load(c: string) {
  if (!c) return
  loading.value = true
  error.value = ''
  try {
    const items = await listFxHistory(c)
    if (c === cur.value) rows.value = items
  } catch (e) {
    if (c === cur.value) error.value = errorText(e, t('汇率历史加载失败'))
  } finally {
    if (c === cur.value) loading.value = false
  }
}

const fmt = (v: number) => `¥${v.toFixed(v < 0.1 ? 6 : 4)}`
const series = computed(() => [{ name: cur.value, data: rows.value.map((r): [number, number] => [Date.parse(`${r.day}T00:00:00Z`), r.cny]) }])
const low = computed(() => Math.min(...rows.value.map((r) => r.cny)))
const high = computed(() => Math.max(...rows.value.map((r) => r.cny)))
// 与 30 天前比较：取 30 天前当天或更早最近的一条；记录不满 30 天时不比较
const change = computed(() => {
  const last = rows.value.at(-1)
  if (!last) return null
  const edge = new Date(Date.parse(`${last.day}T00:00:00Z`) - 30 * 86400_000).toISOString().slice(0, 10)
  const base = rows.value.filter((r) => r.day <= edge).at(-1)
  return base ? ((last.cny - base.cny) / base.cny) * 100 : null
})
const changeText = computed(() => {
  const v = change.value
  return v == null ? '' : `${v > 0 ? '+' : ''}${v.toFixed(2)}%`
})
</script>

<template>
  <div class="fx small">
    <div v-if="currencies.length > 1" class="chips" role="group" :aria-label="t('币种')">
      <button v-for="c in currencies" :key="c" type="button" class="chip num" :class="{ on: c === cur }" :aria-pressed="c === cur" @click="cur = c">{{ c }}</button>
    </div>
    <p v-if="loading" class="muted">{{ t('加载中…') }}</p>
    <p v-else-if="error" class="err">{{ error }}</p>
    <p v-else-if="rows.length < 2" class="muted">{{ t('记录还不够画出走势。面板每天记一次订阅用到的外币汇率，来源没有更早的数据。') }}</p>
    <template v-else>
      <p class="summary num">
        {{ t('区间 {low} ～ {high}', { low: fmt(low), high: fmt(high) }) }}
        <template v-if="changeText"> · {{ t('较 30 天前 {v}', { v: changeText }) }}</template>
      </p>
      <Chart :title="t('1 {c} 折合人民币', { c: cur })" :series="series" :format="fmt" scale-y hide-legend />
      <p class="muted note">{{ t('从 {date} 开始记录，只用于查看走势，订阅金额按最新汇率换算。', { date: rows[0].day }) }}</p>
    </template>
  </div>
</template>

<style scoped>
.fx { margin-top: var(--space-2); }
.chips { display: flex; flex-wrap: wrap; gap: var(--space-1); margin-bottom: var(--space-2); }
.chip { min-height: 32px; padding: 0 var(--space-3); border: 1px solid var(--border); border-radius: var(--radius-full); background: transparent; color: var(--text-muted); cursor: pointer; }
.chip.on { border-color: var(--accent); color: var(--accent); }
.summary { margin: 0 0 var(--space-2); }
.note { margin: var(--space-2) 0 0; }
.err { color: var(--bad); }
</style>
