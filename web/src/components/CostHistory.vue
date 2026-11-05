<script setup lang="ts">
// 云账户费用历史（手机 UI 规范 P2，修订第 289 条）：面板每次同步费用时按月保存（cloud_costs，最多 24 个月），这里按月列出。
// 往月的金额是该月最后一次同步的结果，账单在月末后还可能有数小时到数天的补录，因此可能略低于最终账单；
// 记录从添加账户（并开启同步费用）起，之前的月份没有数据。超过预算的月份标为警告色。
import { computed, onMounted, ref } from 'vue'
import { errorText, listCloudCosts } from '../api'
import { fmtMoney } from '../format'
import { t } from '../i18n'

const props = defineProps<{
  /** 云账户 */
  accountId: number
  /** 每月预算（分，0 为未设置），与费用同币种 */
  budgetCents: number
}>()

type Row = Awaited<ReturnType<typeof listCloudCosts>>[number]
const rows = ref<Row[]>([])
const loading = ref(true)
const error = ref('')

onMounted(async () => {
  try {
    rows.value = await listCloudCosts(props.accountId)
  } catch (e) {
    error.value = errorText(e, t('费用历史加载失败'))
  } finally {
    loading.value = false
  }
})

// 当前月（各云厂商按自己的时区分月；这里用最新一条是否为本地本月来判断“本月”，差几个小时不影响列表）
const thisMonth = (() => {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
})()
const max = computed(() => Math.max(1, ...rows.value.map((r) => Math.max(r.amount_cents, r.forecast_cents ?? 0))))
const past = computed(() => rows.value.filter((r) => r.period.slice(0, 7) < thisMonth))
const sameCurrency = computed(() => new Set(rows.value.map((r) => r.currency)).size <= 1)
// 近 6 个日历月内（不含本月）有记录的月份的月均；中间缺记录的月份不算，也不往更早的月份补
const sixAgo = (() => {
  const d = new Date()
  d.setDate(1)
  d.setMonth(d.getMonth() - 6)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
})()
const avg = computed(() => {
  const list = past.value.filter((r) => r.period.slice(0, 7) >= sixAgo)
  if (!list.length || !sameCurrency.value) return null
  return { n: list.length, cents: Math.round(list.reduce((a, r) => a + r.amount_cents, 0) / list.length), currency: list[0].currency }
})
// 上月较前月的变化：只比较相邻的两个月
const change = computed(() => {
  const [a, b] = past.value
  if (!a || !b || prevMonth(a.period) !== b.period.slice(0, 7) || !b.amount_cents || a.currency !== b.currency) return null
  return Math.round(((a.amount_cents - b.amount_cents) / b.amount_cents) * 100)
})

function prevMonth(period: string): string {
  const [y, m] = period.split('-').map(Number)
  return m === 1 ? `${y - 1}-12` : `${y}-${String(m - 1).padStart(2, '0')}`
}
function label(period: string): string {
  const [y, m] = period.split('-')
  if (period.slice(0, 7) === thisMonth) return t('本月')
  return y === thisMonth.slice(0, 4) ? t('{m} 月', { m: Number(m) }) : t('{y} 年 {m} 月', { y, m: Number(m) })
}
const over = (r: Row) => props.budgetCents > 0 && r.amount_cents > props.budgetCents
</script>

<template>
  <div class="history">
    <p v-if="loading" class="muted">{{ t('加载中…') }}</p>
    <p v-else-if="error" class="err">{{ error }}</p>
    <p v-else-if="!rows.length" class="muted">{{ t('还没有费用记录。面板从添加账户、开启同步费用起逐月记录。') }}</p>
    <template v-else>
      <p v-if="avg" class="summary">
        {{ t('近半年 {n} 个月平均 {amount}', { n: avg.n, amount: fmtMoney(avg.cents, avg.currency) }) }}
        <template v-if="change != null">
          · <span :class="change > 0 ? 'warn' : ''">{{ t('上月较前月 {sign}{n}%', { sign: change > 0 ? '+' : '', n: change }) }}</span>
        </template>
      </p>
      <ul class="rows num">
        <li v-for="r in rows" :key="r.period" :class="{ current: label(r.period) === t('本月') }">
          <span class="month">{{ label(r.period) }}</span>
          <span class="bar" aria-hidden="true">
            <span v-if="r.forecast_cents != null && r.forecast_cents > r.amount_cents" class="fc" :style="{ width: `${(r.forecast_cents / max) * 100}%` }" />
            <span class="amt" :class="{ warn: over(r) }" :style="{ width: `${Math.max(1, (r.amount_cents / max) * 100)}%` }" />
          </span>
          <span class="val" :class="{ warn: over(r) }">
            {{ fmtMoney(r.amount_cents, r.currency) }}
            <small v-if="label(r.period) === t('本月') && r.forecast_cents != null" class="muted">{{ t('预估 {amount}', { amount: fmtMoney(r.forecast_cents, r.currency) }) }}</small>
          </span>
        </li>
      </ul>
      <p class="muted note">{{ t('往月为当月最后一次同步的金额，月末后的补录可能未计入，以云厂商账单为准。') }}<template v-if="budgetCents"> {{ t('超过预算的月份标为橙色。') }}</template></p>
    </template>
  </div>
</template>

<style scoped>
.history { margin-top: var(--space-2); }
.summary { margin: 0 0 var(--space-2); }
.rows { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-1); }
.rows li { display: grid; grid-template-columns: 6.5em minmax(40px, 1fr) auto; align-items: center; gap: var(--space-2); min-height: 24px; }
.rows li.current .month { font-weight: var(--weight-strong); }
.month { color: var(--text-muted); white-space: nowrap; }
.bar { position: relative; height: 6px; border-radius: var(--radius-full); background: var(--track); overflow: hidden; }
.bar span { position: absolute; left: 0; top: 0; bottom: 0; border-radius: var(--radius-full); }
.bar .amt { background: var(--accent); }
.bar .amt.warn { background: var(--warn); }
.bar .fc { background: color-mix(in srgb, var(--accent) 30%, transparent); }
.val { text-align: right; white-space: nowrap; display: flex; flex-direction: column; align-items: flex-end; }
.val small { font-size: var(--font-xs); }
.warn { color: var(--warn); }
.err { color: var(--bad); }
.note { margin: var(--space-2) 0 0; }
</style>
