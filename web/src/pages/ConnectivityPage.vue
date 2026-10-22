<script setup lang="ts">
// 连通性测试（设计 48，修订第 190 条）：从当前浏览器测试到中国、美国与全球常用网站的连通性与延迟，用来判断分流规则是否生效、线路是否通畅。
// 测试在 sandbox iframe（/probe.html，不带 allow-same-origin）中进行：面板页面本身的 CSP 只允许连接面板，
// 探测页有单独的 CSP（只允许 HTTPS 请求），拿不到面板的 Cookie 与页面，结果经 postMessage 交回。
// 每个网站显示本次延迟与最近 8 次的结果（圆点）；历史只保存在本机浏览器。
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { siteIcons } from '../siteIcons'
import { HISTORY, grade, grades, siteGroups, type Grade, type Site } from '../connectivity'

const frame = ref<HTMLIFrameElement | null>(null)
const ready = ref(false)
const latest = reactive<Record<string, number | null | undefined>>({}) // undefined：测试中或还没测
const running = reactive<Record<string, boolean>>({})
const history = reactive<Record<string, (number | null)[]>>(loadHistory())
let runSeq = 0
const runGroups = new Map<number, string[]>() // 一次测试包含的分组

function loadHistory(): Record<string, (number | null)[]> {
  try {
    return JSON.parse(localStorage.getItem('vpsmon.connectivity') ?? '{}')
  } catch {
    return {}
  }
}
function saveHistory() {
  try {
    localStorage.setItem('vpsmon.connectivity', JSON.stringify(history))
  } catch {
    /* 隐私模式等不可用时不保存历史 */
  }
}

/** 测试若干分组：清空本次结果，发给探测页 */
function run(keys: string[]) {
  if (!ready.value || !frame.value?.contentWindow) return
  const targets: { id: string; url: string }[] = []
  for (const g of siteGroups.filter((g) => keys.includes(g.key))) {
    running[g.key] = true
    for (const s of g.sites) {
      latest[s.id] = undefined
      targets.push({ id: s.id, url: s.url })
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
  const d = e.data as { type: string; run?: number; id?: string; ms?: number | null }
  if (d.type === 'ready') {
    ready.value = true
    runAll()
  } else if (d.type === 'result' && typeof d.id === 'string' && d.id in idSet) {
    const ms = typeof d.ms === 'number' && Number.isFinite(d.ms) ? d.ms : null
    latest[d.id] = ms
    history[d.id] = [...(history[d.id] ?? []), ms].slice(-HISTORY)
  } else if (d.type === 'done' && typeof d.run === 'number') {
    for (const k of runGroups.get(d.run) ?? []) running[k] = false
    runGroups.delete(d.run)
    saveHistory()
  }
}
const idSet: Record<string, true> = Object.fromEntries(siteGroups.flatMap((g) => g.sites.map((s) => [s.id, true])))
onMounted(() => window.addEventListener('message', onMessage))
onBeforeUnmount(() => window.removeEventListener('message', onMessage))

// 分组摘要：可达数 / 总数、平均延迟（只算本次可达的）
const summaries = computed(() => Object.fromEntries(siteGroups.map((g) => {
  const done = g.sites.map((s) => latest[s.id]).filter((v) => v !== undefined) as (number | null)[]
  const ok = done.filter((v): v is number => v != null)
  return [g.key, { done: done.length, ok: ok.length, avg: ok.length ? Math.round(ok.reduce((a, b) => a + b, 0) / ok.length) : null }]
})))
const anyRunning = computed(() => Object.values(running).some(Boolean))

const dots = (s: Site) => {
  const h = history[s.id] ?? []
  return [...Array.from({ length: HISTORY - h.length }, () => 'empty' as const), ...h.map((v) => grade(v))]
}
const legend: Grade[] = ['best', 'good', 'slow', 'timeout']
const logo = (s: Site) => (s.icon ? siteIcons[s.icon] : undefined)
// 标志颜色：品牌色很深（黑色系，如 X、TikTok、GitHub）时用正文色，深色模式下不会看不见
function logoColor(hex: string) {
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b < 0.15 ? 'var(--text)' : `#${hex}`
}
</script>

<template>
  <main class="page">
    <div class="page-head"><h1>连通性测试</h1></div>
    <p class="muted intro">一键测试当前网络到各地常用网站的连通性与延迟，用来判断分流规则是否生效、线路是否通畅。测试在本浏览器中进行，不经过面板与 VPS。</p>

    <div class="panel bar">
      <button type="button" class="secondary" :disabled="!ready || anyRunning" @click="runAll">
        <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" class="spin-icon" :class="{ spinning: anyRunning }">
          <path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        {{ anyRunning ? '测试中…' : '重新测试' }}
      </button>
      <span class="legend small">
        <span v-for="g in legend" :key="g"><i :style="{ background: grades[g].color }" />{{ grades[g].name }}</span>
      </span>
    </div>

    <section v-for="g in siteGroups" :key="g.key" class="group">
      <div class="group-head">
        <span class="flag">{{ g.flag }}</span>
        <h2>{{ g.name }}</h2>
        <span class="muted small num">
          可达 <b>{{ summaries[g.key].ok }}/{{ g.sites.length }}</b>
          <template v-if="summaries[g.key].avg != null"> · 平均 <b>{{ summaries[g.key].avg }}ms</b></template>
        </span>
        <button type="button" class="secondary small refresh" :disabled="!ready || running[g.key]" @click="run([g.key])">刷新</button>
      </div>
      <ul class="sites">
        <li v-for="s in g.sites" :key="s.id" class="panel site">
          <span class="logo" :style="logo(s) ? { color: logoColor(logo(s)!.hex) } : { background: s.color }" :class="{ tile: !logo(s) }">
            <svg v-if="logo(s)" viewBox="0 0 24 24" width="22" height="22" aria-hidden="true"><path :d="logo(s)!.path" fill="currentColor" /></svg>
            <span v-else>{{ s.letter ?? s.name[0] }}</span>
          </span>
          <span class="site-main">
            <b class="ellipsis">{{ s.name }}</b>
            <span class="dots" :aria-label="`最近 ${HISTORY} 次`">
              <i v-for="(d, i) in dots(s)" :key="i" :style="d === 'empty' ? undefined : { background: grades[d].color }" :class="{ empty: d === 'empty' }" />
            </span>
          </span>
          <span class="ms num" :style="latest[s.id] !== undefined ? { color: grades[grade(latest[s.id]!)].color } : undefined">
            <template v-if="latest[s.id] === undefined"><span class="muted">{{ running[g.key] ? '…' : '—' }}</span></template>
            <template v-else-if="latest[s.id] === null">超时</template>
            <template v-else>{{ latest[s.id] }}ms</template>
          </span>
        </li>
      </ul>
    </section>

    <!-- 探测页：sandbox 不带 allow-same-origin，见 web/public/probe.js -->
    <iframe ref="frame" class="probe" src="/probe.html" sandbox="allow-scripts" title="连通性探测" aria-hidden="true" tabindex="-1" />
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
.legend i { width: 10px; height: 10px; border-radius: 50%; display: inline-block; }
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
