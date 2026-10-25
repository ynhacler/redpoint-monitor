<script setup lang="ts">
// 舰队总览（修订第 174 条）：仪表板顶部的一条总览（修订第 187 条：由三张大卡片压缩为一行），把分散在各节点与云账户的数据拼成总览——
// 本月剩余流量与“预计哪台先用完”、每月总成本（节点续费折算 + 云账单本月预估）、未来 30 天到期。
// 每项只显示一个数字与一行说明；明细（各节点、成本构成、资料不全的提醒）放在悬停提示中。
// 成本卡只给管理员（云账户只有管理员能读）；资料不全时提醒（没填价格的节点、未关联节点的云实例）。
import { computed, onMounted, ref } from 'vue'
import { listCloudAccounts, listCloudInstances, type CloudAccount } from '../api'
import { costFleet, renewalsSoon, trafficFleet } from '../fleet'
import { fmtMoney, fmtTraffic } from '../format'
import { t } from '../i18n'
import { installed, isViewer } from '../store'

const traffic = computed(() => trafficFleet(installed.value))
const renewals = computed(() => renewalsSoon(installed.value, 30))

const accounts = ref<CloudAccount[]>([])
const linked = ref(new Set<number>())
const unlinked = ref(0)
onMounted(async () => {
  if (isViewer.value) return
  try {
    const [a, inst] = await Promise.all([listCloudAccounts(), listCloudInstances()])
    accounts.value = a
    linked.value = new Set(inst.filter((i) => i.server_id).map((i) => i.server_id!))
    unlinked.value = inst.filter((i) => !i.server_id).length
  } catch {
    /* 云账户读取失败时只按节点价格计算 */
  }
})
const cost = computed(() => costFleet(installed.value, accounts.value, linked.value))
const shown = computed(() => traffic.value.limited > 0 || (!isViewer.value && cost.value.lines.length > 0) || renewals.value.length > 0)
const money = (cents: number, cur: string) => fmtMoney(Math.round(cents), cur === '—' ? '' : cur)

// 悬停提示：明细（修订第 187 条）
const trafficTip = computed(() => [t('{n} 台节点有流量额度', { n: traffic.value.limited }),
  ...traffic.value.risks.map((r) => `${r.server.name}：${r.daysLeft === 0 ? t('已用完（{pct}%）', { pct: Math.round(r.pct) }) : t('约 {n} 天后用完', { n: r.daysLeft })}`)].join('\n'))
const costTip = computed(() => [
  ...cost.value.lines.map((l) => `${l.currency}：${t('节点续费 {m}', { m: money(l.nodes, l.currency) })}${l.cloud ? ` + ${t('云账单本月 {m}', { m: money(l.cloud, l.currency) })}` : ''}`),
  cost.value.unpriced ? t('{n} 台节点没填价格（在节点编辑页填写），总成本不完整', { n: cost.value.unpriced }) : '',
  unlinked.value ? t('{n} 个云实例未关联节点', { n: unlinked.value }) : '',
  cost.value.inCloud ? t('{n} 台关联云实例的节点按云账单计算，不重复计入续费价格', { n: cost.value.inCloud }) : '',
].filter(Boolean).join('\n'))
const renewTip = computed(() => renewals.value.map((r) => `${r.node.name}：${dueText(r.days)}`).join('\n'))

function dueText(days: number) {
  if (days < 0) return t('已过期 {n} 天', { n: -days })
  if (days === 0) return t('今天到期')
  return t('{n} 天后', { n: days })
}
</script>

<template>
  <!-- 只显示有内容的项（修订第 178 条） -->
  <div v-if="shown" class="panel fleet">
    <!-- 流量 -->
    <div v-if="traffic.limited" class="item" :title="trafficTip">
      <span class="label">{{ t('本月剩余流量') }}</span>
      <span class="big num">{{ fmtTraffic(traffic.remaining) }}</span>
      <span v-if="traffic.risks.length" class="sub" :class="traffic.risks[0].daysLeft === 0 ? 'bad' : 'warn'">
        <RouterLink :to="`/servers/${traffic.risks[0].server.id}?tab=traffic`">{{ traffic.risks[0].server.name }}</RouterLink>
        {{ traffic.risks[0].daysLeft === 0 ? t('已用完') : t('约 {n} 天后用完', { n: traffic.risks[0].daysLeft }) }}<template v-if="traffic.risks.length > 1"> {{ t('等 {n} 台', { n: traffic.risks.length }) }}</template>
      </span>
      <span v-else class="sub muted">{{ t('{n} 台有额度，本周期够用', { n: traffic.limited }) }}</span>
    </div>

    <!-- 成本（管理员） -->
    <div v-if="!isViewer && cost.lines.length" class="item" :title="costTip">
      <span class="label">{{ t('每月总成本') }}</span>
      <span class="big num">{{ money(cost.lines[0].nodes + cost.lines[0].cloud, cost.lines[0].currency) }}<small> / {{ t('月') }}</small></span>
      <span v-if="cost.lines.length > 1" class="sub muted num">+ {{ cost.lines.slice(1).map((l) => money(l.nodes + l.cloud, l.currency)).join(' + ') }}</span>
      <span v-else-if="cost.unpriced" class="sub warn">{{ t('{n} 台没填价格', { n: cost.unpriced }) }}</span>
      <span v-else-if="unlinked" class="sub"><RouterLink to="/cloud?unlinked=1#instances">{{ t('{n} 个云实例未关联', { n: unlinked }) }}</RouterLink></span>
      <span v-else class="sub muted">{{ t('节点续费 + 云账单') }}</span>
    </div>

    <!-- 到期 -->
    <div v-if="renewals.length" class="item" :title="renewTip">
      <span class="label">{{ t('30 天内到期') }}</span>
      <span class="big num">{{ renewals.length }}<small> {{ t('台') }}</small></span>
      <span class="sub" :class="renewals[0].days <= 3 ? 'bad' : renewals[0].days <= 7 ? 'warn' : 'muted'">
        <RouterLink :to="`/servers/${renewals[0].node.id}`">{{ renewals[0].node.name }}</RouterLink> {{ dueText(renewals[0].days) }}
      </span>
    </div>
  </div>
</template>

<style scoped>
/* 一行总览：各项等宽排列、竖线分隔；手机上两列 */
.fleet { display: flex; flex-wrap: wrap; padding: var(--space-3) 0; margin-bottom: var(--space-3); }
.item { flex: 1 1 180px; display: flex; flex-direction: column; gap: 2px; min-width: 0; padding: 0 var(--space-4); }
.item + .item { border-left: 1px solid var(--border); }
.label { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); }
.big { font-size: var(--font-lg); line-height: var(--line-lg); font-weight: var(--weight-strong); white-space: nowrap; }
.big small { font-size: var(--font-xs); font-weight: var(--weight-regular); color: var(--text-muted); }
.sub { font-size: var(--font-xs); line-height: var(--line-xs); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
.muted { color: var(--text-muted); }
/* 手机（修订第 199 条）：各项仍在一行（最多三项），数字缩小一档 */
@media (max-width: 600px) {
  .fleet { flex-wrap: nowrap; padding: var(--space-2) 0; margin-bottom: var(--space-2); }
  .item { flex: 1 1 0; padding: 0 var(--space-3); }
  .big { font-size: var(--font-md); line-height: var(--line-md); }
}
</style>
