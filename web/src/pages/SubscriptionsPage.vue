<script setup lang="ts">
// 订阅（设计 47，修订第 189 条）：记录影音、工具、云服务等个人订阅的价格、周期与起止日期。
// “列表”按下次扣费日排列，已结束的置灰；“统计”按本月 / 本年 / 全部显示各类别占比（环形图）与前后几个月的扣费（柱状）。
// 添加时先从常用服务目录中选择（按类别、可搜索），也可自定义；图标为内置的标志（SubAvatar），不加载外部图片。
// 金额换算为人民币统计（修订第 223 条，汇率见 GET /fx-rates）；外币价格下方显示约合人民币。“分析”把进行中的订阅折算为月费，
// 按类别、国家 / 地区、支付方式汇总并排行。每条订阅须选择国家 / 地区与支付方式。
// VPS（修订第 198 条）：每台 VPS 一条订阅，记录厂商、地址（可多个）、配置与机房；列表中显示地址与配置，
// 地址与已接入的节点相同时链接到该节点。
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ApiError, createSubscription, deleteSubscription, deleteSubscriptionLogo, errorText, getFxRates, listServers, listSubscriptions, renewSubscription, subscriptionLogoURL,
  updateSubscription, uploadSubscriptionLogo,
  type FxRates, type Subscription, type SubscriptionInput,
} from '../api'
import EmptyState from '../components/EmptyState.vue'
import Icon from '../components/Icon.vue'
import SubAvatar from '../components/SubAvatar.vue'
import { countryName, countryOptions, flagUrl } from '../countries'
import { fmtMoney, fmtPct } from '../format'
import { locale, t } from '../i18n'
import { simCountries } from '../simCatalog'
import {
  analyze, byCategory, cashflow, catalog, categoryColors, categoryNames, chargeText, cycleNames, currencies, defaultCountry, monthlyCents,
  monthStart, paymentNames, remindOptions, spendCny, toCny, trialOptions, ymd, type CatalogItem, type CnySpend, type Rates, type SubCategory,
} from '../subscriptions'

const route = useRoute()
const router = useRouter()
const view = computed(() => (route.query.tab === 'stats' || route.query.tab === 'analysis' ? route.query.tab : 'list'))
const setView = (v: string) => router.replace({ query: { tab: v === 'list' ? undefined : v } })

const list = ref<Subscription[]>([])
const loaded = ref(false)
const error = ref('')
// 已接入的节点：按公网地址查找，VPS 的地址与节点相同时在列表中链接到节点
const nodeByIP = ref(new Map<string, { id: number; name: string }>())
async function loadNodes() {
  try {
    const m = new Map<string, { id: number; name: string }>()
    for (const n of await listServers()) {
      for (const ip of [n.ipv4, n.ipv6, n.expected_ipv4, n.expected_ipv6]) if (ip) m.set(ip.toLowerCase(), { id: n.id, name: n.name })
    }
    nodeByIP.value = m
  } catch {
    /* 节点列表只用于链接，读取失败时不显示链接 */
  }
}
const nodeOf = (s: Subscription) => s.vps.ips.map((ip) => nodeByIP.value.get(ip.toLowerCase())).find(Boolean)

// 汇率（修订第 223 条）：读取失败时只显示原币种，统计中不计入外币
const fx = ref<FxRates | null>(null)
const rates = computed<Rates>(() => fx.value?.rates ?? { CNY: 1 })
async function loadFx() {
  try {
    fx.value = await getFxRates()
  } catch {
    fx.value = null
  }
}
async function load() {
  try {
    list.value = await listSubscriptions()
    error.value = ''
    if (list.value.some((s) => s.currency !== 'CNY')) void loadFx()
  } catch (e) {
    error.value = errorText(e, t('加载失败'))
  } finally {
    loaded.value = true
  }
}
onMounted(() => { load(); loadNodes() })

// ---- 概览 ----
const now = new Date()
const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
const active = computed(() => list.value.filter((s) => !s.ended))
const cur = 'CNY' // 统计一律按人民币（修订第 223 条）
// 人民币合计（spendCny：与统计页同一舍入，修订第 225 条）
const mainText = (m: CnySpend) => fmtMoney(m.cents, cur)
// 缺少汇率、没有计入人民币合计的部分按原币种列出
const otherText = (m: CnySpend) => [...m.missing].map(([c, v]) => `+ ${fmtMoney(v, c)}`).join(' ')
/** 外币价格约合人民币：“≈ ¥52.30”；人民币或没有汇率时为空 */
const cnyText = (cents: number, currency: string) => {
  if (currency === 'CNY') return ''
  const v = toCny(cents, currency, rates.value)
  return v == null ? '' : `≈ ${fmtMoney(v, 'CNY')}`
}
const rateDate = computed(() => (fx.value?.as_of ? ymd(new Date(fx.value.as_of * 1000)) : ''))
// 列表中用到的外币及其汇率：“1 USD = ¥7.12”
const usedRates = computed(() => [...new Set(list.value.map((s) => s.currency))].filter((c) => c !== 'CNY')
  .map((c) => ({ c, v: rates.value[c] })))
const thisMonth = computed(() => spendCny(list.value, rates.value, monthStart(today), monthStart(today, 1)))
const spentSoFar = computed(() => {
  const tomorrow = new Date(today.getFullYear(), today.getMonth(), today.getDate() + 1)
  return spendCny(list.value, rates.value, new Date(2000, 0, 1), tomorrow)
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
const cats = computed(() => byCategory(list.value, rates.value, ...rangeBounds.value))
const catTotal = computed(() => cats.value.reduce((a, c) => a + c.cents, 0))
const rangeLabel = computed(() => t({ month: '本月预计扣款', year: '本年预计扣款', all: '累计已扣款' }[range.value]))
const rangeMissing = computed(() => otherText(spendCny(list.value, rates.value, ...rangeBounds.value)))
const catCount = computed(() => cats.value.reduce((a, c) => a + c.count, 0))
const ended = computed(() => list.value.length - active.value.length)
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
    const v = spendCny(list.value, rates.value, from, monthStart(today, i + 1)).cents
    out.push({ key: i, label: from.getMonth() === 0 ? String(from.getFullYear()) : from.toLocaleString(locale.value === 'en' ? 'en' : 'zh-CN', { month: 'short' }), cents: v, current: i === 0, future: i > 0 })
  }
  return out
})
const barMax = computed(() => Math.max(1, ...bars.value.map((b) => b.cents)))

