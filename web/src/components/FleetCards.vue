<script setup lang="ts">
// 舰队总览（修订第 174 条）：仪表板顶部三张卡片，把分散在各节点与云账户的数据拼成总览——
// 本月剩余流量与“预计哪台先用完”、每月总成本（节点续费折算 + 云账单本月预估）、未来 30 天到期。
// 成本卡只给管理员（云账户只有管理员能读）；资料不全时提醒（没填价格的节点、未关联节点的云实例）。
import { computed, onMounted, ref } from 'vue'
import { listCloudAccounts, listCloudInstances, type CloudAccount } from '../api'
import { costFleet, renewalsSoon, trafficFleet } from '../fleet'
import { fmtMoney, fmtTraffic } from '../format'
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

function dueText(days: number) {
  if (days < 0) return `已过期 ${-days} 天`
  if (days === 0) return '今天到期'
  return `${days} 天后`
}
</script>

<template>
  <!-- 只显示有内容的卡片（修订第 178 条）：没有流量额度、价格或近期到期时不占位置 -->
  <div v-if="shown" class="fleet">
    <!-- 流量 -->
    <section v-if="traffic.limited" class="panel card">
      <h3>本月剩余流量</h3>
      <p class="big num">{{ fmtTraffic(traffic.remaining) }}</p>
        <p class="muted small">{{ traffic.limited }} 台节点有流量额度</p>
        <ul v-if="traffic.risks.length" class="list">
          <li v-for="r in traffic.risks.slice(0, 3)" :key="r.server.id">
            <RouterLink :to="`/servers/${r.server.id}?tab=traffic`">{{ r.server.name }}</RouterLink>
            <span class="num" :class="r.daysLeft === 0 ? 'bad' : 'warn'">
              {{ r.daysLeft === 0 ? `已用完（${Math.round(r.pct)}%）` : `约 ${r.daysLeft} 天后用完` }}
            </span>
          </li>
        </ul>
        <p v-else class="ok small">按当前用量，本周期没有节点会用完流量。</p>
    </section>

    <!-- 成本（管理员） -->
    <section v-if="!isViewer && cost.lines.length" class="panel card">
      <h3>每月总成本</h3>
      <p v-for="l in cost.lines.slice(0, 2)" :key="l.currency" class="big num">
          {{ money(l.nodes + l.cloud, l.currency) }}<small class="muted"> / 月</small>
        </p>
        <p class="muted small">
          <template v-for="(l, i) in cost.lines.slice(0, 2)" :key="'d' + l.currency">
            <template v-if="i"> · </template>节点续费 {{ money(l.nodes, l.currency) }}<template v-if="l.cloud"> + 云账单本月 {{ money(l.cloud, l.currency) }}</template>
          </template>
        </p>
      <ul v-if="cost.unpriced || unlinked || cost.inCloud" class="hints small">
        <li v-if="cost.unpriced">{{ cost.unpriced }} 台节点没填价格（在节点编辑页填写），总成本不完整</li>
        <li v-if="unlinked"><RouterLink to="/cloud">{{ unlinked }} 个云实例未关联节点</RouterLink></li>
        <li v-if="cost.inCloud" class="muted">{{ cost.inCloud }} 台关联云实例的节点按云账单计算，不重复计入续费价格</li>
      </ul>
    </section>

    <!-- 到期 -->
    <section v-if="renewals.length" class="panel card">
      <h3>未来 30 天到期</h3>
      <p class="big num">{{ renewals.length }}<small class="muted"> 台</small></p>
        <ul class="list">
          <li v-for="r in renewals.slice(0, 4)" :key="r.node.id">
            <RouterLink :to="`/servers/${r.node.id}`">{{ r.node.name }}</RouterLink>
            <span class="num" :class="r.days <= 3 ? 'bad' : r.days <= 7 ? 'warn' : 'muted'">{{ dueText(r.days) }}</span>
          </li>
        </ul>
    </section>
  </div>
</template>

<style scoped>
.fleet { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--space-3); margin-bottom: var(--space-4); }
.card { padding: var(--space-4); display: flex; flex-direction: column; gap: var(--space-1); min-width: 0; }
.card h3 { margin: 0; font-size: var(--font-sm); color: var(--text-muted); font-weight: var(--weight-regular); }
.card p { margin: 0; }
.big { font-size: 24px; line-height: 32px; font-weight: var(--weight-strong); }
.big small { font-size: var(--font-sm); font-weight: var(--weight-regular); }
.list { list-style: none; margin: var(--space-2) 0 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-1); }
.list li { display: flex; justify-content: space-between; gap: var(--space-2); font-size: var(--font-sm); min-width: 0; }
.list a { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.list span { white-space: nowrap; }
.hints { list-style: none; margin: var(--space-2) 0 0; padding: 0; display: flex; flex-direction: column; gap: 2px; color: var(--warn); }
.ok { color: var(--ok); }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
@media (max-width: 900px) {
  .fleet { grid-template-columns: minmax(0, 1fr); }
}
</style>
