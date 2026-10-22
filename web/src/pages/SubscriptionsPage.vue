<script setup lang="ts">
// 订阅（设计 47，修订第 189 条）：记录影音、工具、云服务等个人订阅的价格、周期与起止日期。
// “列表”按下次扣费日排列，已结束的置灰；“统计”按本月 / 本年 / 全部显示各类别占比（环形图）与前后几个月的扣费（柱状）。
// 添加时先从常用服务目录中选择（按类别、可搜索），也可自定义；图标为内置的标志（SubAvatar），不加载外部图片。
// 金额按币种分别统计，不换算；主币种为折算月费最大的币种。
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ApiError, createSubscription, deleteSubscription, deleteSubscriptionLogo, errorText, listSubscriptions, subscriptionLogoURL,
  updateSubscription, uploadSubscriptionLogo,
  type Subscription, type SubscriptionInput,
} from '../api'
import EmptyState from '../components/EmptyState.vue'
import Icon from '../components/Icon.vue'
import SubAvatar from '../components/SubAvatar.vue'
import { fmtMoney } from '../format'
import {
  byCategory, catalog, categoryColors, categoryNames, chargeText, cycleNames, currencies, mainCurrency, monthStart,
  remindOptions, spendBetween, trialOptions, ymd, type CatalogItem, type Money, type SubCategory,
} from '../subscriptions'

const route = useRoute()
const router = useRouter()
const view = computed(() => (route.query.tab === 'stats' ? 'stats' : 'list'))
const setView = (v: string) => router.replace({ query: { tab: v === 'list' ? undefined : v } })

const list = ref<Subscription[]>([])
const loaded = ref(false)
const error = ref('')
async function load() {
  try {
    list.value = await listSubscriptions()
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '加载失败')
  } finally {
    loaded.value = true
  }
}
onMounted(load)

// ---- 概览 ----
const now = new Date()
const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
const active = computed(() => list.value.filter((s) => !s.ended))
const cur = computed(() => mainCurrency(list.value))
// 主币种大字显示，其他币种在下一行（金额按币种分别统计，不换算）
const mainText = (m: Money) => fmtMoney(m.get(cur.value) ?? 0, cur.value)
const otherText = (m: Money) => [...m.entries()].filter(([c]) => c !== cur.value).map(([c, v]) => `+ ${fmtMoney(v, c)}`).join(' ')
const thisMonth = computed(() => spendBetween(list.value, monthStart(today), monthStart(today, 1)))
const spentSoFar = computed(() => {
  const tomorrow = new Date(today.getFullYear(), today.getMonth(), today.getDate() + 1)
  return spendBetween(list.value, new Date(2000, 0, 1), tomorrow)
})
const next = computed(() => list.value.find((s) => s.next_charge))

// ---- 统计 ----
type Range = 'month' | 'year' | 'all'
const range = ref<Range>('month')
const rangeBounds = computed<[Date, Date]>(() => {
  if (range.value === 'month') return [monthStart(today), monthStart(today, 1)]
  if (range.value === 'year') return [new Date(today.getFullYear(), 0, 1), new Date(today.getFullYear() + 1, 0, 1)]
  const tomorrow = new Date(today.getFullYear(), today.getMonth(), today.getDate() + 1)
  return [new Date(2000, 0, 1), tomorrow]
})
const cats = computed(() => byCategory(list.value, cur.value, ...rangeBounds.value))
const catTotal = computed(() => cats.value.reduce((a, c) => a + c.cents, 0))
const rangeLabel = computed(() => ({ month: '本月预计花费', year: '本年预计花费', all: '累计已花费' })[range.value])
// 环形图：半径 70，线宽 22；各段之间留 2 的空隙
const R = 70
const C = 2 * Math.PI * R
const arcs = computed(() => {
  let offset = 0
  const gap = cats.value.length > 1 ? 2 : 0
  return cats.value.map((c) => {
    const len = (c.pct / 100) * C
    const a = { color: categoryColors[c.category], dash: `${Math.max(0, len - gap)} ${C}`, offset: -offset }
    offset += len
    return a
  })
})
// 前 4 个月到后 4 个月的扣费（主币种）
const bars = computed(() => {
  const out = []
  for (let i = -4; i <= 4; i++) {
    const from = monthStart(today, i)
    const v = spendBetween(list.value.filter((s) => s.currency === cur.value), from, monthStart(today, i + 1)).get(cur.value) ?? 0
    out.push({ key: i, label: from.getMonth() === 0 ? String(from.getFullYear()) : `${from.getMonth() + 1} 月`, cents: v, current: i === 0, future: i > 0 })
  }
  return out
})
const barMax = computed(() => Math.max(1, ...bars.value.map((b) => b.cents)))

