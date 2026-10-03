<script setup lang="ts">
// 节点详情顶部的概况卡片（设计 11.1）：名称与状态、CPU 型号，系统、负载、运行时间、容量、本周期流量等信息块，
// 以及系统与资产信息（主机名、IP、内核、Agent、供应商、续费与到期等）。
// 只显示读一眼就能用的静态或慢变信息，没有填写的资产字段不显示；实时指标在下方的指标块中。
import { computed, ref } from 'vue'
import type { ServerView } from '../api'
import { countryName } from '../countries'
import { DASH, fmtBandwidth, fmtBytes, fmtBytesShort, fmtPrice, fmtTime, fmtTraffic, fmtUptime, periodNames } from '../format'
import AgentUpgrade from './AgentUpgrade.vue'
import Flag from './Flag.vue'
import StatusDot from './StatusDot.vue'
import { displayStatus } from '../metrics'

const props = defineProps<{
  /** 节点（latest 可能不存在：尚未上报或离线很久） */
  server: ServerView
  /** 是否在线；离线时不显示负载等实时值 */
  live: boolean
}>()
const emit = defineEmits<{ unauthorized: [] }>()

const s = computed(() => props.server)
const r = computed(() => s.value.latest)
const sys = computed(() => r.value?.system)
const diskTotal = computed(() => r.value?.disk.reduce((a, d) => a + d.total, 0))
// 副标题：位置 · 供应商与套餐；分组单独显示为标签
const subtitle = computed(() => [
  [s.value.country ? countryName(s.value.country) : '', s.value.region].filter(Boolean).join(' '),
  [s.value.provider, s.value.plan].filter(Boolean).join(' '),
].filter(Boolean).join(' · '))

// 系统与资产：只列出有值的项
const expireDays = computed(() => {
  if (!s.value.expire_date) return null
  return Math.ceil((new Date(s.value.expire_date + 'T00:00:00').getTime() - Date.now()) / 86400000)
})
type Fact = { k: string; v: string; copy?: boolean; mono?: boolean; tone?: string; wide?: boolean }
const facts = computed<Fact[]>(() => {
  const x = s.value
  const out: Fact[] = []
  const add = (f: Fact) => f.v && out.push(f)
  add({ k: '主机名', v: sys.value?.hostname || x.hostname || '' })
  add({ k: 'IPv4', v: x.ipv4 || x.expected_ipv4 || '', copy: true, mono: true })
  add({ k: 'IPv6', v: x.ipv6 || x.expected_ipv6 || '', copy: true, mono: true })
  add({ k: '内核', v: sys.value?.kernel || '', mono: true })
  add({ k: '带宽', v: x.bandwidth_mbps ? fmtBandwidth(x.bandwidth_mbps) : '' })
  if (x.price_cents) add({ k: '续费', v: fmtPrice(x.price_cents, x.currency) + (x.billing_period ? ` / ${periodNames[x.billing_period]}` : '') })
  if (x.expire_date) {
    const d = expireDays.value!
    add({ k: '到期', v: `${x.expire_date}（${d >= 0 ? `${d} 天后` : `已过期 ${-d} 天`}）`, tone: d <= 3 ? 'bad' : d <= 14 ? 'warn' : '' })
  }
  if (x.enrolled_at) add({ k: '注册', v: fmtTime(x.enrolled_at) })
  if (x.last_seen_at) add({ k: '最后上报', v: fmtTime(x.last_seen_at) })
  add({ k: '备注', v: x.note || '', wide: true })
  return out
})

const copied = ref('')
async function copy(v: string) {
  try {
    await navigator.clipboard.writeText(v)
    copied.value = v
    setTimeout(() => copied.value === v && (copied.value = ''), 1500)
  } catch {
    /* 剪贴板不可用（非 HTTPS 等）时忽略 */
  }
}

