<script setup lang="ts">
// 订阅的历史扣费（手机 UI 规范 P2，修订第 293 条）：面板不记录实际付款，也不保存改价历史，这里按开始日期、试用期、周期与结束日期
// 推算到今天为止的扣费日（规则与 subscriptions.ts 的 chargesBetween 相同），金额一律按当前价格，界面上写明是推算值。
import { computed, ref } from 'vue'
import type { Subscription } from '../api'
import { fmtMoney } from '../format'
import { t } from '../i18n'
import { chargesBetween, ymd } from '../subscriptions'

const props = defineProps<{
  /** 已保存的订阅（表单中未保存的修改不计入） */
  sub: Subscription
}>()

const SHOWN = 12
const all = ref(false)
// 从开始日期到今天（含今天），最新的在前
const dates = computed(() => {
  const now = new Date()
  const tomorrow = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1)
  return chargesBetween(props.sub, new Date(0), tomorrow).reverse()
})
const shown = computed(() => (all.value ? dates.value : dates.value.slice(0, SHOWN)))
const money = (cents: number) => fmtMoney(cents, props.sub.currency)
</script>

<template>
  <details class="charges small">
    <summary class="muted">{{ t('历史扣费') }}</summary>
    <p v-if="!dates.length" class="muted">{{ t('还没有到首次扣费日。') }}</p>
    <template v-else>
      <p>{{ t('共 {n} 次，合计约 {amount}', { n: dates.length, amount: money(sub.price_cents * dates.length) }) }}</p>
      <ul class="num">
        <li v-for="d in shown" :key="d.getTime()"><span class="muted">{{ ymd(d) }}</span><span>{{ money(sub.price_cents) }}</span></li>
      </ul>
      <button v-if="dates.length > SHOWN && !all" type="button" class="text small" @click="all = true">{{ t('显示全部 {n} 次', { n: dates.length }) }}</button>
      <p class="muted">{{ t('按开始日期与周期推算，金额按当前价格；改过价格时与实际付款不同，以账单为准。') }}</p>
    </template>
  </details>
</template>

<style scoped>
.charges { margin-top: var(--space-3); }
.charges summary { cursor: pointer; min-height: 32px; display: flex; align-items: center; }
.charges p { margin: var(--space-2) 0; }
.charges ul { list-style: none; margin: 0; padding: 0; max-width: 320px; }
.charges li { display: flex; justify-content: space-between; gap: var(--space-3); min-height: 24px; align-items: center; }
</style>
