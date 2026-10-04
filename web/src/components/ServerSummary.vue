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
import Icon, { type IconName } from './Icon.vue'
import StatusDot from './StatusDot.vue'
import { displayStatus } from '../metrics'

const props = defineProps<{
  /** 节点（latest 可能不存在：尚未上报或离线很久） */
  server: ServerView
  /** 是否在线；离线时不显示负载等实时值 */
  live: boolean
}>()

const s = computed(() => props.server)
const r = computed(() => s.value.latest)
const sys = computed(() => r.value?.system)
const diskTotal = computed(() => r.value?.disk.reduce((a, d) => a + d.total, 0))
// 系统与资产：系统信息只列出有值的项；资产字段固定显示，未填写时为“—”，一眼看出哪些还没填
const expireDays = computed(() => {
  if (!s.value.expire_date) return null
  return Math.ceil((new Date(s.value.expire_date + 'T00:00:00').getTime() - Date.now()) / 86400000)
})
type Fact = { k: string; v: string; copy?: boolean; mono?: boolean; tone?: string; wide?: boolean; flag?: string }
const facts = computed<Fact[]>(() => {
  const x = s.value
  const out: Fact[] = []
  const add = (f: Fact) => f.v && out.push(f) // 系统信息：有值才显示
  const asset = (f: Fact) => out.push(f.v ? f : { ...f, v: DASH, copy: false, tone: 'empty' }) // 资产：始终显示
  add({ k: '主机名', v: sys.value?.hostname || x.hostname || '' })
  add({ k: 'IPv4', v: x.ipv4 || x.expected_ipv4 || '', copy: true, mono: true })
  add({ k: 'IPv6', v: x.ipv6 || x.expected_ipv6 || '', copy: true, mono: true })
  add({ k: '内核', v: sys.value?.kernel || '', mono: true })
  asset({ k: '供应商', v: x.provider })
  asset({ k: '套餐', v: x.plan })
  asset({ k: '带宽', v: x.bandwidth_mbps ? fmtBandwidth(x.bandwidth_mbps) : '' })
  asset({ k: '国家 / 地区', v: x.country ? countryName(x.country) : '', flag: x.country })
  asset({ k: '城市 / 机房', v: x.region })
  asset({ k: '分组', v: x.group })
  asset({ k: '续费', v: x.price_cents ? fmtPrice(x.price_cents, x.currency) + (x.billing_period ? ` / ${periodNames[x.billing_period]}` : '') : '' })
  const d = expireDays.value
  asset({ k: '到期', v: x.expire_date ? `${x.expire_date}（${d! >= 0 ? `${d} 天后` : `已过期 ${-d!} 天`}）` : '',
    tone: d == null ? '' : d <= 3 ? 'bad' : d <= 14 ? 'warn' : '' })
  add({ k: '注册', v: x.enrolled_at ? fmtTime(x.enrolled_at) : '' })
  add({ k: '最后上报', v: x.last_seen_at ? fmtTime(x.last_seen_at) : '' })
  asset({ k: '备注', v: x.note, wide: true })
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

// 信息块（图标见 Icon.vue，设计 41.4.3）
const tiles = computed<{ icon: IconName, label: string, value: string, extra?: string, tone?: string }[]>(() => [
  { icon: 'list', label: '操作系统', value: [sys.value?.os, sys.value?.os_version].filter(Boolean).join(' ') || DASH },
  { icon: 'cpu', label: '架构', value: sys.value?.arch || DASH },
  {
    icon: 'chart', label: '负载 1 / 5 / 15',
    value: props.live && r.value
      ? [r.value.cpu.load1, r.value.cpu.load5, r.value.cpu.load15].filter((v) => v != null).map((v) => v!.toFixed(2)).join('/')
      : DASH,
  },
  { icon: 'clock', label: '运行时间', value: props.live ? fmtUptime(sys.value?.uptime) : DASH },
  {
    icon: 'layers', label: '内存',
    value: r.value ? fmtBytes(r.value.memory.total) : DASH,
    extra: r.value?.swap.total ? `Swap ${fmtBytesShort(r.value.swap.total)}` : '',
  },
  {
    icon: 'hard-drive', label: '磁盘',
    value: diskTotal.value ? fmtBytes(diskTotal.value) : DASH,
    extra: r.value && r.value.disk.length > 1 ? `挂载 ${r.value.disk.length}` : '',
  },
  { icon: 'arrow-up', label: '本周期上传', value: fmtTraffic(s.value.traffic.tx, s.value.traffic.unit), tone: 'up' },
  { icon: 'arrow-down', label: '本周期下载', value: fmtTraffic(s.value.traffic.rx, s.value.traffic.unit), tone: 'down' },
])
</script>

<template>
  <section class="panel summary">
    <div class="head">
      <div class="name-row">
        <Flag :code="s.country" round />
        <div class="names">
          <h1>{{ s.name }}</h1>
        </div>
      </div>
      <span class="badge" :class="displayStatus(s)"><StatusDot :status="displayStatus(s)" /></span>
    </div>

    <div class="grid">
      <div v-if="sys?.cpu_model" class="tile wide">
        <Icon name="cpu" :size="18" />
        <div class="body">
          <div class="label">CPU</div>
          <div class="value" :title="sys.cpu_model"><span class="ellipsis">{{ sys.cpu_model }}</span><span v-if="r" class="extra">{{ r.cpu.cores }} 核</span></div>
        </div>
      </div>
      <div v-for="t in tiles" :key="t.label" class="tile" :class="t.tone">
        <Icon :name="t.icon" :size="18" />
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
        <dd><AgentUpgrade :server-id="s.id" :current="r?.agent_version" /></dd>
      </div>
      <div v-for="f in facts" :key="f.k" class="fact" :class="{ wide: f.wide }">
        <dt>{{ f.k }}</dt>
        <dd :class="[f.tone, { mono: f.mono }]">
          <button v-if="f.copy" type="button" class="copy" :title="copied === f.v ? '已复制' : '点击复制'" @click="copy(f.v)">
            {{ f.v }}<span class="copied" :class="{ on: copied === f.v }">已复制</span>
          </button>
          <template v-else><Flag v-if="f.flag" :code="f.flag" class="flag" />{{ f.v }}</template>
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
.badge { padding: 3px var(--space-3); border-radius: var(--radius-full); flex: none; border: 1px solid var(--border); background: var(--surface-2); }
.badge.online { background: color-mix(in srgb, var(--ok) 12%, transparent); border-color: color-mix(in srgb, var(--ok) 30%, transparent); color: var(--ok); }
.badge.offline { background: color-mix(in srgb, var(--bad) 12%, transparent); border-color: color-mix(in srgb, var(--bad) 30%, transparent); color: var(--bad); }
.badge.unknown { background: color-mix(in srgb, var(--warn) 12%, transparent); border-color: color-mix(in srgb, var(--warn) 30%, transparent); color: var(--warn); }

/* 信息块：手机两列，宽屏四列；CPU 型号独占一行 */
.grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-2); }
.tile { display: flex; align-items: center; gap: var(--space-3); padding: 10px var(--space-3); border-radius: 10px;
  border: 1px solid var(--border); background: var(--surface); min-width: 0; }