// ---- 分析（修订第 223 条）：进行中的订阅折算为月费（人民币） ----
const ana = computed(() => analyze(list.value, rates.value))
// 未来 12 个月预计扣款（修订第 240 条）：现金流，按扣费日排期
const cash = computed(() => cashflow(list.value, rates.value, today))
const cashMax = computed(() => Math.max(1, ...cash.value.months.map((m) => m.cents)))
const monthLabel = (d: Date) => (d.getMonth() === 0 ? String(d.getFullYear()) : d.toLocaleString(locale.value === 'en' ? 'en' : 'zh-CN', { month: 'short' }))

// ---- 列表排列（修订第 237 条）：不分类（按下次扣费日）/ 按类别（类别按目录顺序，组内顺序不变，组头为数量与月均折算） ----
const grouped = ref(readGrouped())
function readGrouped() {
  try {
    return localStorage.getItem('vpsmon.subGrouped') === '1'
  } catch {
    return false
  }
}
function setGrouped(v: boolean) {
  grouped.value = v
  try {
    localStorage.setItem('vpsmon.subGrouped', v ? '1' : '0')
  } catch {
    /* 隐私模式等不可用时只在本页生效 */
  }
}
type ListRow = { key: string; s?: Subscription; cat?: SubCategory; count?: number; monthly?: number }
const listRows = computed<ListRow[]>(() => {
  if (!grouped.value) return list.value.map((s) => ({ key: `s${s.id}`, s }))
  const monthly = new Map(ana.value.byCategory.map((x) => [x.key, x.cents]))
  const out: ListRow[] = []
  for (const cat of Object.keys(categoryNames) as SubCategory[]) {
    const xs = list.value.filter((s) => s.category === cat)
    if (!xs.length) continue
    out.push({ key: `c${cat}`, cat, count: xs.length, monthly: monthly.get(cat) ?? 0 })
    for (const s of xs) out.push({ key: `s${s.id}`, s })
  }
  return out
})
const anaGroup = ref<'category' | 'country' | 'payment'>('category')
const anaRows = computed(() => {
  const a = ana.value
  if (anaGroup.value === 'country') return a.byCountry.map((x) => ({ ...x, name: x.key ? countryName(x.key) : t('未设置'), flag: x.key }))
  if (anaGroup.value === 'payment') return a.byPayment.map((x) => ({ ...x, name: x.key ? t(paymentNames[x.key] ?? x.key) : t('未设置'), flag: '' }))
  return a.byCategory.map((x) => ({ ...x, name: t(categoryNames[x.key as SubCategory] ?? x.key), flag: '' }))
})
const anaColor = (i: number) => ['var(--accent)', 'var(--ok)', 'var(--series-2)', 'var(--series-3)', 'var(--warn)', 'var(--bad)'][i % 6]
const countryOpts = computed(() => countryOptions())
const cycleShort: Record<string, string> = { weekly: '周', monthly: '月', quarterly: '季', semiannually: '半年', annually: '年', once: '次' }

// ---- 添加 / 编辑 ----
type Step = 'pick' | 'form'
const step = ref<Step | null>(null)
const editing = ref<Subscription | null>(null)
const pickCat = ref<'all' | 'hot' | SubCategory>('all')
const pickCountry = ref('') // SIM 电话卡按国家 / 地区筛选（空为全部）
const query = ref('')
const picks = computed(() => {
  const q = query.value.trim().toLowerCase()
  return catalog.filter((c) => (pickCat.value === 'all' || (pickCat.value === 'hot' ? c.hot : c.category === pickCat.value))
    && (pickCat.value !== 'sim' || !pickCountry.value || c.country === pickCountry.value)
    && (!q || c.name.toLowerCase().includes(q) || c.key.includes(q)))
})
const countryOf = (key?: string) => simCountries.find((c) => c.key === key)
const pickTabs = computed(() => [{ k: 'all', label: t('全部') }, { k: 'hot', label: t('热门') },
  ...(Object.keys(categoryNames) as SubCategory[]).filter((c) => c !== 'other' && catalog.some((x) => x.category === c)).map((c) => ({ k: c, label: t(categoryNames[c]) }))])

