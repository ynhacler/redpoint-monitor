<script setup lang="ts">
// 连通性测试（设计 48，修订第 190 条）：从当前浏览器测试到中国、美国与全球常用网站的连通性与延迟，用来判断分流规则是否生效、线路是否通畅。
// 测试在 sandbox iframe（/probe.html，不带 allow-same-origin）中进行：面板页面本身的 CSP 只允许连接面板，
// 探测页有单独的 CSP（只允许 HTTPS 请求），拿不到面板的 Cookie 与页面，结果经 postMessage 交回。
// 每个网站显示本次延迟与最近 8 次的结果（圆点）；历史只保存在本机浏览器。
// 状态（修订第 226 条）：检测中、未检测、成功、超时、无法连接各有文字；本次还没测时显示上次保存的结果并标“上次”；
// 分组摘要只统计本次已完成的检测，平均延迟只算成功样本；写明探测源是本浏览器、分组是目标网站所在地区。
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { t } from '../i18n'
import { siteIcons } from '../siteIcons'
import { siteLogos } from '../siteLogos'
import { FAIL, HISTORY, grade, grades, siteGroups, siteState, summarize, type Grade, type Sample, type Site } from '../connectivity'

const frame = ref<HTMLIFrameElement | null>(null)
const ready = ref(false)
const latest = reactive<Record<string, Sample | undefined>>({}) // undefined：本次还没有结果
const running = reactive<Record<string, boolean>>({})
const history = reactive<Record<string, Sample[]>>(loadHistory())
const testedAt = reactive<Record<string, number>>(load('vpsmon.connectivity.at')) // 分组最近一次完成检测的时间（毫秒）
let runSeq = 0
const runGroups = new Map<number, string[]>() // 一次测试包含的分组

function load<T>(key: string): Record<string, T> {
  try {
    return JSON.parse(localStorage.getItem(key) ?? '{}')
  } catch {
    return {}
  }
}
function loadHistory() {
  return load<Sample[]>('vpsmon.connectivity')
}
function saveHistory() {
  try {
    localStorage.setItem('vpsmon.connectivity', JSON.stringify(history))
    localStorage.setItem('vpsmon.connectivity.at', JSON.stringify(testedAt))
  } catch {
    /* 隐私模式等不可用时不保存历史 */
  }
}

/** 测试若干分组：清空本次结果，发给探测页 */
function run(keys: string[]) {
  if (!ready.value || !frame.value?.contentWindow) return
  const targets: { id: string; url: string; urls: string[] }[] = []
  for (const g of siteGroups.filter((g) => keys.includes(g.key))) {
    running[g.key] = true
    for (const s of g.sites) {
      latest[s.id] = undefined
      targets.push({ id: s.id, url: s.url, urls: [s.url, ...(s.alt ?? [])] }) // url 给缓存了旧探测页的浏览器
    }
  }
  const seq = ++runSeq
  runGroups.set(seq, keys)
  frame.value.contentWindow.postMessage({ type: 'run', run: seq, targets, timeoutMs: 5000 }, '*')
}
const runAll = () => run(siteGroups.map((g) => g.key))

function onMessage(e: MessageEvent) {
  // 只接受探测页的消息
  if (!frame.value || e.source !== frame.value.contentWindow || !e.data) return
  const d = e.data as { type: string; run?: number; id?: string; ms?: number | null; fail?: string }
  if (d.type === 'ready') {
    ready.value = true
    runAll()
  } else if (d.type === 'result' && typeof d.id === 'string' && d.id in idSet) {
    const ms = typeof d.ms === 'number' && Number.isFinite(d.ms) && d.ms >= 0 ? d.ms : d.fail === 'error' ? FAIL : null
    latest[d.id] = ms
    history[d.id] = [...(history[d.id] ?? []), ms].slice(-HISTORY)
  } else if (d.type === 'done' && typeof d.run === 'number') {
    for (const k of runGroups.get(d.run) ?? []) {
      running[k] = false
      testedAt[k] = Date.now()
    }
    runGroups.delete(d.run)
    saveHistory()
  }
}
const idSet: Record<string, true> = Object.fromEntries(siteGroups.flatMap((g) => g.sites.map((s) => [s.id, true])))
onMounted(() => window.addEventListener('message', onMessage))
onBeforeUnmount(() => window.removeEventListener('message', onMessage))

