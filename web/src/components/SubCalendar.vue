<script setup lang="ts">
// 续订日历（修订第 313 条，参考 Wallos 的日历页）：按月显示每天扣费的订阅（图标）与当天合计，可前后翻月；
// 点某天在下方列出当天的订阅，点订阅进入编辑。金额换算为人民币（缺少汇率的按原币另列）。周一为每周第一天。
import { computed, ref } from 'vue'
import { subscriptionLogoURL, type Subscription } from '../api'
import { fmtMoney } from '../format'
import { locale, t } from '../i18n'
import { chargesBetween, monthStart, toCny, ymd, type Rates } from '../subscriptions'
import Icon from './Icon.vue'
import SubAvatar from './SubAvatar.vue'

const props = defineProps<{
  /** 全部订阅 */
  list: Subscription[]
  /** 汇率 */
  rates: Rates
}>()
const emit = defineEmits<{ open: [s: Subscription] }>()

const now = new Date()
const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
const offset = ref(0)
const month = computed(() => monthStart(today, offset.value))
const title = computed(() => month.value.toLocaleString(locale.value === 'en' ? 'en' : 'zh-CN', { year: 'numeric', month: 'long' }))
const weekdays = computed(() => (locale.value === 'en' ? ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'] : ['一', '二', '三', '四', '五', '六', '日']))

type Day = { date: Date; key: string; subs: Subscription[]; cny: number; other: Map<string, number> }
// 本月每天的扣费
const days = computed<Day[]>(() => {
  const from = month.value
  const to = monthStart(today, offset.value + 1)
  const map = new Map<string, Day>()
  for (let d = new Date(from); d < to; d = new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1)) {
    map.set(ymd(d), { date: d, key: ymd(d), subs: [], cny: 0, other: new Map() })
  }
  for (const s of props.list) {
    for (const d of chargesBetween(s, from, to)) {
      const x = map.get(ymd(d))
      if (!x) continue
      x.subs.push(s)
      const v = toCny(s.price_cents, s.currency, props.rates)
      if (v == null) x.other.set(s.currency, (x.other.get(s.currency) ?? 0) + s.price_cents)
      else x.cny += v
    }
  }
  return [...map.values()]
})
// 第一天前补空格（周一为第一列）
const lead = computed(() => (month.value.getDay() + 6) % 7)
const total = computed(() => days.value.reduce((a, d) => a + d.cny, 0))
const count = computed(() => days.value.reduce((a, d) => a + d.subs.length, 0))
const otherText = (m: Map<string, number>) => [...m].map(([c, v]) => `+ ${fmtMoney(v, c)}`).join(' ')
const totalOther = computed(() => {
  const m = new Map<string, number>()
  for (const d of days.value) for (const [c, v] of d.other) m.set(c, (m.get(c) ?? 0) + v)
  return otherText(m)
})
const picked = ref<string | null>(ymd(today))
const pickedDay = computed(() => days.value.find((d) => d.key === picked.value) ?? null)
function go(n: number) {
  offset.value += n
  picked.value = offset.value === 0 ? ymd(today) : null
}
const isToday = (d: Date) => d.getTime() === today.getTime()
const logo = (s: Subscription) => (s.logo_version ? subscriptionLogoURL(s) : '')
const dayLabel = (d: Date) => t('{m} 月 {d} 日', { m: d.getMonth() + 1, d: d.getDate() })
</script>

<template>
  <section class="panel cal">
    <header class="cal-head">
      <button type="button" class="icon-btn" :aria-label="t('上个月')" @click="go(-1)"><Icon name="arrow-left" :size="18" /></button>
      <div class="cal-title">
        <b>{{ title }}</b>
        <span class="small muted num">{{ t('{n} 次扣费 · {v}', { n: count, v: fmtMoney(total, 'CNY') }) }} {{ totalOther }}</span>
      </div>
      <button type="button" class="icon-btn flip" :aria-label="t('下个月')" @click="go(1)"><Icon name="arrow-left" :size="18" /></button>
    </header>
    <button v-if="offset !== 0" type="button" class="text back-today small" @click="go(-offset)">{{ t('回到本月') }}</button>
    <div class="grid" role="grid" :aria-label="title">
      <span v-for="w in weekdays" :key="w" class="wd small muted" role="columnheader">{{ w }}</span>
      <span v-for="i in lead" :key="`l${i}`" />
      <button v-for="d in days" :key="d.key" type="button" role="gridcell" class="day"
        :class="{ today: isToday(d.date), picked: picked === d.key, has: d.subs.length, past: d.date < today }"
        :aria-label="`${dayLabel(d.date)} ${d.subs.map((s) => s.name).join('、')}`" @click="picked = d.key">
        <span class="dn num">{{ d.date.getDate() }}</span>
        <span v-if="d.subs.length" class="avs">
          <SubAvatar :brand="d.subs[0].brand" :name="d.subs[0].name" :size="20" :src="logo(d.subs[0])" />
          <small v-if="d.subs.length > 1" class="more num">+{{ d.subs.length - 1 }}</small>
        </span>
        <span v-if="d.cny" class="amt num">{{ fmtMoney(d.cny, 'CNY') }}</span>
      </button>
    </div>
    <div v-if="pickedDay" class="picked-list">
      <h3 class="small muted">{{ dayLabel(pickedDay.date) }}</h3>
      <p v-if="!pickedDay.subs.length" class="small muted">{{ t('这一天没有扣费。') }}</p>
      <button v-for="s in pickedDay.subs" :key="s.id" type="button" class="pick-row" @click="emit('open', s)">
        <SubAvatar :brand="s.brand" :name="s.name" :size="28" :src="logo(s)" />
        <span class="ellipsis">{{ s.name }}</span>
        <b class="num">{{ fmtMoney(s.price_cents, s.currency) }}</b>
      </button>
    </div>
  </section>
</template>

<style scoped>
.cal { padding: var(--space-3) var(--space-4) var(--space-4); }
.cal-head { display: flex; align-items: center; gap: var(--space-2); }
.cal-title { flex: 1; display: flex; flex-direction: column; align-items: center; text-align: center; }
.icon-btn { background: none; border: 0; padding: var(--space-2); color: var(--text-muted); min-height: 0; display: inline-flex; }
.flip { transform: scaleX(-1); }
.back-today { display: block; margin: 0 auto; background: none; border: 0; color: var(--accent); min-height: 0; }
.grid { display: grid; grid-template-columns: repeat(7, minmax(0, 1fr)); gap: 4px; margin-top: var(--space-3); }
.wd { text-align: center; padding-bottom: var(--space-1); }
.day { display: flex; flex-direction: column; align-items: center; gap: 2px; min-height: 64px; padding: 4px 2px; border-radius: var(--radius-sm);
  background: none; border: 1px solid transparent; color: var(--text); font: inherit; cursor: pointer; min-width: 0; }
.day.has { background: var(--surface-2); }
.day.past { color: var(--text-muted); }
.day.today .dn { color: var(--on-accent); background: var(--accent); border-radius: var(--radius-full); padding: 0 6px; }
.day.picked { border-color: var(--accent); }
.dn { font-size: var(--font-sm); line-height: var(--line-sm); }
.avs { display: flex; align-items: center; }
.more { font-size: 10px; color: var(--text-muted); margin-left: 1px; }
.amt { font-size: 10px; color: var(--text-muted); max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.picked-list { margin-top: var(--space-4); display: flex; flex-direction: column; gap: var(--space-1); }
.picked-list h3 { margin: 0 0 var(--space-1); font-weight: var(--weight-regular); }
.pick-row { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-2); border-radius: var(--radius-sm); background: none;
  border: 0; color: var(--text); font: inherit; text-align: left; cursor: pointer; min-height: 0; }
.pick-row .ellipsis { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
@media (hover: hover) { .pick-row:hover, .day:hover { background: var(--surface-2); filter: none; } }
@media (max-width: 600px) { .amt { display: none; } .day { min-height: 52px; } }
</style>