.tile.wide { grid-column: 1 / -1; }
.tile svg { flex: none; color: var(--text-muted); }
.tile.up svg { color: var(--ok); }
.tile.down svg { color: var(--accent); }
.body { min-width: 0; flex: 1; }
.label { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); }
.value { display: flex; align-items: baseline; justify-content: space-between; gap: var(--space-2);
  font-size: var(--font-md); line-height: var(--line-md); font-weight: var(--weight-strong); min-width: 0; }
.ellipsis { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
.extra { font-size: var(--font-xs); font-weight: var(--weight-regular); color: var(--text-muted); white-space: nowrap; flex: none; }
@media (min-width: 900px) {
  .grid { grid-template-columns: repeat(4, minmax(0, 1fr)); }
}


/* 系统与资产：紧凑的标签 / 数值列表，宽屏多列 */
.facts { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: var(--space-2) var(--space-5);
  margin: var(--space-4) 0 0; padding-top: var(--space-4); border-top: 1px solid var(--border); }
.fact { display: flex; align-items: baseline; gap: var(--space-3); min-width: 0; }
.fact.wide { grid-column: 1 / -1; }
.fact dt { flex: none; width: 68px; font-size: var(--font-xs); line-height: var(--line-md); color: var(--text-muted); }
.fact dd { margin: 0; min-width: 0; font-size: var(--font-sm); line-height: var(--line-md); overflow-wrap: anywhere; }
.fact dd.mono { font-family: var(--font-mono); }
.fact dd.warn { color: var(--warn); }
.fact dd.bad { color: var(--bad); }
.fact dd.empty { color: var(--text-muted); }
.flag { margin-right: var(--space-1); }
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