// ---- 添加 / 编辑 ----
type Step = 'pick' | 'form'
const step = ref<Step | null>(null)
const editing = ref<Subscription | null>(null)
const pickCat = ref<'all' | 'hot' | SubCategory>('all')
const query = ref('')
const picks = computed(() => {
  const q = query.value.trim().toLowerCase()
  return catalog.filter((c) => (pickCat.value === 'all' || (pickCat.value === 'hot' ? c.hot : c.category === pickCat.value))
    && (!q || c.name.toLowerCase().includes(q) || c.key.includes(q)))
})
const pickTabs = computed(() => [{ k: 'all', t: '全部' }, { k: 'hot', t: '热门' },
  ...(Object.keys(categoryNames) as SubCategory[]).filter((c) => c !== 'other' && catalog.some((x) => x.category === c)).map((c) => ({ k: c, t: categoryNames[c] }))])

const blank = (): SubscriptionInput & { price: string } => ({
  name: '', brand: '', category: 'other', price_cents: 0, price: '', currency: cur.value, cycle: 'monthly', trial_days: 0,
  start_date: ymd(today), end_date: '', remind_days: 0, note: '',
})
const form = reactive(blank())
const fieldErrors = ref<Record<string, string>>({})
// 图标：待上传的 PNG 与预览地址；removeLogo 表示保存时删除已上传的图标
const logoBlob = ref<Blob | null>(null)
const logoPreview = ref('')
const removeLogo = ref(false)
const formLogo = computed(() => logoPreview.value
  || (editing.value?.logo_version && !removeLogo.value ? subscriptionLogoURL(editing.value) : ''))
function resetLogo() {
  logoBlob.value = null
  logoPreview.value = ''
  removeLogo.value = false
}
/** 读取选择的图片，居中裁成正方形并缩放为 128×128 PNG。
 *  用 data: 地址读取与预览：面板的 CSP 只允许 'self' 与 data: 图片，不放开 blob:。 */
async function pickLogo(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (file.size > 10 << 20) {
    fieldErrors.value = { ...fieldErrors.value, logo: '图片不能超过 10 MB' }
    return
  }
  try {
    const src = await new Promise<string>((resolve, reject) => {
      const r = new FileReader()
      r.onload = () => resolve(String(r.result))
      r.onerror = () => reject(r.error)
      r.readAsDataURL(file)
    })
    const img = new Image()
    img.src = src
    await img.decode()
    const side = Math.min(img.naturalWidth, img.naturalHeight)
    if (!side) throw new Error('empty')
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = 128
    canvas.getContext('2d')!.drawImage(img, (img.naturalWidth - side) / 2, (img.naturalHeight - side) / 2, side, side, 0, 0, 128, 128)
    const blob = await new Promise<Blob | null>((r) => canvas.toBlob(r, 'image/png'))
    if (!blob) throw new Error('encode')
    resetLogo()
    logoBlob.value = blob
    logoPreview.value = canvas.toDataURL('image/png')
    const { logo: _, ...rest } = fieldErrors.value
    fieldErrors.value = rest
  } catch {
    fieldErrors.value = { ...fieldErrors.value, logo: '无法读取这张图片' }
  }
}
function clearLogo() {
  resetLogo()
  removeLogo.value = true
}
const saving = ref(false)
const confirmDelete = ref(false)

function openAdd() {
  editing.value = null
  step.value = 'pick'
  query.value = ''
  pickCat.value = 'all'
}
function choose(item: CatalogItem | null) {
  Object.assign(form, blank())
  if (item) Object.assign(form, { name: item.name, brand: item.key, category: item.category, currency: item.currency ?? cur.value })
  fieldErrors.value = {}
  resetLogo()
  step.value = 'form'
}
function openEdit(s: Subscription) {
  editing.value = s
  Object.assign(form, { ...s, price: (s.price_cents / 100).toFixed(2) })
  fieldErrors.value = {}
  resetLogo()
  confirmDelete.value = false
  step.value = 'form'
  window.scrollTo({ top: 0, behavior: 'smooth' })
}
const close = () => { step.value = null; editing.value = null }