// 分组摘要：只统计本次已完成的检测（修订第 226 条）
const summaries = computed(() => Object.fromEntries(siteGroups.map((g) => [g.key, summarize(g.sites.map((s) => latest[s.id]))])))
const state = (s: Site, group: string) => siteState(latest[s.id], !!running[group], history[s.id] ?? [])
const hhmm = (ms: number) => new Date(ms).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
const anyRunning = computed(() => Object.values(running).some(Boolean))

const dots = (s: Site) => {
  const h = history[s.id] ?? []
  return [...Array.from({ length: HISTORY - h.length }, () => 'empty' as const), ...h.map((v) => grade(v))]
}
const legend: Grade[] = ['best', 'good', 'slow', 'timeout', 'fail']
const logo = (s: Site) => (s.icon ? siteIcons[s.icon] : undefined)
// 单色标志（修订第 197 条）：品牌色图形放在同色的浅底方块中，与图片标志一样大小；
// 品牌色很深（黑色系，如 X、TikTok、GitHub）时图形用正文色、底色用中性色，深色模式下不会看不见
function tint(hex: string) {
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
  if (0.2126 * r + 0.7152 * g + 0.0722 * b < 0.15) return { color: 'var(--text)', background: 'var(--surface-2)' }
  return { color: `#${hex}`, background: `color-mix(in srgb, #${hex} 14%, transparent)` }
}
</script>

<template>
  <main class="page">
    <div class="page-head"><h1>{{ t('连通性测试') }}</h1></div>
    <p class="muted intro">{{ t('一键测试当前网络到各地常用网站的连通性与延迟，用来判断分流规则是否生效、线路是否通畅。测试在本浏览器中进行，不经过面板与 VPS。') }}</p>
    <p class="small muted intro">{{ t('探测源：本浏览器（当前网络与代理）。下面的“中国 / 美国 / 全球”是目标网站所在地区，不是探测地点。') }}</p>

    <div class="panel bar">
      <button type="button" class="secondary" :disabled="!ready || anyRunning" @click="runAll">
        <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" class="spin-icon" :class="{ spinning: anyRunning }">
          <path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        {{ anyRunning ? t('测试中…') : t('重新测试') }}
      </button>
      <span class="legend small">
        <span v-for="g in legend" :key="g"><i :style="{ background: grades[g].color }" />{{ t(grades[g].name) }}</span>
        <span><i class="empty" />{{ t('无记录') }}</span>
        <span>{{ t('圆点为最近 {n} 次，右边最新', { n: HISTORY }) }}</span>
      </span>
    </div>

    <section v-for="g in siteGroups" :key="g.key" class="group">
      <div class="group-head">
        <span class="flag">{{ g.flag }}</span>
        <h2>{{ t(g.name) }}</h2>
        <span class="tag small muted">{{ t('目标网站') }}</span>
        <span class="muted small num">
          <template v-if="running[g.key]">{{ t('检测中 {d}/{n}', { d: summaries[g.key].done, n: summaries[g.key].total }) }}</template>
          <template v-else-if="!summaries[g.key].done">{{ testedAt[g.key] ? t('上次检测 {time}', { time: hhmm(testedAt[g.key]) }) : t('未检测') }}</template>
          <template v-else>
            {{ t('成功') }} <b>{{ summaries[g.key].ok }}/{{ summaries[g.key].done }}</b>
            <template v-if="summaries[g.key].timeout"> · {{ t('超时') }} {{ summaries[g.key].timeout }}</template>
            <template v-if="summaries[g.key].fail"> · {{ t('无法连接') }} {{ summaries[g.key].fail }}</template>
            · <template v-if="summaries[g.key].avg != null">{{ t('成功样本平均') }} <b>{{ summaries[g.key].avg }} ms</b></template><template v-else>{{ t('无成功样本') }}</template>
            <template v-if="testedAt[g.key]"> · {{ hhmm(testedAt[g.key]) }}</template>
          </template>
        </span>
        <button type="button" class="secondary small refresh" :disabled="!ready || running[g.key]" @click="run([g.key])">{{ t('刷新') }}</button>
      </div>
      <ul class="sites">
        <li v-for="s in g.sites" :key="s.id" class="panel site">
          <span v-if="siteLogos[s.id]" class="logo img" :class="siteLogos[s.id].fit" :style="{ background: siteLogos[s.id].bg }">
            <img :src="siteLogos[s.id].src" alt="" />
          </span>
          <span v-else class="logo" :style="logo(s) ? tint(logo(s)!.hex) : { background: s.color }" :class="{ tile: !logo(s) }">
            <svg v-if="logo(s)" viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><path :d="logo(s)!.path" fill="currentColor" /></svg>
            <span v-else>{{ s.letter ?? s.name[0] }}</span>
          </span>
          <span class="site-main">
            <b class="ellipsis">{{ t(s.name) }}</b>
            <span class="dots" :aria-label="t('最近 {n} 次', { n: HISTORY })">
              <i v-for="(d, i) in dots(s)" :key="i" :style="d === 'empty' ? undefined : { background: grades[d].color }" :class="{ empty: d === 'empty' }"
                :title="d === 'empty' ? t('无记录') : t(grades[d].name)" />
            </span>
          </span>
          <span v-for="st in [state(s, g.key)]" :key="st.kind" class="ms num" :class="{ stale: 'stale' in st && st.stale }"
            :style="st.kind === 'ok' ? { color: grades[grade(st.ms)].color } : st.kind === 'timeout' || st.kind === 'fail' ? { color: 'var(--bad)' } : undefined">
            <small v-if="'stale' in st && st.stale" class="muted">{{ t('上次') }} </small>
            <template v-if="st.kind === 'running'"><span class="muted state">{{ t('检测中') }}</span></template>
            <template v-else-if="st.kind === 'untested'"><span class="muted state">{{ t('未检测') }}</span></template>
            <template v-else-if="st.kind === 'timeout'">{{ t('超时') }}</template>
            <template v-else-if="st.kind === 'fail'">{{ t('无法连接') }}</template>
            <template v-else-if="st.kind === 'ok'">{{ st.ms }} ms</template>
          </span>
        </li>
      </ul>
    </section>

    <!-- 探测页：sandbox 不带 allow-same-origin，见 web/public/probe.js -->
    <iframe ref="frame" class="probe" src="/probe.html" sandbox="allow-scripts" :title="t('连通性探测')" aria-hidden="true" tabindex="-1" />
  </main>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-4); max-width: 80ch; }
