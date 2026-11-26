<script setup lang="ts">
// 续订日历（修订第 313 条，参考 Wallos 的日历页）：按月显示每天扣费的订阅（图标）与当天合计，可前后翻月；
// 点某天在下方列出当天的订阅，点订阅进入编辑。金额换算为人民币（缺少汇率的按原币另列）。周一为每周第一天。
// 修订第 331 条（美化）：标题与“本月 / 已扣 / 待扣”汇总；完整的周格子（前后月份的日期变淡）；
// 电脑宽度每格最多列出 2 个订阅（图标、名称、金额），其余“+N 项”；手机只显示图标；选中日期的订阅按卡片列出。
import { computed, ref } from 'vue'
import { subscriptionLogoURL, type Subscription } from '../api'
import { fmtMoney } from '../format'
import { locale, t } from '../i18n'
import { chargesBetween, cyclePay, monthStart, toCny, ymd, type Rates } from '../subscriptions'
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

type Day = { date: Date; key: string; subs: Subscription[]; cny: number; other: Map<string, number>; out?: boolean }
const newDay = (d: Date, out = false): Day => ({ date: d, key: ymd(d), subs: [], cny: 0, other: new Map(), out })
// 本月每天的扣费
const days = computed<Day[]>(() => {
  const from = month.value
  const to = monthStart(today, offset.value + 1)
  const map = new Map<string, Day>()
  for (let d = new Date(from); d < to; d = new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1)) map.set(ymd(d), newDay(d))
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
// 补齐整周：第一天之前与最后一天之后用前后月份的日期（变淡，不可点）
const cells = computed<Day[]>(() => {
  const first = month.value
  const lead = (first.getDay() + 6) % 7
  const before = Array.from({ length: lead }, (_, i) => newDay(new Date(first.getFullYear(), first.getMonth(), i - lead + 1), true))
  const last = days.value.at(-1)!.date
  const tail = (7 - ((lead + days.value.length) % 7)) % 7
  const after = Array.from({ length: tail }, (_, i) => newDay(new Date(last.getFullYear(), last.getMonth(), last.getDate() + i + 1), true))
  return [...before, ...days.value, ...after]
})
const sumOf = (list: Day[]) => list.reduce((a, d) => a + d.cny, 0)
const total = computed(() => sumOf(days.value))
const count = computed(() => days.value.reduce((a, d) => a + d.subs.length, 0))
const paid = computed(() => sumOf(days.value.filter((d) => d.date < today)))
const due = computed(() => sumOf(days.value.filter((d) => d.date >= today)))
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
const weekend = (d: Date) => d.getDay() === 0 || d.getDay() === 6
const logo = (s: Subscription) => (s.logo_version ? subscriptionLogoURL(s) : '')
const dayLabel = (d: Date) => t('{m} 月 {d} 日', { m: d.getMonth() + 1, d: d.getDate() })
const cnyLine = (s: Subscription) => {
  if (s.currency === 'CNY') return ''
  const v = toCny(s.price_cents, s.currency, props.rates)
  return v == null ? '' : `≈ ${fmtMoney(v, 'CNY')}`
}
const SHOWN = 2
</script>

<template>
  <section class="panel cal">
    <header class="cal-head">
      <div class="cal-title">
        <h2>{{ title }}</h2>
        <span class="small muted num">{{ t('{n} 次扣费 · {v}', { n: count, v: fmtMoney(total, 'CNY') }) }} {{ totalOther }}</span>
      </div>
      <dl v-if="count" class="cal-sum num">
        <div><dt>{{ t('已扣') }}</dt><dd>{{ fmtMoney(paid, 'CNY') }}</dd></div>
        <div><dt>{{ t('待扣') }}</dt><dd class="due">{{ fmtMoney(due, 'CNY') }}</dd></div>
      </dl>
      <div class="nav">
        <button type="button" class="nav-btn" :aria-label="t('上个月')" @click="go(-1)"><Icon name="arrow-left" :size="16" /></button>
        <button type="button" class="nav-btn today-btn" :disabled="offset === 0" @click="go(-offset)">{{ t('本月') }}</button>
        <button type="button" class="nav-btn flip" :aria-label="t('下个月')" @click="go(1)"><Icon name="arrow-left" :size="16" /></button>
      </div>
    </header>

    <div class="grid" role="grid" :aria-label="title">
      <span v-for="(w, i) in weekdays" :key="w" class="wd small" :class="{ we: i >= 5 }" role="columnheader">{{ w }}</span>
      <template v-for="d in cells" :key="d.key">
        <span v-if="d.out" class="day out" aria-hidden="true"><span class="dn num">{{ d.date.getDate() }}</span></span>
        <button v-else type="button" role="gridcell" class="day"
          :class="{ today: isToday(d.date), picked: picked === d.key, has: d.subs.length, past: d.date < today, we: weekend(d.date) }"
          :aria-label="`${dayLabel(d.date)} ${d.subs.map((s) => s.name).join('、')}`" :aria-selected="picked === d.key" @click="picked = d.key">
          <span class="top"><span class="dn num">{{ d.date.getDate() }}</span><span v-if="d.cny && d.subs.length > 1" class="amt num">{{ fmtMoney(d.cny, 'CNY') }}</span></span>
          <!-- 电脑：订阅小条 -->
          <span v-if="d.subs.length" class="chips">
            <span v-for="s in d.subs.slice(0, SHOWN)" :key="s.id" class="chip">
              <SubAvatar :brand="s.brand" :name="s.name" :size="16" :src="logo(s)" />
              <span class="cn">{{ s.name }}</span>
              <span class="cp num">{{ fmtMoney(s.price_cents, s.currency) }}</span>
            </span>
            <span v-if="d.subs.length > SHOWN" class="more small num">{{ t('+{n} 项', { n: d.subs.length - SHOWN }) }}</span>
          </span>
          <!-- 手机：只显示图标 -->
          <span v-if="d.subs.length" class="dots">
            <SubAvatar v-for="s in d.subs.slice(0, 3)" :key="s.id" :brand="s.brand" :name="s.name" :size="16" :src="logo(s)" />
          </span>
        </button>
      </template>
    </div>

    <div v-if="pickedDay" class="picked-list">
      <h3>{{ dayLabel(pickedDay.date) }}<span v-if="pickedDay.subs.length" class="small muted num">{{ t('{n} 次扣费 · {v}', { n: pickedDay.subs.length, v: fmtMoney(pickedDay.cny, 'CNY') }) }} {{ otherText(pickedDay.other) }}</span></h3>
      <p v-if="!pickedDay.subs.length" class="small muted empty">{{ t('这一天没有扣费。') }}</p>
      <button v-for="s in pickedDay.subs" :key="s.id" type="button" class="pick-row" @click="emit('open', s)">
        <SubAvatar :brand="s.brand" :name="s.name" :size="32" :src="logo(s)" />
        <span class="pr-main"><b class="ellipsis">{{ s.name }}</b><span class="small muted">{{ t(cyclePay[s.cycle]) }}</span></span>
        <span class="pr-amt"><b class="num">{{ fmtMoney(s.price_cents, s.currency) }}</b><span v-if="cnyLine(s)" class="small muted num">{{ cnyLine(s) }}</span></span>
        <Icon name="chevron-right" :size="16" class="muted" />
      </button>
    </div>
  </section>
</template>

<style scoped>
.cal { padding: var(--space-4) var(--space-5) var(--space-5); }
.cal-head { display: flex; align-items: center; gap: var(--space-4); flex-wrap: wrap; }
.cal-title { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.cal-title h2 { margin: 0; font-size: var(--font-xl); line-height: var(--line-xl); }
.cal-sum { display: flex; gap: var(--space-5); margin: 0; }
.cal-sum div { display: flex; flex-direction: column; align-items: flex-end; }
.cal-sum dt { font-size: var(--font-sm); color: var(--text-muted); }
.cal-sum dd { margin: 0; font-weight: var(--weight-strong); }
.cal-sum .due { color: var(--accent); }
.nav { display: inline-flex; border: 1px solid var(--border); border-radius: var(--radius-sm); overflow: hidden; }
.nav-btn { background: var(--surface); border: 0; color: var(--text-muted); padding: 6px 10px; min-height: 32px; display: inline-flex; align-items: center;
  font-size: var(--font-sm); cursor: pointer; }
.nav-btn + .nav-btn { border-left: 1px solid var(--border); }
.nav-btn:disabled { color: var(--text-muted); opacity: .5; cursor: default; }
.flip :deep(svg) { transform: scaleX(-1); }

/* 整月格子：细线分隔，像纸质日历 */
.grid { display: grid; grid-template-columns: repeat(7, minmax(0, 1fr)); margin-top: var(--space-4); border: 1px solid var(--border);
  border-radius: var(--radius-md); overflow: hidden; background: var(--border); gap: 1px; }
.wd { background: var(--surface-2); text-align: center; padding: var(--space-2) 0; color: var(--text-muted); font-weight: var(--weight-strong); }
.wd.we { color: var(--text-muted); opacity: .75; }
.day { position: relative; display: flex; flex-direction: column; align-items: stretch; gap: 4px; min-height: 96px; padding: 6px; background: var(--surface);
  border: 0; border-radius: 0; color: var(--text); font: inherit; text-align: left; cursor: pointer; min-width: 0; }
.day.out { background: var(--surface); cursor: default; }
.day.out .dn { color: var(--text-muted); opacity: .5; }
.day.we:not(.has) { background: color-mix(in srgb, var(--surface-2) 50%, var(--surface)); }
.day.has { background: color-mix(in srgb, var(--accent) 6%, var(--surface)); }
.day.past .dn, .day.past .chips { opacity: .6; }
.day.picked { box-shadow: inset 0 0 0 2px var(--accent); z-index: 1; }
.top { display: flex; align-items: center; justify-content: space-between; gap: 4px; }
.dn { display: inline-flex; align-items: center; justify-content: center; min-width: 24px; height: 24px; border-radius: var(--radius-full); font-size: var(--font-sm); }
.day.today .dn { background: var(--accent); color: var(--on-accent); font-weight: var(--weight-strong); }
.amt { font-size: var(--font-xs); color: var(--text-muted); white-space: nowrap; }
.chips { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.chip { display: flex; align-items: center; gap: 4px; min-width: 0; padding: 2px 4px; border-radius: var(--radius-sm); background: var(--surface);
  border: 1px solid var(--border); font-size: var(--font-xs); line-height: 1.3; }
.chip .cn { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.chip .cp { color: var(--text-muted); white-space: nowrap; }
.more { color: var(--text-muted); padding-left: 4px; font-size: var(--font-xs); }
.dots { display: none; }
@media (hover: hover) { .day:not(.out):hover { background: var(--surface-2); filter: none; } .pick-row:hover { background: var(--surface-2); filter: none; } }

.picked-list { margin-top: var(--space-4); display: flex; flex-direction: column; gap: var(--space-2); }
.picked-list h3 { margin: 0; display: flex; align-items: baseline; gap: var(--space-3); font-size: var(--font-md); }
.empty { margin: 0; }
.pick-row { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-2) var(--space-3); border-radius: var(--radius-md); background: var(--surface);
  border: 1px solid var(--border); color: var(--text); font: inherit; text-align: left; cursor: pointer; min-height: 0; }
.pr-main { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.pr-main .ellipsis { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.pr-amt { display: flex; flex-direction: column; align-items: flex-end; }

/* 窄屏：格子变矮，订阅只显示图标；汇总换到标题下 */
@media (max-width: 720px) {
  .cal { padding: var(--space-3) var(--space-3) var(--space-4); }
  .cal-head { gap: var(--space-2) var(--space-3); }
  .cal-title { flex-basis: calc(100% - 140px); }
  .cal-sum { order: 3; flex-basis: 100%; gap: var(--space-4); }
  .cal-sum div { flex-direction: row; gap: var(--space-1); align-items: baseline; }
  .day { min-height: 56px; padding: 4px 2px; align-items: center; }
  .top { justify-content: center; }
  .amt, .chips { display: none; }
  .dots { display: flex; justify-content: center; gap: 1px; flex-wrap: wrap; }
}
</style>