type FormPhone = { cc: string; number: string }
// VPS 配置在表单中为字符串（空表示没有填写），保存时转为数字
type FormVPS = { ips: string[]; cpu: string; memory_gb: string; disk_gb: string; bandwidth_mbps: string; traffic_gb: string; location: string }
const blankVPS = (): FormVPS => ({ ips: [''], cpu: '', memory_gb: '', disk_gb: '', bandwidth_mbps: '', traffic_gb: '', location: '' })
// 支付方式没有默认值：表单中可为空，保存时由面板要求选择（修订第 223 条）
const blank = (): Omit<SubscriptionInput, 'vps' | 'payment'> & { price: string; phones: FormPhone[]; vps: FormVPS; payment: SubscriptionInput['payment'] | '' } => ({
  name: '', brand: '', category: 'other', price_cents: 0, price: '', currency: 'CNY', cycle: 'monthly', trial_days: 0,
  start_date: ymd(today), end_date: '', remind_days: 0, note: '', phones: [], vps: blankVPS(), country: '', payment: '',
})
const vpsNum = (v: string) => (v.trim() === '' ? 0 : Number(v))
const vpsStr = (n: number) => (n ? String(n) : '')
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
    fieldErrors.value = { ...fieldErrors.value, logo: t('图片不能超过 10 MB') }
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
    fieldErrors.value = { ...fieldErrors.value, logo: t('无法读取这张图片') }
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
  if (item) Object.assign(form, { name: t(item.name), brand: item.key, category: item.category, currency: item.currency ?? 'CNY' })
  // 国家 / 地区按 SIM 卡的国家或币种预填（可改）；支付方式须自己选择
  form.country = item?.country ? item.country.toUpperCase() : defaultCountry(form.currency)
  if (form.category === 'sim') form.phones = [{ cc: item?.dial ?? '+86', number: '' }]
  fieldErrors.value = {}
  resetLogo()
  step.value = 'form'
}
function openEdit(s: Subscription) {
  editing.value = s
  renewMsg.value = ''
  Object.assign(form, {
    ...s, price: (s.price_cents / 100).toFixed(2), phones: s.phones.map((p) => ({ ...p })),
    vps: {
      ips: s.vps.ips.length ? [...s.vps.ips] : [''], cpu: vpsStr(s.vps.cpu), memory_gb: vpsStr(s.vps.memory_gb), disk_gb: vpsStr(s.vps.disk_gb),
      bandwidth_mbps: vpsStr(s.vps.bandwidth_mbps), traffic_gb: vpsStr(s.vps.traffic_gb), location: s.vps.location,
    },
  })
  if (s.category === 'sim' && !form.phones.length) form.phones = [{ cc: countryOf(catalog.find((c) => c.key === s.brand)?.country)?.dial ?? '+86', number: '' }]
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
    fieldErrors.value = { price_cents: t('请填写价格') }
    return
  }
  const body: SubscriptionInput = {
    name: form.name, brand: form.brand, category: form.category, price_cents: Math.round(price * 100), currency: form.currency,
    cycle: form.cycle, trial_days: form.trial_days, start_date: form.start_date, end_date: form.end_date ?? '', remind_days: form.remind_days, note: form.note,
    country: form.country, payment: form.payment as SubscriptionInput['payment'],
    phones: form.category === 'sim' ? form.phones.filter((p) => p.number.trim()) : [],
    vps: form.category === 'vps'
      ? {
          ips: form.vps.ips.map((ip) => ip.trim()).filter(Boolean), cpu: vpsNum(form.vps.cpu), memory_gb: vpsNum(form.vps.memory_gb),
          disk_gb: vpsNum(form.vps.disk_gb), bandwidth_mbps: vpsNum(form.vps.bandwidth_mbps), traffic_gb: vpsNum(form.vps.traffic_gb),
          location: form.vps.location,
        }
      : undefined,
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
    else fieldErrors.value = { _: errorText(e, t('保存失败')) }
  } finally {
    saving.value = false
  }
}
// 已续费（修订第 224 条）：有结束日期的订阅按周期顺延结束日期（只改这一项，表单中其他未保存的修改不受影响）
const renewMsg = ref('')
async function renew() {
  if (!editing.value) return
  saving.value = true
  try {
    const x = await renewSubscription(editing.value.id)
    editing.value = x
    form.end_date = x.end_date
    renewMsg.value = t('结束日期已顺延到 {date}', { date: x.end_date })
    await load()
  } catch (e) {
    fieldErrors.value = { _: errorText(e, t('续费失败')) }
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
    fieldErrors.value = { _: errorText(e, t('删除失败')) }
  } finally {
    saving.value = false
  }
}
// 列表主金额（修订第 253 条）：人民币（外币按汇率换算，前面加 ≈）；没有汇率时显示原币
const mainPrice = (s: Subscription) => {
  if (s.currency === 'CNY') return fmtMoney(s.price_cents, 'CNY')
  const v = toCny(s.price_cents, s.currency, rates.value)
  return v == null ? fmtMoney(s.price_cents, s.currency) : `≈ ${fmtMoney(v, 'CNY')}`
}
// 7 天内扣费的短标签（文字，不只靠颜色）
const soonDays = (s: Subscription) => {
  if (s.ended || !s.next_charge) return null
  const d = Math.round((new Date(`${s.next_charge}T00:00:00`).getTime() - today.getTime()) / 86400000)
  return d >= 0 && d <= 7 ? d : null
}
// VPS 配置摘要：2 核 · 1 GB · 20 GB · 1 TB/月 · 500 Mbps（没有填写的省略）
const ipError = (i: number) => fieldErrors.value[`vps.ips[${i}]`]
// 号码显示：区号 + 按当地习惯分组的号码（+1 650 555 0123、+86 138 0013 8000），其他按每 4 位分组
function fmtPhone(p: { cc: string; number: string }) {
  const n = p.number
  if (p.cc === '+1' && n.length === 10) return `+1 ${n.slice(0, 3)} ${n.slice(3, 6)} ${n.slice(6)}`
  if (p.cc === '+86' && n.length === 11) return `+86 ${n.slice(0, 3)} ${n.slice(3, 7)} ${n.slice(7)}`
  return `${p.cc} ${n.replace(/(\d)(?=(\d{4})+$)/g, '$1 ')}`
}
// 号码编辑：增加一行时沿用上一行的区号
function addPhone() {
  form.phones.push({ cc: form.phones.at(-1)?.cc ?? '+86', number: '' })
}
const phoneError = (i: number) => fieldErrors.value[`phones[${i}].cc`] || fieldErrors.value[`phones[${i}].number`]
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>{{ t('订阅') }}</h1>
      <button v-if="!step" type="button" class="btn" @click="openAdd"><Icon name="plus" />{{ t('添加订阅') }}</button>
    </div>
    <p v-if="error" class="banner">{{ error }}</p>

    <!-- 选择服务 -->
    <section v-if="step === 'pick'" class="panel editor">
      <div class="editor-head">
        <h2>{{ t('选择服务') }}</h2>
        <div class="row-actions">
          <button type="button" class="secondary" @click="choose(null)"><Icon name="edit" />{{ t('自定义') }}</button>
          <button type="button" class="text" @click="close">{{ t('取消') }}</button>
        </div>
      </div>
      <nav class="cat-tabs" :aria-label="t('类别')">
        <button v-for="tb in pickTabs" :key="tb.k" type="button" :class="{ active: pickCat === tb.k }" @click="pickCat = tb.k as typeof pickCat">{{ tb.label }}</button>
      </nav>
      <div v-if="pickCat === 'sim'" class="countries" role="group" :aria-label="t('国家 / 地区')">
        <button type="button" class="chip" :class="{ active: !pickCountry }" @click="pickCountry = ''">{{ t('全部') }}</button>
        <button v-for="c in simCountries" :key="c.key" type="button" class="chip" :class="{ active: pickCountry === c.key }" @click="pickCountry = c.key">
          {{ c.flag }} {{ t(c.name) }}<span class="muted num">{{ c.dial }}</span>
        </button>
      </div>
      <label class="search"><Icon name="search" /><input v-model="query" type="search" :placeholder="t('输入名称搜索')" /></label>
      <ul class="pick-list">
        <li v-for="c in picks" :key="c.key">
          <button type="button" class="pick" @click="choose(c)">
            <SubAvatar :brand="c.key" :name="c.name" />
            <span class="pick-name">{{ t(c.name) }}<small v-if="c.country" class="muted"> {{ countryOf(c.country)?.flag }} {{ c.dial }}</small></span>
            <Icon name="plus" class="muted" />
          </button>
        </li>
        <li v-if="!picks.length" class="muted small empty">{{ t('没有找到，点击右上角“自定义”添加。') }}</li>
      </ul>
    </section>

    <!-- 填写 / 编辑 -->
    <form v-else-if="step === 'form'" class="panel editor" novalidate @submit.prevent="save">
      <div class="form-head">
        <SubAvatar :brand="form.brand" :name="form.name || '?'" :size="64" :src="formLogo" />
        <h2>{{ form.name || t('自定义订阅') }}</h2>
        <!-- 上传图标（修订第 189 条）：浏览器中裁剪为正方形并缩放为 128×128 PNG 后上传 -->
        <div class="logo-actions small">
          <label class="text upload">{{ t('上传图标') }}<input type="file" accept="image/*" @change="pickLogo" /></label>
          <button v-if="formLogo" type="button" class="text" @click="clearLogo">{{ t('移除图标') }}</button>
        </div>
        <small v-if="fieldErrors.logo" class="bad">{{ fieldErrors.logo }}</small>
      </div>
      <div class="grid">
        <label>{{ t('名称') }}<input v-model="form.name" maxlength="64" :placeholder="t('如 Netflix')" />
          <small v-if="fieldErrors.name" class="bad">{{ fieldErrors.name }}</small></label>
        <label>{{ t('类别') }}
          <select v-model="form.category"><option v-for="(n, k) in categoryNames" :key="k" :value="k">{{ t(n) }}</option></select></label>
        <label>{{ t('价格') }}
          <span class="price">
            <select v-model="form.currency" :aria-label="t('币种')"><option v-for="c in currencies" :key="c" :value="c">{{ c }}</option></select>
            <input v-model="form.price" inputmode="decimal" placeholder="0.00" />
          </span>
          <small v-if="fieldErrors.price_cents || fieldErrors.currency" class="bad">{{ fieldErrors.price_cents || fieldErrors.currency }}</small></label>
        <!-- 国家 / 地区与支付方式（修订第 223 条）：必选 -->
        <label>{{ t('国家 / 地区') }}
          <select v-model="form.country">
            <option value="" disabled>{{ t('请选择') }}</option>
            <optgroup :label="t('常用')"><option v-for="c in countryOpts.common" :key="c.code" :value="c.code">{{ c.name }}</option></optgroup>
            <optgroup :label="t('全部')"><option v-for="c in countryOpts.all" :key="c.code" :value="c.code">{{ c.name }}</option></optgroup>
          </select>
          <small v-if="fieldErrors.country" class="bad">{{ fieldErrors.country }}</small></label>
        <label>{{ t('支付方式') }}
          <select v-model="form.payment">
            <option value="" disabled>{{ t('请选择') }}</option>
            <option v-for="(n, k) in paymentNames" :key="k" :value="k">{{ t(n) }}</option>
          </select>
          <small v-if="fieldErrors.payment" class="bad">{{ fieldErrors.payment }}</small></label>
        <label>{{ t('订阅周期') }}
          <select v-model="form.cycle"><option v-for="(n, k) in cycleNames" :key="k" :value="k">{{ t(n) }}</option></select></label>
        <label>{{ t('免费试用') }}
          <select v-model.number="form.trial_days"><option v-for="o in trialOptions" :key="o.v" :value="o.v">{{ t(o.t) }}</option></select></label>
        <label>{{ t('到期提醒') }}
          <select v-model.number="form.remind_days"><option v-for="o in remindOptions" :key="o.v" :value="o.v">{{ t(o.t) }}</option></select>
          <small v-if="fieldErrors.remind_days" class="bad">{{ fieldErrors.remind_days }}</small></label>
        <label>{{ form.category === 'vps' ? t('购买日期') : t('开始订阅') }}<input v-model="form.start_date" type="date" />
          <small v-if="fieldErrors.start_date" class="bad">{{ fieldErrors.start_date }}</small></label>
        <label>{{ t('结束订阅') }}<input v-model="form.end_date" type="date" />
          <small v-if="fieldErrors.end_date" class="bad">{{ fieldErrors.end_date }}</small>
          <small v-else class="muted">{{ t('留空表示仍在订阅') }}</small></label>
        <!-- SIM 电话卡的号码（修订第 189 条）：可增减，最多 10 个；空行不保存 -->
        <div v-if="form.category === 'sim'" class="wide phones">
          <span class="phones-label">{{ t('手机号') }}</span>
          <div v-for="(p, i) in form.phones" :key="i" class="phone-row">
            <select v-model="p.cc" :aria-label="t('区号')" class="cc">
              <option v-if="!simCountries.some((c) => c.dial === p.cc)" :value="p.cc">{{ p.cc }}</option>
              <option v-for="c in simCountries" :key="c.key" :value="c.dial">{{ c.flag }} {{ c.dial }}</option>
            </select>
            <input v-model="p.number" inputmode="tel" autocomplete="off" :placeholder="t('号码')" :aria-label="t('号码')" />
            <button type="button" class="text" :aria-label="t('删除第 {n} 个号码', { n: i + 1 })" @click="form.phones.splice(i, 1)">{{ t('删除') }}</button>
            <small v-if="phoneError(i)" class="bad">{{ phoneError(i) }}</small>
          </div>
          <button v-if="form.phones.length < 10" type="button" class="text add-phone" @click="addPhone"><Icon name="plus" :size="14" />{{ t('添加号码') }}</button>
          <small v-if="fieldErrors.phones" class="bad">{{ fieldErrors.phones }}</small>
        </div>
        <!-- VPS（修订第 198 条）：地址可增减（最多 16 个，空行不保存），配置都可不填 -->
        <template v-if="form.category === 'vps'">
          <div class="wide phones">
            <span class="phones-label">{{ t('IP 地址') }}</span>
            <div v-for="(_, i) in form.vps.ips" :key="i" class="ip-row">
              <input v-model="form.vps.ips[i]" class="mono" inputmode="url" autocomplete="off" spellcheck="false" :placeholder="t('IPv4 或 IPv6')"
                :aria-label="t('第 {n} 个地址', { n: i + 1 })" />
              <button type="button" class="text" :aria-label="t('删除第 {n} 个地址', { n: i + 1 })" @click="form.vps.ips.splice(i, 1)">{{ t('删除') }}</button>
              <small v-if="ipError(i)" class="bad">{{ ipError(i) }}</small>
            </div>
            <button v-if="form.vps.ips.length < 16" type="button" class="text add-phone" @click="form.vps.ips.push('')">
              <Icon name="plus" :size="14" />{{ t('添加地址') }}</button>
            <small v-if="fieldErrors['vps.ips']" class="bad">{{ fieldErrors['vps.ips'] }}</small>
          </div>
          <label>{{ t('CPU（核）') }}<input v-model="form.vps.cpu" inputmode="numeric" :placeholder="t('如 2')" />
            <small v-if="fieldErrors['vps.cpu']" class="bad">{{ fieldErrors['vps.cpu'] }}</small></label>
          <label>{{ t('内存（GB）') }}<input v-model="form.vps.memory_gb" inputmode="decimal" :placeholder="t('如 1 或 0.5')" />
            <small v-if="fieldErrors['vps.memory_gb']" class="bad">{{ fieldErrors['vps.memory_gb'] }}</small></label>
          <label>{{ t('硬盘（GB）') }}<input v-model="form.vps.disk_gb" inputmode="numeric" :placeholder="t('如 20')" />
            <small v-if="fieldErrors['vps.disk_gb']" class="bad">{{ fieldErrors['vps.disk_gb'] }}</small></label>
          <label>{{ t('每月流量（GB）') }}<input v-model="form.vps.traffic_gb" inputmode="numeric" :placeholder="t('如 1024')" />
            <small v-if="fieldErrors['vps.traffic_gb']" class="bad">{{ fieldErrors['vps.traffic_gb'] }}</small></label>
          <label>{{ t('带宽（Mbps）') }}<input v-model="form.vps.bandwidth_mbps" inputmode="numeric" :placeholder="t('如 500')" />
            <small v-if="fieldErrors['vps.bandwidth_mbps']" class="bad">{{ fieldErrors['vps.bandwidth_mbps'] }}</small></label>
          <label>{{ t('机房') }}<input v-model="form.vps.location" maxlength="40" :placeholder="t('如 东京、Los Angeles')" />
            <small v-if="fieldErrors['vps.location']" class="bad">{{ fieldErrors['vps.location'] }}</small></label>
        </template>
        <label class="wide">{{ t('描述') }}<input v-model="form.note" maxlength="200" :placeholder="t('可选，如 家庭版、和朋友合租')" /></label>
      </div>
      <p v-if="fieldErrors._" class="small bad">{{ fieldErrors._ }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving || !form.name.trim()">{{ saving ? t('保存中…') : t('保存') }}</button>
        <button type="button" class="secondary" :disabled="saving" @click="editing ? close() : (step = 'pick')">{{ editing ? t('取消') : t('返回') }}</button>
        <button v-if="editing && editing.end_date && editing.cycle !== 'once'" type="button" class="secondary" :disabled="saving"
          :title="t('结束日期按订阅周期顺延一个周期')" @click="renew">{{ t('已续费') }}</button>
        <span v-if="renewMsg" class="small muted">{{ renewMsg }}</span>
        <template v-if="editing">
          <button v-if="!confirmDelete" type="button" class="text danger-text del" @click="confirmDelete = true">{{ t('删除') }}</button>
          <button v-else type="button" class="danger del" :disabled="saving" @click="remove">{{ t('确认删除') }}</button>
        </template>
      </div>
    </form>

    <template v-if="loaded && !step">
      <EmptyState v-if="!list.length" :text="t('还没有订阅。记录影音、工具、云服务等订阅的价格与扣费日，扣费前可提醒。')">
        <button type="button" class="btn" @click="openAdd"><Icon name="plus" />{{ t('添加订阅') }}</button>
      </EmptyState>

      <template v-else>
        <!-- 概览 -->
        <div class="panel strip">
          <div class="item">
            <span class="label">{{ t('本月预计扣款') }}</span>
            <span class="big num">{{ mainText(thisMonth) }}</span>
            <span class="sub muted num">{{ t('{n} 个订阅本月扣费', { n: thisMonth.items.length }) }} {{ otherText(thisMonth) }}</span>
          </div>
          <div class="item">
            <span class="label">{{ t('订阅中') }}</span>
            <span class="big num">{{ active.length }}<small> {{ t('个') }}</small></span>
            <span class="sub muted">{{ ended ? t('共记录 {n} 个，已结束 {e} 个', { n: list.length, e: ended }) : t('共记录 {n} 个', { n: list.length }) }}</span>
          </div>
          <div class="item">
            <span class="label">{{ t('截至目前已扣款') }}</span>
            <span class="big num">{{ mainText(spentSoFar) }}</span>
            <span v-if="otherText(spentSoFar)" class="sub muted num">{{ otherText(spentSoFar) }}</span>
          </div>
          <div v-if="next" class="item">
            <span class="label">{{ t('下次扣费') }}</span>
            <span class="big">{{ next.name }}</span>
            <span class="sub muted num">{{ next.next_charge }} · {{ fmtMoney(next.price_cents, next.currency) }}</span>
          </div>
        </div>

        <div class="view-bar">
          <div class="segmented view-tabs" role="tablist" :aria-label="t('视图')">
            <button type="button" role="tab" :class="{ active: view === 'list' }" @click="setView('list')">{{ t('列表') }}</button>
            <button type="button" role="tab" :class="{ active: view === 'stats' }" @click="setView('stats')">{{ t('统计') }}</button>
            <button type="button" role="tab" :class="{ active: view === 'analysis' }" @click="setView('analysis')">{{ t('分析') }}</button>
          </div>
          <!-- 列表排列（修订第 237 条）：不分类 / 按类别，记在本机 -->
          <div v-if="view === 'list'" class="segmented" role="group" :aria-label="t('排列方式')">
            <button type="button" :class="{ active: !grouped }" :aria-pressed="!grouped" @click="setGrouped(false)">{{ t('不分类') }}</button>
            <button type="button" :class="{ active: grouped }" :aria-pressed="grouped" @click="setGrouped(true)">{{ t('按类别') }}</button>
          </div>
        </div>

        <!-- 列表 -->
        <ul v-if="view === 'list'" class="subs">
          <template v-for="r in listRows" :key="r.key">
          <li v-if="r.cat" class="cat-head">
            <i :style="{ background: categoryColors[r.cat] }" />
            <b>{{ t(categoryNames[r.cat]) }}</b>
            <span class="muted small">{{ t('{n} 个', { n: r.count }) }}</span>
            <span v-if="r.monthly" class="muted small num cat-sum">{{ t('月均 {v}', { v: fmtMoney(r.monthly, 'CNY') }) }}</span>
          </li>
          <template v-else>
          <li v-for="s in [r.s!]" :key="s.id">
            <!-- 三层信息（修订第 253 条，UI-012）：名称 / 地区 · 支付方式 / 扣费说明；右侧人民币为主金额，原币在下一行。
                 开始日期、VPS 规格与备注放在编辑页（悬停可看备注） -->
            <button type="button" class="panel sub-row" :class="{ ended: s.ended }" :title="s.note || undefined" @click="openEdit(s)">
              <SubAvatar :brand="s.brand" :name="s.name" :src="s.logo_version ? subscriptionLogoURL(s) : ''" />
              <span class="sub-main">
                <b class="ellipsis">{{ s.name }}<small v-if="s.phones.length" class="num id-inline">{{ fmtPhone(s.phones[0]) }}</small><small
                  v-else-if="s.category === 'vps' && s.vps.ips.length" class="mono id-inline">{{ s.vps.ips[0] }}</small></b>
                <span v-if="s.country || s.payment" class="small muted meta-line">
                  <img v-if="s.country && flagUrl(s.country)" :src="flagUrl(s.country)" alt="" class="flag" />
                  {{ [s.country ? countryName(s.country) : '', s.payment ? t(paymentNames[s.payment] ?? s.payment) : ''].filter(Boolean).join(' · ') }}
                </span>
                <span class="small muted">{{ chargeText(s, today) }}</span>
              </span>
              <RouterLink v-if="s.category === 'vps' && nodeOf(s)" :to="`/servers/${nodeOf(s)!.id}`" class="node-link small" :title="t('已接入监控：{name}', { name: nodeOf(s)!.name })"
                @click.stop>{{ t('监控中') }}</RouterLink>
              <span class="sub-price">
                <b class="num">{{ mainPrice(s) }}<small class="muted cyc">/{{ t(cycleShort[s.cycle] ?? s.cycle) }}</small></b>
                <span v-if="s.currency !== 'CNY'" class="small muted num">{{ fmtMoney(s.price_cents, s.currency) }}</span>
                <span v-if="soonDays(s) != null" class="soon small">{{ soonDays(s) === 0 ? t('今天扣费') : t('{n} 天后扣费', { n: soonDays(s)! }) }}</span>
              </span>
            </button>
          </li>
          </template>
          </template>
        </ul>

        <!-- 分析（修订第 223 条）：进行中的订阅折算为月费（人民币） -->
        <section v-else-if="view === 'analysis'" class="panel stats">
          <div class="ana-head">
            <div>
              <span class="label">{{ t('月均折算') }}</span>
              <span class="big num">{{ fmtMoney(ana.monthly, 'CNY') }}</span>
              <span class="sub muted num">{{ t('每年约 {v} · {n} 个按周期扣费的订阅 · 平均每个 {avg}/月', {
                v: fmtMoney(ana.monthly * 12, 'CNY'), n: ana.count, avg: fmtMoney(ana.count ? Math.round(ana.monthly / ana.count) : 0, 'CNY') }) }}</span>
            </div>
            <div class="segmented" role="group" :aria-label="t('分组')">
              <button type="button" :class="{ active: anaGroup === 'category' }" @click="anaGroup = 'category'">{{ t('类别') }}</button>
              <button type="button" :class="{ active: anaGroup === 'country' }" @click="anaGroup = 'country'">{{ t('国家 / 地区') }}</button>
              <button type="button" :class="{ active: anaGroup === 'payment' }" @click="anaGroup = 'payment'">{{ t('支付方式') }}</button>
            </div>
          </div>
          <ul class="legend ana-groups">
            <li v-for="(r, i) in anaRows" :key="r.key">
              <i :style="{ background: anaGroup === 'category' ? categoryColors[r.key as SubCategory] : anaColor(i) }" />
              <span class="legend-main">
                <span><img v-if="r.flag && flagUrl(r.flag)" :src="flagUrl(r.flag)" alt="" class="flag" /> {{ r.name }} <b class="num">{{ Math.round(r.pct) }}%</b></span>
                <span class="bar"><span :style="{ width: `${r.pct}%`, background: anaGroup === 'category' ? categoryColors[r.key as SubCategory] : anaColor(i) }" /></span>
              </span>
              <span class="legend-amt">
                <b class="num">{{ t('{v}/月', { v: fmtMoney(r.cents, 'CNY') }) }}</b>
                <span class="small muted">{{ t('{n} 个订阅', { n: r.count }) }}</span>
              </span>
            </li>
            <li v-if="!anaRows.length" class="muted small">{{ t('没有进行中的按周期扣费的订阅。') }}</li>
          </ul>
          <h3 class="bars-title">{{ t('月均成本排行（不是按月付费的折算为每月）') }}</h3>
          <ol class="ana-top">
            <li v-for="(x, i) in ana.top.slice(0, 10)" :key="x.s.id">
              <span class="rank num">{{ i + 1 }}</span>
              <SubAvatar :brand="x.s.brand" :name="x.s.name" :size="28" :src="x.s.logo_version ? subscriptionLogoURL(x.s) : ''" />
              <span class="sub-main">
                <b class="ellipsis">{{ x.s.name }}</b>
                <span class="small muted num">{{ fmtMoney(x.s.price_cents, x.s.currency) }}/{{ t(cycleShort[x.s.cycle] ?? x.s.cycle) }}
                  <template v-if="x.s.cycle !== 'monthly'"> · {{ t('折合 {v}/月', { v: fmtMoney(monthlyCents(x.s), x.s.currency) }) }}</template></span>
              </span>
              <b class="num">{{ fmtMoney(x.cents, 'CNY') }}</b>
            </li>
          </ol>
          <!-- 未来 12 个月预计扣款（修订第 240 条） -->
          <h3 class="bars-title">{{ t('未来 12 个月预计扣款') }}</h3>
          <p class="small muted cash-sum num">
            {{ t('合计 {v}（月均折算 × 12 为 {m}；差异来自年付、季付落在哪个月，以及中途开始或结束的订阅）', { v: fmtMoney(cash.total, 'CNY'), m: fmtMoney(ana.monthly * 12, 'CNY') }) }}
            <template v-if="cash.missing.length">{{ t('缺少 {c} 的汇率，这些订阅没有计入。', { c: cash.missing.join('、') }) }}</template>
          </p>
          <div class="bars">
            <div v-for="(m, i) in cash.months" :key="i" class="bar-col" :title="`${monthLabel(m.month)} ${fmtMoney(m.cents, 'CNY')} · ${t('{n} 个订阅', { n: m.count })}`">
              <span v-if="m.cents === cashMax" class="bar-tip num">{{ fmtMoney(m.cents, 'CNY') }}</span>
              <span class="bar-fill" :class="{ current: i === 0, future: i > 0 }" :style="{ height: `${Math.max(3, (m.cents / cashMax) * 100)}%` }" />
              <span class="bar-label small muted">{{ monthLabel(m.month) }}</span>
            </div>
          </div>
          <p class="small muted rate-note">
            {{ t('月均折算是按周期摊到每月的成本，不等于本月扣款：年付、季付只在扣费的月份计入本月扣款。') }}
            {{ t('每周按 52 ÷ 12、每季 ÷ 3、每半年 ÷ 6、每年 ÷ 12 折算为每月；已结束的不计入。') }}
            <template v-if="ana.once">{{ t('一次性付费的 {n} 个不计入。', { n: ana.once }) }}</template>
            <template v-if="ana.missing.length">{{ t('缺少 {c} 的汇率，这些订阅没有计入。', { c: ana.missing.join('、') }) }}</template>
          </p>
        </section>

        <!-- 统计 -->
        <section v-else class="panel stats">
          <div class="segmented" role="group" :aria-label="t('时间范围')">
            <button type="button" :class="{ active: range === 'month' }" @click="range = 'month'">{{ t('本月') }}</button>
            <button type="button" :class="{ active: range === 'year' }" @click="range = 'year'">{{ t('本年') }}</button>
            <button type="button" :class="{ active: range === 'all' }" @click="range = 'all'">{{ t('全部') }}</button>
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
                <!-- 修订第 233 条：环形图已经表示占比，这里不再重复进度条，只列名称、占比、金额与订阅数 -->
                <span class="legend-main">
                  <span>{{ t(categoryNames[c.category]) }} <b class="num">{{ fmtPct(c.pct) }}</b></span>
                </span>
                <span class="legend-amt">
                  <b class="num">{{ fmtMoney(c.cents, cur) }}</b>
                  <span class="small muted">{{ t('{n} 个订阅', { n: c.count }) }}</span>
                </span>
              </li>
              <li v-if="!cats.length" class="muted small">{{ t('这段时间没有扣费。') }}</li>
              <li v-else class="muted small">{{ t('这段时间有扣费的 {n} 个订阅（各类别相加与合计一致）', { n: catCount }) }}<template v-if="rangeMissing"> · {{ t('缺少汇率未计入：{v}', { v: rangeMissing }) }}</template></li>
            </ul>
          </div>

          <h3 class="bars-title">{{ t('每月扣费（人民币）') }}</h3>
          <div class="bars">
            <div v-for="b in bars" :key="b.key" class="bar-col" :title="fmtMoney(b.cents, cur)">
              <span v-if="b.current" class="bar-tip num">{{ fmtMoney(b.cents, cur) }}</span>
              <span class="bar-fill" :class="{ current: b.current, future: b.future }" :style="{ height: `${Math.max(3, (b.cents / barMax) * 100)}%` }" />
              <span class="bar-label small muted">{{ b.label }}</span>
            </div>
          </div>
        </section>
        <!-- 汇率说明（修订第 223 条） -->
        <p v-if="usedRates.length && view !== 'list'" class="small muted rate-note">
          {{ t('外币按汇率换算为人民币：') }}<span v-for="r in usedRates" :key="r.c" class="num rate">1 {{ r.c }} = {{ r.v ? `¥${r.v.toFixed(r.v < 0.1 ? 4 : 2)}` : t('无汇率') }}</span>
          <template v-if="rateDate">{{ t('（{source}，{date}）', { source: fx?.source ?? '', date: rateDate }) }}</template>
          <template v-if="fx?.error"> · {{ t('最近一次更新失败：{e}', { e: fx.error }) }}</template>
        </p>
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
.cash-sum { margin: 0 0 var(--space-2); }
.view-bar { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); flex-wrap: wrap; margin-bottom: var(--space-3); }
.view-bar .view-tabs { margin-bottom: 0; }
.cat-head { display: flex; align-items: center; gap: var(--space-2); padding: var(--space-3) var(--space-1) 0; }
.cat-head:first-child { padding-top: 0; }
.cat-head i { width: 10px; height: 10px; border-radius: 50%; flex: none; }
.cat-head .cat-sum { margin-left: auto; }
.subs { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-2); }
.sub-row { width: 100%; display: flex; align-items: center; gap: var(--space-3); padding: var(--space-3) var(--space-4); text-align: left;
  color: var(--text); font: inherit; cursor: pointer; }