.bar { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); flex-wrap: wrap; padding: var(--space-3) var(--space-4);
  margin-bottom: var(--space-5); background: var(--surface-2); }
.bar button { display: inline-flex; align-items: center; gap: var(--space-2); }
.spinning { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
.legend { display: flex; gap: var(--space-4); color: var(--text-muted); }
.legend span { display: inline-flex; align-items: center; gap: 6px; }
.legend { flex-wrap: wrap; }
.legend i { width: 10px; height: 10px; border-radius: 50%; display: inline-block; }
.legend i.empty { background: var(--track); }
.tag { border: 1px solid var(--border); border-radius: 999px; padding: 0 8px; }
.ms.stale { opacity: 0.6; }
.ms .state { font-size: var(--font-sm); font-weight: normal; }
.group { margin-bottom: var(--space-6); }
.group-head { display: flex; align-items: center; gap: var(--space-3); margin-bottom: var(--space-3); flex-wrap: wrap; }
.group-head h2 { margin: 0; font-size: var(--font-xl); }
.group-head b { color: var(--text); }
.flag { font-size: 24px; line-height: 1; }
.refresh { margin-left: auto; }
.sites { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: var(--space-2); }
.site { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-3) var(--space-4); }
.logo { width: 32px; height: 32px; flex: none; display: grid; place-items: center; border-radius: 8px; }
.logo.tile { color: var(--on-accent); font-weight: var(--weight-strong); font-size: var(--font-sm); }
.logo.img { overflow: hidden; box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--text) 8%, transparent); }
.logo.img img { display: block; }
.logo.fill img { width: 100%; height: 100%; object-fit: cover; }
.logo.contain img { width: 76%; height: 76%; object-fit: contain; }
.site-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 6px; }
.ellipsis { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.dots { display: flex; gap: 4px; }
.dots i { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }
.dots i.empty { background: var(--track); }
.ms { flex: none; font-size: var(--font-lg); font-weight: var(--weight-strong); }
.probe { position: absolute; width: 0; height: 0; border: 0; visibility: hidden; }
@media (max-width: 600px) {
  .legend { gap: var(--space-3); }
  .sites { grid-template-columns: minmax(0, 1fr); }
}
</style>