// 信息块：图标为 Lucide 路径（ISC 许可），线宽 1.5（设计 41.4.3）
const tiles = computed(() => [
  { icon: 'M4 6h16M4 12h16M4 18h10', label: '操作系统', value: [sys.value?.os, sys.value?.os_version].filter(Boolean).join(' ') || DASH },
  { icon: 'M9 3v2M15 3v2M9 19v2M15 19v2M3 9h2M3 15h2M19 9h2M19 15h2M7 5h10v14H7z', label: '架构', value: sys.value?.arch || DASH },
  {
    icon: 'M3 3v18h18M7 15l4-4 3 3 5-6', label: '负载 1 / 5 / 15',
    value: props.live && r.value
      ? [r.value.cpu.load1, r.value.cpu.load5, r.value.cpu.load15].filter((v) => v != null).map((v) => v!.toFixed(2)).join('/')
      : DASH,
  },
  { icon: 'M12 6v6l4 2M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20Z', label: '运行时间', value: props.live ? fmtUptime(sys.value?.uptime) : DASH },
  {
    icon: 'M12 2 2 7l10 5 10-5-10-5ZM2 17l10 5 10-5M2 12l10 5 10-5', label: '内存',
    value: r.value ? fmtBytes(r.value.memory.total) : DASH,
    extra: r.value?.swap.total ? `Swap ${fmtBytesShort(r.value.swap.total)}` : '',
  },
  {
    icon: 'M22 12H2M5.5 5h13L22 12v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-6L5.5 5ZM6 16h.01M10 16h.01', label: '磁盘',
    value: diskTotal.value ? fmtBytes(diskTotal.value) : DASH,
    extra: r.value && r.value.disk.length > 1 ? `挂载 ${r.value.disk.length}` : '',
  },
  { icon: 'M12 19V5M5 12l7-7 7 7', label: '本周期上传', value: fmtTraffic(s.value.traffic.tx, s.value.traffic.unit), tone: 'up' },
  { icon: 'M12 5v14M19 12l-7 7-7-7', label: '本周期下载', value: fmtTraffic(s.value.traffic.rx, s.value.traffic.unit), tone: 'down' },
])
</script>

<template>
  <section class="panel summary">
    <div class="head">
      <div class="name-row">
        <Flag :code="s.country" round />
        <div class="names">
          <h1>{{ s.name }}</h1>
          <p v-if="subtitle || s.group" class="muted small sub">
            <span v-if="s.group" class="group">{{ s.group }}</span>{{ subtitle }}
          </p>
        </div>
      </div>
      <span class="badge" :class="displayStatus(s)"><StatusDot :status="displayStatus(s)" /></span>
    </div>

    <div class="grid">
      <div v-if="sys?.cpu_model" class="tile wide">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 3v2M15 3v2M9 19v2M15 19v2M3 9h2M3 15h2M19 9h2M19 15h2M7 5h10v14H7zM10 9h4v6h-4z" /></svg>
        <div class="body">
          <div class="label">CPU</div>
          <div class="value" :title="sys.cpu_model"><span class="ellipsis">{{ sys.cpu_model }}</span><span v-if="r" class="extra">{{ r.cpu.cores }} 核</span></div>
        </div>
      </div>
      <div v-for="t in tiles" :key="t.label" class="tile" :class="t.tone">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path :d="t.icon" /></svg>
        <div class="body">
          <div class="label">{{ t.label }}</div>
          <div class="value num"><span class="ellipsis">{{ t.value }}</span><span v-if="t.extra" class="extra">{{ t.extra }}</span></div>
        </div>
      </div>
    </div>

    <!-- 系统与资产 -->
    <dl class="facts">
      <div v-if="s.status !== 'pending'" class="fact">
        <dt>Agent</dt>
        <dd><AgentUpgrade :server-id="s.id" :current="r?.agent_version" @unauthorized="emit('unauthorized')" /></dd>
      </div>
      <div v-for="f in facts" :key="f.k" class="fact" :class="{ wide: f.wide }">
        <dt>{{ f.k }}</dt>
        <dd :class="[f.tone, { mono: f.mono }]">
          <button v-if="f.copy" type="button" class="copy" :title="copied === f.v ? '已复制' : '点击复制'" @click="copy(f.v)">
            {{ f.v }}<span class="copied" :class="{ on: copied === f.v }">已复制</span>
          </button>
          <template v-else>{{ f.v }}</template>
        </dd>
      </div>
    </dl>
  </section>
</template>