.sub-row:hover { border-color: var(--text-muted); filter: none; }
.sub-row.ended { opacity: .5; }
.sub-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.id-inline { margin-left: var(--space-2); font-weight: normal; color: var(--text-muted); font-size: var(--font-sm); }
.cyc { font-weight: normal; font-size: var(--font-xs); }
.soon { white-space: nowrap; padding: 0 6px; border-radius: var(--radius-full); color: var(--warn); background: color-mix(in srgb, var(--warn) 12%, transparent); }
.sub-price { display: flex; flex-direction: column; align-items: flex-end; gap: 2px; text-align: right; flex: 0 1 auto; max-width: 48%; min-width: 0; }
/* 窄屏不重叠（修订第 237 条）：左侧每行超出时省略，右侧扣费说明可换行 */
.sub-main > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sub-price > span { white-space: normal; overflow-wrap: anywhere; }
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

/* 分析（修订第 223 条） */
.ana-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--space-3); flex-wrap: wrap; margin-bottom: var(--space-4); }
.ana-head > div:first-child { display: flex; flex-direction: column; gap: 2px; }
.ana-groups { margin-bottom: var(--space-4); }
.ana-top { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; }
.ana-top li { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-2) 0; }
.ana-top li + li { border-top: 1px solid var(--border); }
.rank { width: 20px; text-align: center; color: var(--text-muted); font-size: var(--font-sm); flex: none; }
.rate-note { margin: var(--space-3) 0 0; }
.rate { margin-right: var(--space-3); }
.flag { width: 16px; height: 12px; border-radius: 2px; vertical-align: -1px; object-fit: cover; }
.meta-line { display: inline-flex; align-items: center; gap: 4px; }

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
.countries { display: flex; gap: var(--space-1); flex-wrap: wrap; margin-bottom: var(--space-3); }
.chip { display: inline-flex; align-items: center; gap: 4px; padding: 3px var(--space-2); border-radius: var(--radius-full); background: var(--surface);
  color: var(--text-muted); border: 1px solid var(--border); font-size: var(--font-sm); }
