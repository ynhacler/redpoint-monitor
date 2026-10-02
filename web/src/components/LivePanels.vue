<script setup lang="ts">
// 节点详情的实时指标块（设计 11.1、41.6）：CPU → 内存 → 磁盘 → 网络，顺序与 App 一致。
// 每块“左侧数值、右侧图形”：大号数值一眼可读，图形表达占比（环）或分布（每核分段条）。
// CPU 时间占比、每核、内存缓存、IOPS 等为可选字段（设计 4.4～4.9），旧版 Agent 不上报时对应部分不显示。
import { computed } from 'vue'
import type { DiskInfo, ServerView } from '../api'
import { DASH, fmtBytes, fmtBytesShort, fmtPct, fmtUptime } from '../format'
import { cpu, gaugeLevel, ioForDisk, mem, rx, tx } from '../metrics'
import Ring, { type RingSegment } from './Ring.vue'

const props = defineProps<{
  /** 节点；调用方保证 latest 存在且在线 */
  server: ServerView
}>()

const r = computed(() => props.server.latest!)
const b = computed(() => r.value.cpu.breakdown)
const cpuLv = computed(() => gaugeLevel(cpu(props.server), 'cpu'))

// CPU 时间分类（设计 4.4）：steal 持续偏高说明宿主机超售，iowait 偏高说明在等磁盘
const cpuParts = computed(() => {
  const x = b.value
  if (!x) return []
  return [
    { key: 'sys', name: 'SYS', value: x.system + x.irq + x.softirq, color: 'var(--bad)', hint: '内核态（含中断）' },
    { key: 'user', name: 'USER', value: x.user + x.nice, color: 'var(--ok)', hint: '用户态（含 nice）' },
    { key: 'iowait', name: 'IOWAIT', value: x.iowait, color: 'var(--series-2)', hint: '等待磁盘；计为空闲' },
    { key: 'steal', name: 'STEAL', value: x.steal, color: 'var(--warn)', hint: '被宿主机占用；持续偏高说明超售' },
  ]
})
const pct1 = (v: number) => (v >= 10 || v === 0 ? Math.round(v) : Math.round(v * 10) / 10)

// 每核分段条：16 核以内每核一行（30 格）；更多核心时改为紧凑的竖条网格，避免页面过长
const SEGMENTS = 30
const perCore = computed(() => r.value.cpu.per_core ?? [])
const compactCores = computed(() => perCore.value.length > 16)
const filled = (v: number) => Math.round((Math.max(0, Math.min(100, v)) / 100) * SEGMENTS)

// 负载三环：外到内依次为 1、5、15 分钟，按核心数归一（负载 = 核心数时满环）
const loadRings = computed(() => {
  const c = Math.max(1, r.value.cpu.cores)
  const vals = [r.value.cpu.load1, r.value.cpu.load5 ?? 0, r.value.cpu.load15 ?? 0]
  return vals.map((v, i) => {
    const ratio = v / c
    const radius = 22 - i * 7
    const circ = 2 * Math.PI * radius
    const len = Math.min(1, ratio) * circ
    return { radius, dash: `${len} ${circ - len}`, color: ratio >= 2 ? 'var(--bad)' : ratio >= 1 ? 'var(--warn)' : 'var(--ok)' }
  })
})
const loadText = computed(() =>
  [r.value.cpu.load1, r.value.cpu.load5, r.value.cpu.load15].filter((v) => v != null).map((v) => v!.toFixed(2)).join(' / '),
)

// 内存：已用（不含缓存）+ 缓存（设计 4.5）
const m = computed(() => r.value.memory)
const cache = computed(() => (m.value.buffers ?? 0) + (m.value.cached ?? 0))
const memLv = computed(() => gaugeLevel(mem(props.server), 'mem'))
const memSegments = computed<RingSegment[] | undefined>(() => {
  if (!m.value.total || m.value.cached == null) return undefined
  return [
    { value: m.value.usage, color: `var(--${memLv.value})` },
    { value: (cache.value / m.value.total) * 100, color: 'var(--series-2)' },
  ]
})
const swapPct = computed(() => (r.value.swap.total ? (r.value.swap.used / r.value.swap.total) * 100 : undefined))

