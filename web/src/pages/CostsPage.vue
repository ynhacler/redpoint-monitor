<script setup lang="ts">
// 费用统计（设计 36.3）：按节点续费价格与周期折算的月均 / 年均开销（按币种，不换算）、按供应商或分组拆分、
// 30 天内的续费，以及云账户本月已产生的费用（设计 44）。资产信息在节点编辑页填写（设计 1.2.3）。
import { computed, onMounted, ref } from 'vue'
import { errorText, listCloudAccounts, type CloudAccount } from '../api'
import { breakdown, monthlyCents, totalsByCurrency, upcomingRenewals, type CostNode } from '../costs'
import Icon from '../components/Icon.vue'
import { fmtMoney, periodNames } from '../format'
import { state } from '../store'

const nodes = computed<CostNode[]>(() => state.servers.map((s) => ({
  id: s.id, name: s.name, group: s.group, provider: s.provider, price_cents: s.price_cents, currency: s.currency,
  billing_period: s.billing_period, expire_date: s.expire_date,
})))
const totals = computed(() => totalsByCurrency(nodes.value))
const by = ref<'provider' | 'group'>('provider')
const parts = computed(() => breakdown(nodes.value, by.value))
const renewals = computed(() => upcomingRenewals(nodes.value, 30))
const unpriced = computed(() => nodes.value.filter((n) => !n.price_cents || !n.billing_period))

const cloud = ref<CloudAccount[]>([])
const cloudError = ref('')
onMounted(async () => {
  try {
    cloud.value = (await listCloudAccounts()).filter((a) => a.current_cost)
  } catch (e) {
    cloudError.value = errorText(e)
  }
})

const money = (cents: number, cur: string) => fmtMoney(Math.round(cents), cur)
function dueText(days: number) {
  if (days < 0) return `已过期 ${-days} 天`
  if (days === 0) return '今天到期'
  return `${days} 天后`
}
</script>

<template>
  <main class="page">
    <div class="nav-row">
      <RouterLink to="/" class="back muted small"><Icon name="arrow-left" :size="14" />仪表板</RouterLink>
    </div>
    <div class="page-head">
      <h1>费用统计</h1>
    </div>
    <p class="muted small intro">
      按每个节点填写的续费价格与周期折算为月均、年均开销；不同币种分开统计，不做换算。一次性付款单独列出，不计入月均。
    </p>

    <section v-if="totals.length" class="totals">
      <div v-for="t in totals" :key="t.currency" class="panel total">
        <div class="muted small">{{ t.currency }} · {{ t.count }} 个节点</div>
        <div class="big num">{{ money(t.monthly, t.currency) }}<span class="muted small"> / 月</span></div>
        <div class="small">年均 <b class="num">{{ money(t.yearly, t.currency) }}</b></div>
        <div v-if="t.oneTime" class="muted small">一次性付款 {{ money(t.oneTime, t.currency) }}</div>
      </div>
    </section>
    <p v-else class="panel muted small">还没有填写价格的节点。在节点的“编辑”中填写续费价格、币种与计费周期后，这里会自动汇总。</p>

    <section v-if="parts.length" class="panel section">
      <div class="section-head">
        <h3>月均开销拆分</h3>
        <div class="segmented" role="group" aria-label="拆分方式">
          <button type="button" :class="{ active: by === 'provider' }" @click="by = 'provider'">按供应商</button>
          <button type="button" :class="{ active: by === 'group' }" @click="by = 'group'">按分组</button>
        </div>
      </div>
      <ul class="rows">
        <li v-for="p in parts" :key="p.currency + p.key">
          <span class="name">{{ p.key }}</span>
          <span class="muted small">{{ p.count }} 个</span>
          <span class="num">{{ money(p.monthly, p.currency) }}</span>
        </li>
      </ul>
    </section>

    <section class="panel section">
      <h3>30 天内续费</h3>
      <ul v-if="renewals.length" class="rows">
        <li v-for="r in renewals" :key="r.node.id">
          <RouterLink :to="`/servers/${r.node.id}`" class="name">{{ r.node.name }}</RouterLink>
          <span class="small" :class="r.days < 0 ? 'bad' : r.days <= 7 ? 'warn' : 'muted'">{{ r.node.expire_date }} · {{ dueText(r.days) }}</span>
          <span class="num">
            {{ r.node.price_cents ? money(r.node.price_cents, r.node.currency) : '—' }}
            <span v-if="r.node.billing_period" class="muted small">{{ periodNames[r.node.billing_period] ?? '' }}</span>
          </span>
        </li>
      </ul>
      <p v-else class="muted small">30 天内没有到期的节点。</p>
    </section>

    <section v-if="cloud.length || cloudError" class="panel section">
      <h3>云账户本月费用</h3>
      <p v-if="cloudError" class="small bad">{{ cloudError }}</p>
      <ul class="rows">
        <li v-for="a in cloud" :key="a.id">
          <RouterLink to="/cloud" class="name">{{ a.name }}</RouterLink>
          <span class="muted small">{{ a.current_cost!.period }}{{ a.current_cost!.forecast_cents != null ? ` · 预估 ${fmtMoney(a.current_cost!.forecast_cents, a.current_cost!.currency)}` : '' }}</span>
          <span class="num">{{ fmtMoney(a.current_cost!.amount_cents, a.current_cost!.currency) }}</span>
        </li>
      </ul>
    </section>

    <p v-if="unpriced.length" class="muted small note">
      {{ unpriced.length }} 个节点没有填写价格或计费周期，未计入统计：
      <template v-for="(n, i) in unpriced.slice(0, 8)" :key="n.id">
        <RouterLink :to="`/servers/${n.id}/edit`">{{ n.name }}</RouterLink>{{ i < Math.min(unpriced.length, 8) - 1 ? '、' : '' }}
      </template>
      <template v-if="unpriced.length > 8"> 等</template>
    </p>
    <p v-if="nodes.some((n) => monthlyCents(n) != null)" class="muted small note">月均 = 续费价格 ÷ 周期月数；年均 = 月均 × 12。</p>
  </main>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-4); max-width: 72ch; }
.totals { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: var(--space-3); }
.total { display: flex; flex-direction: column; gap: var(--space-1); }
.big { font-size: var(--font-num); line-height: var(--line-num); font-weight: var(--weight-strong); }
.section { margin-top: var(--space-4); }
.section h3 { margin: 0 0 var(--space-3); font-size: var(--font-lg); }
.section-head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); flex-wrap: wrap; margin-bottom: var(--space-3); }
.section-head h3 { margin: 0; }
.rows { list-style: none; margin: 0; padding: 0; }
.rows li { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: var(--space-3); align-items: baseline; padding: var(--space-2) 0; }
.rows li + li { border-top: 1px solid var(--border); }
.name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.num { text-align: right; white-space: nowrap; }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
.note { margin-top: var(--space-4); }
</style>