async function save() {
  const price = Number(form.price || 0)
  if (!Number.isFinite(price) || price < 0) {
    fieldErrors.value = { price_cents: '请填写价格' }
    return
  }
  const body: SubscriptionInput = {
    name: form.name, brand: form.brand, category: form.category, price_cents: Math.round(price * 100), currency: form.currency,
    cycle: form.cycle, trial_days: form.trial_days, start_date: form.start_date, end_date: form.end_date ?? '', remind_days: form.remind_days, note: form.note,
  }
  saving.value = true
  try {
    const saved = editing.value ? await updateSubscription(editing.value.id, body) : await createSubscription(body)
    editing.value = saved // 图标上传失败时再次保存只修改这一条，不会重复添加
    if (logoBlob.value) await uploadSubscriptionLogo(saved.id, logoBlob.value)
    else if (removeLogo.value && saved.logo_version) await deleteSubscriptionLogo(saved.id)
    resetLogo()
    close()
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.details.length) fieldErrors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
    else fieldErrors.value = { _: errorText(e, '保存失败') }
  } finally {
    saving.value = false
  }
}
async function remove() {
  if (!editing.value) return
  saving.value = true
  try {
    await deleteSubscription(editing.value.id)
    close()
    await load()
  } catch (e) {
    fieldErrors.value = { _: errorText(e, '删除失败') }
  } finally {
    saving.value = false
  }
}
const startText = (s: Subscription) => `${s.start_date.replaceAll('-', '.')} 开始订阅`
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>订阅</h1>
      <button v-if="!step" type="button" class="btn" @click="openAdd"><Icon name="plus" />添加订阅</button>
    </div>
    <p v-if="error" class="banner">{{ error }}</p>

    <!-- 选择服务 -->
    <section v-if="step === 'pick'" class="panel editor">
      <div class="editor-head">
        <h2>选择服务</h2>
        <div class="row-actions">
          <button type="button" class="secondary" @click="choose(null)"><Icon name="edit" />自定义</button>
          <button type="button" class="text" @click="close">取消</button>
        </div>
      </div>
      <nav class="cat-tabs" aria-label="类别">
        <button v-for="t in pickTabs" :key="t.k" type="button" :class="{ active: pickCat === t.k }" @click="pickCat = t.k as typeof pickCat">{{ t.t }}</button>
      </nav>
      <label class="search"><Icon name="search" /><input v-model="query" type="search" placeholder="输入名称搜索" /></label>
      <ul class="pick-list">
        <li v-for="c in picks" :key="c.key">
          <button type="button" class="pick" @click="choose(c)">
            <SubAvatar :brand="c.key" :name="c.name" />
            <span class="pick-name">{{ c.name }}</span>
            <Icon name="plus" class="muted" />
          </button>
        </li>
        <li v-if="!picks.length" class="muted small empty">没有找到，点击右上角“自定义”添加。</li>
      </ul>
    </section>

    <!-- 填写 / 编辑 -->
    <form v-else-if="step === 'form'" class="panel editor" novalidate @submit.prevent="save">
      <div class="form-head">
        <SubAvatar :brand="form.brand" :name="form.name || '?'" :size="64" :src="formLogo" />
        <h2>{{ form.name || '自定义订阅' }}</h2>
        <!-- 上传图标（修订第 189 条）：浏览器中裁剪为正方形并缩放为 128×128 PNG 后上传 -->
        <div class="logo-actions small">
          <label class="text upload">上传图标<input type="file" accept="image/*" @change="pickLogo" /></label>
          <button v-if="formLogo" type="button" class="text" @click="clearLogo">移除图标</button>
        </div>
        <small v-if="fieldErrors.logo" class="bad">{{ fieldErrors.logo }}</small>
      </div>
      <div class="grid">
        <label>名称<input v-model="form.name" maxlength="64" placeholder="如 Netflix" />
          <small v-if="fieldErrors.name" class="bad">{{ fieldErrors.name }}</small></label>
        <label>类别
          <select v-model="form.category"><option v-for="(n, k) in categoryNames" :key="k" :value="k">{{ n }}</option></select></label>
        <label>价格
          <span class="price">
            <select v-model="form.currency" aria-label="币种"><option v-for="c in currencies" :key="c" :value="c">{{ c }}</option></select>
            <input v-model="form.price" inputmode="decimal" placeholder="0.00" />
          </span>
          <small v-if="fieldErrors.price_cents || fieldErrors.currency" class="bad">{{ fieldErrors.price_cents || fieldErrors.currency }}</small></label>
        <label>订阅周期
          <select v-model="form.cycle"><option v-for="(n, k) in cycleNames" :key="k" :value="k">{{ n }}</option></select></label>
        <label>免费试用
          <select v-model.number="form.trial_days"><option v-for="o in trialOptions" :key="o.v" :value="o.v">{{ o.t }}</option></select></label>
        <label>到期提醒
          <select v-model.number="form.remind_days"><option v-for="o in remindOptions" :key="o.v" :value="o.v">{{ o.t }}</option></select>
          <small v-if="fieldErrors.remind_days" class="bad">{{ fieldErrors.remind_days }}</small></label>
        <label>开始订阅<input v-model="form.start_date" type="date" />
          <small v-if="fieldErrors.start_date" class="bad">{{ fieldErrors.start_date }}</small></label>
        <label>结束订阅<input v-model="form.end_date" type="date" />
          <small v-if="fieldErrors.end_date" class="bad">{{ fieldErrors.end_date }}</small>
          <small v-else class="muted">留空表示仍在订阅</small></label>
        <label class="wide">描述<input v-model="form.note" maxlength="200" placeholder="可选，如 家庭版、和朋友合租" /></label>
      </div>
      <p v-if="fieldErrors._" class="small bad">{{ fieldErrors._ }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving || !form.name.trim()">{{ saving ? '保存中…' : '保存' }}</button>
        <button type="button" class="secondary" :disabled="saving" @click="editing ? close() : (step = 'pick')">{{ editing ? '取消' : '返回' }}</button>
        <template v-if="editing">
          <button v-if="!confirmDelete" type="button" class="text danger-text del" @click="confirmDelete = true">删除</button>
          <button v-else type="button" class="danger del" :disabled="saving" @click="remove">确认删除</button>
        </template>
      </div>
    </form>

    <template v-if="loaded && !step">
      <EmptyState v-if="!list.length" text="还没有订阅。记录影音、工具、云服务等订阅的价格与扣费日，扣费前可提醒。">
        <button type="button" class="btn" @click="openAdd"><Icon name="plus" />添加订阅</button>
      </EmptyState>

      <template v-else>
        <!-- 概览 -->
        <div class="panel strip">
          <div class="item">
            <span class="label">本月预计花费</span>
            <span class="big num">{{ mainText(thisMonth) }}</span>
            <span v-if="otherText(thisMonth)" class="sub muted num">{{ otherText(thisMonth) }}</span>
          </div>
          <div class="item">
            <span class="label">订阅中</span>
            <span class="big num">{{ active.length }}<small> 个</small></span>
            <span class="sub muted">共记录 {{ list.length }} 个</span>
          </div>
          <div class="item">
            <span class="label">截至目前已花费</span>
            <span class="big num">{{ mainText(spentSoFar) }}</span>
            <span v-if="otherText(spentSoFar)" class="sub muted num">{{ otherText(spentSoFar) }}</span>
          </div>
          <div v-if="next" class="item">
            <span class="label">下次扣费</span>
            <span class="big">{{ next.name }}</span>
            <span class="sub muted num">{{ next.next_charge }} · {{ fmtMoney(next.price_cents, next.currency) }}</span>
          </div>
        </div>

        <div class="segmented view-tabs" role="tablist" aria-label="视图">
          <button type="button" role="tab" :class="{ active: view === 'list' }" @click="setView('list')">列表</button>
          <button type="button" role="tab" :class="{ active: view === 'stats' }" @click="setView('stats')">统计</button>
        </div>

        <!-- 列表 -->
        <ul v-if="view === 'list'" class="subs">
          <li v-for="s in list" :key="s.id">
            <button type="button" class="panel sub-row" :class="{ ended: s.ended }" @click="openEdit(s)">
              <SubAvatar :brand="s.brand" :name="s.name" :src="s.logo_version ? subscriptionLogoURL(s) : ''" />
              <span class="sub-main">
                <b class="ellipsis">{{ s.name }}</b>
                <span class="small muted">{{ startText(s) }}<template v-if="s.note"> · {{ s.note }}</template></span>
              </span>
              <span class="sub-price">
                <b class="num">{{ fmtMoney(s.price_cents, s.currency) }}</b>
                <span class="small muted">{{ chargeText(s, today) }}</span>
              </span>
            </button>
          </li>
        </ul>

        <!-- 统计 -->
        <section v-else class="panel stats">
          <div class="segmented" role="group" aria-label="时间范围">
            <button type="button" :class="{ active: range === 'month' }" @click="range = 'month'">本月</button>
            <button type="button" :class="{ active: range === 'year' }" @click="range = 'year'">本年</button>
            <button type="button" :class="{ active: range === 'all' }" @click="range = 'all'">全部</button>
          </div>
          <div class="donut-row">
            <svg class="donut" viewBox="0 0 180 180" role="img" :aria-label="`${rangeLabel} ${fmtMoney(catTotal, cur)}`">
              <circle cx="90" cy="90" :r="R" fill="none" stroke="var(--track)" stroke-width="22" />
              <g transform="rotate(-90 90 90)">
                <circle v-for="(a, i) in arcs" :key="i" cx="90" cy="90" :r="R" fill="none" :stroke="a.color" stroke-width="22"
                  :stroke-dasharray="a.dash" :stroke-dashoffset="a.offset" />
              </g>
              <text x="90" y="88" text-anchor="middle" class="donut-total">{{ fmtMoney(catTotal, cur) }}</text>
              <text x="90" y="110" text-anchor="middle" class="donut-label">{{ rangeLabel }}</text>
            </svg>
            <ul class="legend">
              <li v-for="c in cats" :key="c.category">
                <i :style="{ background: categoryColors[c.category] }" />
                <span class="legend-main">
                  <span>{{ categoryNames[c.category] }} <b class="num">{{ Math.round(c.pct) }}%</b></span>
                  <span class="bar"><span :style="{ width: `${c.pct}%`, background: categoryColors[c.category] }" /></span>
                </span>
                <span class="legend-amt">
                  <b class="num">{{ fmtMoney(c.cents, cur) }}</b>
                  <span class="small muted">{{ c.count }} 个订阅</span>
                </span>
              </li>
              <li v-if="!cats.length" class="muted small">这段时间没有 {{ cur }} 扣费。</li>
            </ul>
          </div>

          <h3 class="bars-title">每月扣费（{{ cur }}）</h3>
          <div class="bars">
            <div v-for="b in bars" :key="b.key" class="bar-col" :title="fmtMoney(b.cents, cur)">
              <span v-if="b.current" class="bar-tip num">{{ fmtMoney(b.cents, cur) }}</span>
              <span class="bar-fill" :class="{ current: b.current, future: b.future }" :style="{ height: `${Math.max(3, (b.cents / barMax) * 100)}%` }" />
              <span class="bar-label small muted">{{ b.label }}</span>
            </div>
          </div>
        </section>
      </template>
    </template>
  </main>
</template>

<style scoped>

/* 概览：与仪表板总览同一版式 */
.strip { display: flex; flex-wrap: wrap; padding: var(--space-3) 0; margin-bottom: var(--space-3); }
.item { flex: 1 1 180px; display: flex; flex-direction: column; gap: 2px; min-width: 0; padding: 0 var(--space-4); }
.item + .item { border-left: 1px solid var(--border); }
.label { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); }
.big { font-size: var(--font-lg); line-height: var(--line-lg); font-weight: var(--weight-strong); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.big small { font-size: var(--font-xs); font-weight: var(--weight-regular); color: var(--text-muted); }
.sub { font-size: var(--font-xs); line-height: var(--line-xs); }
.view-tabs { margin-bottom: var(--space-3); }

/* 列表 */
.subs { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-2); }
.sub-row { width: 100%; display: flex; align-items: center; gap: var(--space-3); padding: var(--space-3) var(--space-4); text-align: left;
  color: var(--text); font: inherit; cursor: pointer; }