<style scoped>
.summary { padding: var(--space-4); }
.head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); margin-bottom: var(--space-4); }
.name-row { display: flex; align-items: center; gap: var(--space-3); min-width: 0; font-size: 22px; }
.names { min-width: 0; }
.names h1 { margin: 0; font-size: var(--font-xl); line-height: var(--line-xl); font-weight: 700; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sub { margin: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.badge { padding: 3px var(--space-3); border-radius: var(--radius-full); flex: none; border: 1px solid var(--border); background: var(--surface-2); }
.badge.online { background: color-mix(in srgb, var(--ok) 12%, transparent); border-color: color-mix(in srgb, var(--ok) 30%, transparent); color: var(--ok); }
.badge.offline { background: color-mix(in srgb, var(--bad) 12%, transparent); border-color: color-mix(in srgb, var(--bad) 30%, transparent); color: var(--bad); }
.badge.unknown { background: color-mix(in srgb, var(--warn) 12%, transparent); border-color: color-mix(in srgb, var(--warn) 30%, transparent); color: var(--warn); }

/* 信息块：手机两列，宽屏四列；CPU 型号独占一行 */
.grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-2); }
.tile { display: flex; align-items: center; gap: var(--space-3); padding: 10px var(--space-3); border-radius: 10px;
  border: 1px solid var(--border); background: var(--surface); min-width: 0; }
.tile.wide { grid-column: 1 / -1; }
.tile svg { width: 18px; height: 18px; flex: none; fill: none; stroke: var(--text-muted); stroke-width: 1.5; stroke-linecap: round; stroke-linejoin: round; }
.tile.up svg { stroke: var(--ok); }
.tile.down svg { stroke: var(--accent); }
.body { min-width: 0; flex: 1; }
.label { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); }
.value { display: flex; align-items: baseline; justify-content: space-between; gap: var(--space-2);
  font-size: var(--font-md); line-height: var(--line-md); font-weight: var(--weight-strong); min-width: 0; }
.ellipsis { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
.extra { font-size: var(--font-xs); font-weight: var(--weight-regular); color: var(--text-muted); white-space: nowrap; flex: none; }
@media (min-width: 900px) {
  .grid { grid-template-columns: repeat(4, minmax(0, 1fr)); }
}

.group { display: inline-block; margin-right: var(--space-2); padding: 0 var(--space-2); border-radius: var(--radius-full);
  background: color-mix(in srgb, var(--accent) 12%, transparent); color: var(--accent); font-size: var(--font-xs); line-height: var(--line-sm); }

/* 系统与资产：紧凑的标签 / 数值列表，宽屏多列 */
.facts { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: var(--space-2) var(--space-5);
  margin: var(--space-4) 0 0; padding-top: var(--space-4); border-top: 1px solid var(--border); }
.fact { display: flex; align-items: baseline; gap: var(--space-3); min-width: 0; }
.fact.wide { grid-column: 1 / -1; }
.fact dt { flex: none; width: 56px; font-size: var(--font-xs); line-height: var(--line-md); color: var(--text-muted); }
.fact dd { margin: 0; min-width: 0; font-size: var(--font-sm); line-height: var(--line-md); overflow-wrap: anywhere; }
.fact dd.mono { font-family: var(--font-mono); }
.fact dd.warn { color: var(--warn); }
.fact dd.bad { color: var(--bad); }
.copy { all: unset; cursor: pointer; position: relative; overflow-wrap: anywhere; }
.copy:hover { color: var(--accent); }
.copy:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; border-radius: 2px; }
.copied { display: none; margin-left: var(--space-2); font-family: var(--font-family); font-size: var(--font-xs); color: var(--ok); }
.copied.on { display: inline; }
/* 手机两列较窄：次要信息（Swap、挂载数）换到数值下方，避免把数值挤成省略号 */
@media (max-width: 600px) {
  .facts { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-3); }
  .fact { flex-direction: column; gap: 0; }
  .fact dt { width: auto; line-height: var(--line-xs); }
  .tile { padding: var(--space-2) var(--space-3); gap: var(--space-2); }
  .tile:not(.wide) .value { flex-direction: column; align-items: flex-start; gap: 0; }
}
</style>