// 网络：开机以来累计（与计费周期无关，计费用量见流量卡片）；环表示下行与上行的比例
const totalRx = computed(() => r.value.network.reduce((a, n) => a + n.rx_bytes, 0))
const totalTx = computed(() => r.value.network.reduce((a, n) => a + n.tx_bytes, 0))
const netSegments = computed<RingSegment[]>(() => {
  const sum = totalRx.value + totalTx.value
  if (!sum) return []
  return [
    { value: (totalRx.value / sum) * 100, color: 'var(--accent)' },
    { value: (totalTx.value / sum) * 100, color: 'var(--ok)' },
  ]
})

// 磁盘：竖向用量条 + 所在磁盘的 IO
const io = (d: DiskInfo) => ioForDisk(d, r.value.disk_io)
const iops = (v: number | undefined) => (v == null ? DASH : v >= 100 ? Math.round(v).toString() : v.toFixed(1))
</script>

<template>
  <div class="live">
    <!-- CPU -->
    <section class="block cpu">
      <h3 class="tag">CPU<span v-if="r.system.cpu_model" class="model">{{ r.system.cpu_model }}</span></h3>
      <div class="cpu-top">
        <div class="hero num" :class="cpuLv">{{ Math.round(cpu(server) ?? 0) }}<small>%</small></div>
        <dl v-if="cpuParts.length" class="parts">
          <div v-for="p in cpuParts" :key="p.key" :title="p.hint">
            <dt><i :style="{ background: p.color }" />{{ p.name }}</dt>
            <dd class="num" :class="{ warn: (p.key === 'steal' || p.key === 'iowait') && p.value >= 10 }">{{ pct1(p.value) }}<small>%</small></dd>
          </div>
        </dl>
      </div>

      <div v-if="perCore.length && !compactCores" class="cores">
        <div v-for="(v, i) in perCore" :key="i" class="core-row" :title="`CPU${i}  ${fmtPct(v)}`">
          <span v-for="k in SEGMENTS" :key="k" class="seg" :class="k <= filled(v) ? gaugeLevel(v, 'cpu') : ''" />
        </div>
      </div>
      <div v-else-if="perCore.length" class="cores-compact" :aria-label="`每核使用率，共 ${perCore.length} 核`">
        <span v-for="(v, i) in perCore" :key="i" class="vbar" :title="`CPU${i}  ${fmtPct(v)}`">
          <span :class="gaugeLevel(v, 'cpu')" :style="{ height: `${Math.max(3, v)}%` }" />
        </span>
      </div>

      <div class="foot">
        <dl class="facts">
          <div><dt>核心</dt><dd class="num">{{ r.cpu.cores }}</dd></div>
          <div v-if="b"><dt>空闲</dt><dd class="num">{{ pct1(b.idle) }}<small>%</small></dd></div>
          <div><dt>运行时间</dt><dd class="num">{{ fmtUptime(r.system.uptime) }}</dd></div>
          <div v-if="r.cpu.temp_c"><dt>温度</dt><dd class="num" :class="{ warn: r.cpu.temp_c >= 75, bad: r.cpu.temp_c >= 90 }">{{ r.cpu.temp_c.toFixed(0) }}<small>℃</small></dd></div>
          <div class="load"><dt>负载 1 / 5 / 15</dt><dd class="num">{{ loadText }}</dd></div>
        </dl>
        <svg class="load-rings" viewBox="0 0 52 52" role="img" :aria-label="`负载 ${loadText}，核心 ${r.cpu.cores}`">
          <g transform="rotate(-90 26 26)" fill="none" stroke-width="5">
            <template v-for="(l, i) in loadRings" :key="i">
              <circle cx="26" cy="26" :r="l.radius" stroke="var(--track)" />
              <circle cx="26" cy="26" :r="l.radius" :stroke="l.color" :stroke-dasharray="l.dash" stroke-linecap="round" />
            </template>
          </g>
        </svg>
      </div>
    </section>

    <!-- 内存 -->
    <section class="block row-block">
      <h3 class="tag">内存 <span class="num">{{ fmtBytes(m.total) }}</span></h3>
      <dl class="facts grow">
        <div v-if="m.free != null"><dt>FREE</dt><dd class="num big">{{ fmtBytesShort(m.free) }}</dd></div>
        <div><dt><i :style="{ background: `var(--${memLv})` }" />USED</dt><dd class="num big">{{ fmtBytesShort(m.used) }}</dd></div>
        <div v-if="m.cached != null"><dt><i style="background: var(--series-2)" />CACHE</dt><dd class="num big">{{ fmtBytesShort(cache) }}</dd></div>
        <div v-if="r.swap.total"><dt>SWAP</dt><dd class="num big">{{ fmtBytesShort(r.swap.used) }}<small> / {{ fmtBytesShort(r.swap.total) }}</small></dd></div>
      </dl>
      <Ring :pct="mem(server)" :level="memLv" :segments="memSegments" :size="64" label="内存" />
    </section>

    <!-- 磁盘（设计 4.6、4.7）：每个挂载点一块 -->
    <section v-for="d in r.disk" :key="d.mount" class="block disk">
      <div class="disk-head">
        <div class="disk-name">
          <div class="mount">{{ d.mount }}</div>
          <div class="muted small">{{ d.device || DASH }}</div>
        </div>
        <div class="disk-cap">
          <div class="muted small">{{ d.fstype }}</div>
          <div class="num">{{ fmtBytesShort(d.used) }}<small> / {{ fmtBytesShort(d.total) }}</small></div>
        </div>
        <span class="pill" :title="fmtPct(d.usage)"><span :class="gaugeLevel(d.usage, 'disk')" :style="{ height: `${Math.max(4, d.usage)}%` }" /></span>
      </div>
      <table v-if="io(d)" class="io num">
        <thead><tr><th /><th>速度</th><th>累计</th><th>IOPS</th><th title="每次 IO 的平均耗时（含排队）">耗时</th></tr></thead>
        <tbody>
          <tr>
            <th>R</th><td>{{ fmtBytesShort(io(d)!.read_speed, true) }}</td><td>{{ fmtBytesShort(io(d)!.read_bytes) }}</td>
            <td>{{ iops(io(d)!.read_iops) }}</td>
            <td rowspan="2" class="await">{{ io(d)!.await_ms != null ? `${io(d)!.await_ms!.toFixed(1)} ms` : DASH }}</td>
          </tr>
          <tr>
            <th>W</th><td>{{ fmtBytesShort(io(d)!.write_speed, true) }}</td><td>{{ fmtBytesShort(io(d)!.write_bytes) }}</td>
            <td>{{ iops(io(d)!.write_iops) }}</td>
          </tr>
        </tbody>
      </table>
      <p v-if="io(d)?.util != null" class="muted small util">{{ io(d)!.device }} 繁忙 {{ fmtPct(io(d)!.util) }}</p>
    </section>

    <!-- 网络 -->
    <section class="block row-block net">
      <h3 class="tag">网络</h3>
      <div class="speeds">
        <div><dt>↓ 下行</dt><dd class="num big">{{ fmtBytesShort(rx(server), true) }}</dd></div>
        <div><dt>↑ 上行</dt><dd class="num big">{{ fmtBytesShort(tx(server), true) }}</dd></div>
      </div>
      <dl class="totals" title="开机以来累计（网卡计数，重启后清零）；计费用量见“本周期流量”">
        <div><dt><i style="background: var(--accent)" />↓</dt><dd class="num">{{ fmtBytesShort(totalRx) }}</dd></div>
        <div><dt><i style="background: var(--ok)" />↑</dt><dd class="num">{{ fmtBytesShort(totalTx) }}</dd></div>
      </dl>
      <Ring v-if="netSegments.length" :pct="undefined" :segments="netSegments" :size="56" label="下行与上行累计比例" class="net-ring" />
      <p v-if="r.conns" class="conns muted small num">
        TCP {{ r.conns.tcp }} · UDP {{ r.conns.udp }} · TIME_WAIT {{ r.conns.time_wait }}
        <template v-if="r.processes"> · 进程 {{ r.processes.total }}</template>
        <span class="ifaces">{{ r.network.map((n) => n.interface).join(' · ') }}</span>
      </p>
    </section>
  </div>