.chip.active { color: var(--text); border-color: var(--accent); box-shadow: inset 0 0 0 1px var(--accent); }
.chip:hover { filter: none; color: var(--text); }
.pick-name small { font-size: var(--font-xs); }
.phones { display: flex; flex-direction: column; gap: var(--space-2); font-size: var(--font-sm); }
.phone-row { display: grid; grid-template-columns: 120px minmax(0, 1fr) auto; gap: var(--space-2); align-items: center; }
.phone-row small { grid-column: 1 / -1; }
.add-phone { align-self: flex-start; display: inline-flex; align-items: center; gap: 4px; color: var(--accent); }
.phone-line { color: var(--text); }
.ip-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: var(--space-2); align-items: center; }
.ip-row small { grid-column: 1 / -1; }
.mono { font-family: var(--font-mono); }
.node-link { flex: none; padding: 1px var(--space-2); border-radius: var(--radius-full); color: var(--ok);
  background: color-mix(in srgb, var(--ok) 12%, transparent); text-decoration: none; }
.del { margin-left: auto; }
@media (max-width: 600px) {
  .item { flex-basis: 40%; padding: var(--space-1) var(--space-3); }
  .item + .item { border-left: 0; }
  .donut-row { justify-content: center; }
  .bars { gap: var(--space-1); }
  .bar-label { font-size: 10px; }
}
</style>