.sub-row:hover { border-color: var(--text-muted); filter: none; }
.sub-row.ended { opacity: .5; }
.sub-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.sub-price { display: flex; flex-direction: column; align-items: flex-end; gap: 2px; text-align: right; flex: none; }
.ellipsis { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

/* 统计 */
.stats { padding: var(--space-4) var(--space-5); }
.donut-row { display: flex; align-items: center; gap: var(--space-6); margin: var(--space-4) 0; flex-wrap: wrap; }
.donut { width: 200px; height: 200px; flex: none; }
.donut circle { transition: stroke-dasharray .3s; }
.donut-total { font-size: 18px; font-weight: 700; fill: var(--text); }
.donut-label { font-size: 11px; fill: var(--text-muted); }
.legend { flex: 1; min-width: 240px; list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-3); }
.legend li { display: flex; align-items: center; gap: var(--space-3); }
.legend i { width: 12px; height: 12px; border-radius: 3px; flex: none; }
.legend-main { flex: 1; display: flex; flex-direction: column; gap: 6px; min-width: 0; }
.bar { display: block; height: 4px; border-radius: 2px; background: var(--track); overflow: hidden; }
.bar span { display: block; height: 100%; border-radius: 2px; }
.legend-amt { display: flex; flex-direction: column; align-items: flex-end; flex: none; }
.bars-title { margin: var(--space-4) 0 var(--space-2); font-size: var(--font-sm); color: var(--text-muted); font-weight: var(--weight-regular); }
.bars { display: grid; grid-template-columns: repeat(9, minmax(0, 1fr)); gap: var(--space-2); height: 180px; padding-top: 28px; }
.bar-col { position: relative; display: flex; flex-direction: column; justify-content: flex-end; align-items: center; height: 100%; padding-bottom: 22px; }
.bar-fill { width: min(36px, 70%); border-radius: 8px 8px 4px 4px; background: color-mix(in srgb, var(--accent) 25%, transparent); }
.bar-fill.current { background: var(--accent); }
.bar-fill.future { background: color-mix(in srgb, var(--accent) 12%, transparent); }
.bar-label { position: absolute; bottom: 0; white-space: nowrap; }
.bar-tip { position: absolute; top: -26px; padding: 2px var(--space-2); border-radius: var(--radius-full); background: var(--accent);
  color: var(--on-accent); font-size: var(--font-xs); white-space: nowrap; }