</template>

<style scoped>
.live { display: grid; grid-template-columns: repeat(auto-fit, minmax(340px, 1fr)); gap: var(--space-3); }
.block { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md); padding: var(--space-4); min-width: 0; }
.cpu { grid-column: 1 / -1; }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
dl, dd { margin: 0; }
dt { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); display: flex; align-items: center; gap: var(--space-1);
  white-space: nowrap; letter-spacing: .02em; }
dt i { width: 4px; height: 10px; border-radius: 2px; display: inline-block; }
dd small, .hero small, .num small { font-size: .6em; color: var(--text-muted); margin-left: 1px; font-weight: var(--weight-regular); }
.big { font-size: var(--font-xl); line-height: var(--line-xl); }

.tag { flex-basis: 100%; margin: 0 0 var(--space-3); font-size: var(--font-sm); line-height: var(--line-sm); color: var(--text-muted);
  font-weight: var(--weight-strong); display: flex; gap: var(--space-2); min-width: 0; }
.tag .num { font-weight: var(--weight-regular); }
.model { font-weight: var(--weight-regular); margin-left: auto; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.row-block .tag { margin-bottom: 0; }

/* CPU */
.cpu-top { display: flex; align-items: flex-start; gap: var(--space-6); flex-wrap: wrap; }
.hero { font-size: 44px; line-height: 48px; font-weight: var(--weight-regular); }
.hero.warn { color: var(--warn); }
.hero.bad { color: var(--bad); }
.parts { flex: 1; display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--space-3); min-width: 240px; }
.parts dd { font-size: var(--font-xl); line-height: var(--line-xl); }
.cores { display: flex; flex-direction: column; gap: 5px; margin-top: var(--space-4); }
.core-row { display: grid; grid-template-columns: repeat(30, minmax(0, 1fr)); gap: 3px; }
.seg { height: 10px; border-radius: 2px; background: var(--track); }
.seg.ok { background: var(--ok); }
.seg.warn { background: var(--warn); }
.seg.bad { background: var(--bad); }
.cores-compact { display: grid; grid-template-columns: repeat(auto-fill, minmax(10px, 1fr)); gap: 4px; margin-top: var(--space-4); }
.vbar { height: 40px; border-radius: 2px; background: var(--track); display: flex; align-items: flex-end; overflow: hidden; }
.vbar span { width: 100%; background: var(--ok); }
.vbar span.warn { background: var(--warn); }
.vbar span.bad { background: var(--bad); }
.foot { display: flex; align-items: flex-end; gap: var(--space-3); margin-top: var(--space-4); }
.facts { flex: 1; display: grid; grid-template-columns: repeat(auto-fill, minmax(96px, 1fr)); gap: var(--space-3); }
.facts dd { font-size: var(--font-lg); line-height: var(--line-lg); }
.facts .load { grid-column: span 2; }
.load-rings { width: 52px; height: 52px; flex: none; }

