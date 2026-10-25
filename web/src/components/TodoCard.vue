<script setup lang="ts">
// 待处理（修订第 241 条，需求说明书 T-021）：仪表板顶部把分散在各处、需要动手处理的事项列在一起，每项一行并直达处理的位置。
//   离线节点、进行中的告警、流量将在本周期用完、7 天内到期的节点、7 天内结束的订阅、到了“扣费前提醒”天数内的订阅、
//   未关联节点的云实例、待安装的节点
// 严重的排在前面，默认显示 5 项，其余点“全部”展开。没有事项时显示一行“没有待处理的事项”。
// 订阅与云实例只有管理员能读；查看者只看到节点相关的事项。只读取已有接口，不新增请求类型。
import { computed, onMounted, ref } from 'vue'
import { listCloudInstances, listSubscriptions, type Subscription } from '../api'
import { renewalsSoon, trafficFleet } from '../fleet'
import { t } from '../i18n'
import { activeAlertCount } from '../metrics'
import { alertsOn, installed, isViewer, state } from '../store'
import Icon from './Icon.vue'

type Level = 'bad' | 'warn' | 'info'
interface Todo {
  key: string
  level: Level
  text: string
  to: string
}

const subs = ref<Subscription[]>([])
const unlinked = ref(0)
onMounted(async () => {
  if (isViewer.value) return
  const [s, inst] = await Promise.allSettled([listSubscriptions(), listCloudInstances()])
  if (s.status === 'fulfilled') subs.value = s.value
  if (inst.status === 'fulfilled') unlinked.value = inst.value.filter((i) => !i.server_id).length
})

const today = () => {
  const n = new Date()
  return new Date(n.getFullYear(), n.getMonth(), n.getDate()).getTime()
}
const daysTo = (ymd: string) => Math.round((new Date(`${ymd}T00:00:00`).getTime() - today()) / 86400000)
const when = (d: number) => (d < 0 ? t('已过期 {n} 天', { n: -d }) : d === 0 ? t('今天') : t('{n} 天后', { n: d }))

const items = computed<Todo[]>(() => {
  const out: Todo[] = []
  const offline = installed.value.filter((s) => s.status === 'offline' && !s.maintenance)
  if (offline.length) {
    out.push({ key: 'offline', level: 'bad', to: offline.length === 1 ? `/servers/${offline[0].id}` : '/?f=offline',
      text: offline.length === 1 ? t('{name} 离线', { name: offline[0].name }) : t('{n} 台节点离线', { n: offline.length }) })
  }
  const alerts = alertsOn.value ? activeAlertCount(state.servers) : 0
  if (alerts) out.push({ key: 'alerts', level: 'warn', to: '/alerts', text: t('{n} 条告警进行中', { n: alerts }) })
  for (const r of trafficFleet(installed.value).risks) {
    out.push({ key: `traffic${r.server.id}`, level: r.daysLeft === 0 ? 'bad' : 'warn', to: `/servers/${r.server.id}?tab=traffic`,
      text: r.daysLeft === 0 ? t('{name} 本周期流量已用完', { name: r.server.name })
        : t('{name} 流量约 {n} 天后用完（重置前）', { name: r.server.name, n: r.daysLeft }) })
  }
  for (const r of renewalsSoon(installed.value, 7)) {
    out.push({ key: `expire${r.node.id}`, level: r.days <= 3 ? 'bad' : 'warn', to: `/servers/${r.node.id}`,
      text: t('{name} 到期：{when}', { name: r.node.name, when: when(r.days) }) })
  }
  for (const s of subs.value) {
    if (s.ended) continue
    const end = s.end_date ? daysTo(s.end_date) : null
    const charge = s.next_charge ? daysTo(s.next_charge) : null
    if (end != null && end >= 0 && end <= 7) {
      out.push({ key: `subend${s.id}`, level: 'warn', to: '/subscriptions', text: t('订阅 {name} 结束：{when}', { name: s.name, when: when(end) }) })
    } else if (charge != null && charge >= 0 && s.remind_days > 0 && charge <= s.remind_days) {
      // 扣费只按订阅自己的“扣费前提醒”天数列出（不提醒的订阅不算待处理）
      out.push({ key: `subcharge${s.id}`, level: 'info', to: '/subscriptions', text: t('订阅 {name} 扣费：{when}', { name: s.name, when: when(charge) }) })
    }
  }
  if (unlinked.value) {
    out.push({ key: 'unlinked', level: 'info', to: '/cloud?unlinked=1#instances', text: t('{n} 个云实例未关联节点', { n: unlinked.value }) })
  }
  const pending = state.servers.filter((s) => s.status === 'pending').length
  if (pending && !isViewer.value) out.push({ key: 'pending', level: 'info', to: '/#pending', text: t('{n} 台节点等待安装 Agent', { n: pending }) })
  const rank: Record<Level, number> = { bad: 0, warn: 1, info: 2 }
  return out.sort((a, b) => rank[a.level] - rank[b.level])
})