/* 选择与填写 */
.editor { padding: var(--space-4) var(--space-5); margin-bottom: var(--space-4); }
.editor-head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); margin-bottom: var(--space-3); }
.editor h2 { margin: 0; font-size: var(--font-lg); }
.row-actions { display: flex; gap: var(--space-2); align-items: center; }
.cat-tabs { display: flex; gap: var(--space-1); overflow-x: auto; scrollbar-width: none; border-bottom: 1px solid var(--border); margin-bottom: var(--space-3); }
.cat-tabs button { background: none; border: 0; border-bottom: 2px solid transparent; border-radius: 0; color: var(--text-muted);
  padding: var(--space-2) var(--space-3); white-space: nowrap; }
.cat-tabs button.active { color: var(--text); border-bottom-color: var(--accent); font-weight: var(--weight-strong); }
.search { display: flex; align-items: center; gap: var(--space-2); border: 1px solid var(--border); border-radius: var(--radius-sm);
  background: var(--surface); padding: 0 var(--space-3); color: var(--text-muted); margin-bottom: var(--space-3); }
.search input { border: 0; padding-left: 0; flex: 1; background: transparent; outline: none; }
.pick-list { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: var(--space-2); }
.pick { width: 100%; display: flex; align-items: center; gap: var(--space-3); padding: var(--space-2) var(--space-3); background: var(--surface);
  border: 1px solid var(--border); border-radius: var(--radius-md); color: var(--text); text-align: left; font: inherit; }
.pick:hover { border-color: var(--accent); filter: none; }
.pick-name { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.empty { grid-column: 1 / -1; }
.form-head { display: flex; flex-direction: column; align-items: center; gap: var(--space-2); margin-bottom: var(--space-4); }
.form-head h2 { margin: 0; }
.logo-actions { display: flex; gap: var(--space-3); align-items: center; }
.upload { position: relative; cursor: pointer; color: var(--accent); }
.upload input { position: absolute; inset: 0; opacity: 0; cursor: pointer; width: 100%; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: var(--space-3); }
.grid label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.grid .wide { grid-column: 1 / -1; }
.price { display: flex; gap: var(--space-2); }
.price select { width: 84px; flex: none; }
.price input { flex: 1; min-width: 0; }
.actions { display: flex; gap: var(--space-2); align-items: center; margin-top: var(--space-4); }
.del { margin-left: auto; }
@media (max-width: 600px) {
  .item { flex-basis: 40%; padding: var(--space-1) var(--space-3); }
  .item + .item { border-left: 0; }
  .donut-row { justify-content: center; }
  .bars { gap: var(--space-1); }
  .bar-label { font-size: 10px; }
}
</style>