/* 内存、网络：左侧数值、右侧环 */
.row-block { display: flex; align-items: center; align-content: flex-start; gap: var(--space-3); flex-wrap: wrap; }
.grow { flex: 1; grid-template-columns: repeat(auto-fill, minmax(80px, 1fr)); }

/* 磁盘 */
.disk-head { display: flex; align-items: center; gap: var(--space-3); }
.disk-name { flex: 1; min-width: 0; }
.mount { font-size: var(--font-lg); line-height: var(--line-lg); font-family: var(--font-mono); font-weight: var(--weight-strong); }
.disk-cap { text-align: right; }
.pill { width: 18px; height: 40px; border-radius: 5px; background: var(--track); display: flex; align-items: flex-end; overflow: hidden; flex: none; }
.pill span { width: 100%; background: var(--ok); }
.pill span.warn { background: var(--warn); }
.pill span.bad { background: var(--bad); }
.io { width: 100%; border-collapse: collapse; margin-top: var(--space-3); padding-top: var(--space-3); border-top: 1px solid var(--border); font-size: var(--font-sm); }
.io th, .io td { padding: 3px var(--space-2) 3px 0; text-align: right; font-weight: var(--weight-regular); }
.io thead th { color: var(--text-muted); font-size: var(--font-xs); font-family: var(--font-family); padding-top: var(--space-3); }
.io tbody th { color: var(--text-muted); text-align: left; width: 20px; }
.io .await { vertical-align: middle; }
.util { margin: var(--space-1) 0 0; text-align: right; }

/* 网络 */
.speeds { display: flex; gap: var(--space-6); flex: 1; }
.totals { display: flex; flex-direction: column; gap: var(--space-1); }
.totals div { display: flex; align-items: center; gap: var(--space-2); }
.conns { flex-basis: 100%; margin: 0; display: flex; gap: var(--space-2); flex-wrap: wrap; }
.ifaces { margin-left: auto; }

@media (max-width: 600px) {
  .live { grid-template-columns: 1fr; }
  .hero { font-size: 36px; line-height: 40px; }
  .parts { min-width: 0; flex-basis: 100%; }
  .core-row { gap: 2px; }
}
</style>