const LIMIT = 5
const expanded = ref(false)
const mobileOpen = ref(false)
// 手机展开后显示全部；电脑默认 5 项
const shown = computed(() => (expanded.value || mobileOpen.value ? items.value : items.value.slice(0, LIMIT)))
const levelName: Record<Level, string> = { bad: '严重', warn: '注意', info: '提示' }
</script>

<template>
  <section v-if="state.loaded && installed.length" class="panel todo" :aria-label="t('待处理')">
    <div class="head">
      <h2>{{ t('待处理') }}</h2>
      <span v-if="items.length" class="count num">{{ items.length }}</span>
      <span v-else class="ok small"><Icon name="shield" :size="14" />{{ t('没有待处理的事项') }}</span>
      <button v-if="items.length > LIMIT" type="button" class="text small more wide-only" @click="expanded = !expanded">
        {{ expanded ? t('收起') : t('全部 {n}', { n: items.length }) }}
      </button>
      <!-- 手机（修订第 248 条，UI-007）：默认收成一行，显示最紧急的一项，点开看全部；节点卡片更早出现在首屏 -->
      <button v-if="items.length" type="button" class="text peek" :aria-expanded="mobileOpen" @click="mobileOpen = !mobileOpen">
        <i class="dot" :class="items[0].level" />
        <span class="txt">{{ mobileOpen ? t('收起') : items[0].text }}</span>
        <Icon name="chevron-right" :size="14" class="go" :class="{ open: mobileOpen }" />
      </button>
    </div>
    <ul v-if="items.length" :class="{ closed: !mobileOpen }">
      <li v-for="x in shown" :key="x.key">
        <RouterLink :to="x.to" class="row">
          <i class="dot" :class="x.level" :title="t(levelName[x.level])" />
          <span class="sr-only">{{ t(levelName[x.level]) }}：</span>
          <span class="txt">{{ x.text }}</span>
          <Icon name="chevron-right" :size="14" class="go" />
        </RouterLink>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.todo { padding: var(--space-3) var(--space-4); margin-bottom: var(--space-3); }
.head { display: flex; align-items: center; gap: var(--space-2); }
.head h2 { margin: 0; font-size: var(--font-md); }
.count { min-width: 20px; height: 20px; padding: 0 6px; border-radius: var(--radius-full); background: var(--surface-2); font-size: var(--font-xs);
  line-height: 20px; text-align: center; font-weight: var(--weight-strong); }
.ok { color: var(--ok); display: inline-flex; align-items: center; gap: 4px; }
.more { margin-left: auto; }
ul { list-style: none; margin: var(--space-2) 0 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 0 var(--space-4); }
.row { display: flex; align-items: center; gap: var(--space-2); padding: 6px 0; color: var(--text); text-decoration: none; min-width: 0;
  min-height: 32px; }
.row:hover .txt { text-decoration: underline; }
.txt { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: var(--font-md); }
.dot { width: 8px; height: 8px; border-radius: 50%; flex: none; background: var(--accent); }
.dot.bad { background: var(--bad); }
.dot.warn { background: var(--warn); }
.go { color: var(--text-muted); flex: none; }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
.peek { display: none; }
@media (max-width: 600px) {
  .todo { padding: var(--space-2) var(--space-3); margin-bottom: var(--space-2); }
  ul { grid-template-columns: minmax(0, 1fr); }
  /* 收成一行（修订第 248 条） */
  .wide-only, ul.closed { display: none; }
  .peek { display: flex; align-items: center; gap: var(--space-2); flex: 1; min-width: 0; padding: 0; min-height: 44px; color: var(--text);
    font: inherit; text-align: left; }
  .peek .go { transition: transform .15s; }
  .peek .go.open { transform: rotate(90deg); }
}
</style>
