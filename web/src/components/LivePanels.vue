<script setup lang="ts">
// 节点详情的实时指标块（设计 11.1、41.6，参考 ServerCat）：CPU → 内存 → 网络 → 磁盘，顺序与 App 一致。
// 磁盘可能有多块，网络放在磁盘之前，避免被挤到页面底部。
// 版式规则：标签全大写、次要色、小号（界面字体）；数值用等宽字体（与参考一致），单位缩小一档并用次要色（Qty）；
// 每块“左侧数值、右侧图形”，图形表达占比（环）或分布（每核分段条、竖向容量条）。
// CPU 时间占比、每核、内存缓存、IOPS 等为可选字段（设计 4.4～4.9），旧版 Agent 不上报时对应部分不显示。
import { computed } from 'vue'
import type { DiskInfo, ServerView } from '../api'
import { DASH, fmtBytesShort, fmtPct, uptimeParts } from '../format'
import { cpu, gaugeLevel, ioForDisk, mem, rx, tx } from '../metrics'
import Qty from './Qty.vue'
import Ring, { type RingSegment } from './Ring.vue'

const props = defineProps<{
  /** 节点；调用方保证 latest 存在且在线 */
  server: ServerView
}>()

const r = computed(() => props.server.latest!)
const b = computed(() => r.value.cpu.breakdown)
const cpuLv = computed(() => gaugeLevel(cpu(props.server), 'cpu'))
const pct1 = (v: number) => (Number.isNaN(v) ? DASH : v >= 10 || v === 0 ? String(Math.round(v)) : v.toFixed(1))

// CPU 时间分类（设计 4.4）：steal 持续偏高说明宿主机超售，iowait 偏高说明在等磁盘
const cpuParts = computed(() => {
  const x = b.value
  if (!x) {
    // 旧版 Agent 不上报占比：版式不变，数值显示“—”
    return [
      { key: 'sys', name: 'SYS', value: NaN, color: 'var(--bad)', hint: '内核态（含中断）' },
      { key: 'user', name: 'USER', value: NaN, color: 'var(--ok)', hint: '用户态（含 nice）' },
      { key: 'iowait', name: 'IOWAIT', value: NaN, color: 'var(--series-2)', hint: '等待磁盘；计为空闲' },
      { key: 'steal', name: 'STEAL', value: NaN, color: 'var(--warn)', hint: '被宿主机占用；持续偏高说明超售' },
    ]
  }
  return [
    { key: 'sys', name: 'SYS', value: x.system + x.irq + x.softirq, color: 'var(--bad)', hint: '内核态（含中断）' },
    { key: 'user', name: 'USER', value: x.user + x.nice, color: 'var(--ok)', hint: '用户态（含 nice）' },
    { key: 'iowait', name: 'IOWAIT', value: x.iowait, color: 'var(--series-2)', hint: '等待磁盘；计为空闲' },
    { key: 'steal', name: 'STEAL', value: x.steal, color: 'var(--warn)', hint: '被宿主机占用；持续偏高说明超售' },
  ]
})

// 每核分段条：16 核以内每核一行 40 格（窄竖条）；更多核心时改为紧凑的竖条网格，避免页面过长
const SEGMENTS = 40
// 未上报每核数据时按核心数画空条（最多 16 行），版式与有数据时一致
const perCore = computed(() => r.value.cpu.per_core ?? Array.from({ length: Math.min(16, Math.max(1, r.value.cpu.cores)) }, () => NaN))
const noPerCore = computed(() => !r.value.cpu.per_core?.length)
// 旧版 Agent：缺少 A3 新增的可选字段（设计 4.4～4.9）
const oldAgent = computed(() => !b.value || noPerCore.value || r.value.memory.cached == null || !r.value.conns)
const compactCores = computed(() => perCore.value.length > 16)
const filled = (v: number) => (Number.isNaN(v) ? 0 : Math.round((Math.max(0, Math.min(100, v)) / 100) * SEGMENTS))

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
const loads = computed(() =>
  [r.value.cpu.load1, r.value.cpu.load5, r.value.cpu.load15].filter((v) => v != null).map((v) => v!.toFixed(2)),
)
const up = computed(() => uptimeParts(r.value.system.uptime))
// 空闲：有占比时取 idle + iowait（与使用率口径一致：iowait 计为空闲），否则为 100 − 使用率
const idle = computed(() => (b.value ? b.value.idle + b.value.iowait : Math.max(0, 100 - (cpu(props.server) ?? 0))))

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

// 磁盘：竖向容量条 + 所在磁盘的 IO
const io = (d: DiskInfo) => ioForDisk(d, r.value.disk_io)
const iops = (v: number | undefined) => (v == null ? DASH : v >= 100 ? String(Math.round(v)) : v.toFixed(1))
</script>

<template>
  <div class="live">
    <p v-if="oldAgent" class="old-agent small">
      当前 Agent（{{ r.agent_version || '未知版本' }}）未上报 CPU 占比、每核使用率、内存缓存、连接数等，显示为“—”。升级 Agent 后自动显示。
    </p>
    <!-- CPU -->
    <section class="block cpu">
      <div class="cpu-top">
        <div class="hero-col">
          <div class="hero" :class="cpuLv"><Qty :v="Math.round(cpu(server) ?? 0)" u="%" /></div>
          <div v-if="r.cpu.temp_c" class="temp" :class="{ warn: r.cpu.temp_c >= 75, bad: r.cpu.temp_c >= 90 }" title="CPU 温度">{{ Math.round(r.cpu.temp_c) }}℃</div>
        </div>
        <dl class="parts">
          <div v-for="p in cpuParts" :key="p.key" :title="p.hint">
            <dt><i :style="{ background: p.color }" />{{ p.name }}</dt>
            <dd :class="{ warn: (p.key === 'steal' || p.key === 'iowait') && p.value >= 10 }"><Qty :v="pct1(p.value)" :u="Number.isNaN(p.value) ? '' : '%'" /></dd>
          </div>
        </dl>
      </div>

      <div v-if="perCore.length && !compactCores" class="cores">
        <div v-for="(v, i) in perCore" :key="i" class="core-row" :title="noPerCore ? 'Agent 未上报每核使用率' : `CPU${i}  ${fmtPct(v)}`">
          <span v-for="k in SEGMENTS" :key="k" class="seg" :class="!noPerCore && k <= filled(v) ? gaugeLevel(v, 'cpu') : ''" />
        </div>
      </div>
      <div v-else-if="perCore.length" class="cores-compact" :aria-label="`每核使用率，共 ${perCore.length} 核`">
        <span v-for="(v, i) in perCore" :key="i" class="vbar" :title="`CPU${i}  ${fmtPct(v)}`">
          <span :class="gaugeLevel(v, 'cpu')" :style="{ height: `${Math.max(3, v)}%` }" />
        </span>
      </div>

      <div class="foot">
        <dl class="facts">
          <div><dt>CORES</dt><dd><Qty :v="r.cpu.cores" /></dd></div>
          <div><dt>IDLE</dt><dd><Qty :v="pct1(idle)" u="%" /></dd></div>
          <div><dt>UPTIME</dt><dd><Qty :v="up.v" :u="up.u" /></dd></div>
        </dl>
        <div class="load" :title="`负载 1 / 5 / 15 分钟：${loads.join(' / ')}，核心 ${r.cpu.cores}`">
          <div class="load-text">
            <dt>LOAD</dt>
            <dd class="load-vals">{{ loads.join('/') }}</dd>
          </div>
          <svg class="load-rings" viewBox="0 0 52 52" role="img" :aria-label="`负载 ${loads.join(' / ')}`">
            <g transform="rotate(-90 26 26)" fill="none" stroke-width="5">
              <template v-for="(l, i) in loadRings" :key="i">
                <circle cx="26" cy="26" :r="l.radius" stroke="var(--track)" />
                <circle cx="26" cy="26" :r="l.radius" :stroke="l.color" :stroke-dasharray="l.dash" stroke-linecap="round" />
              </template>
            </g>
          </svg>
        </div>
      </div>
    </section>

    <!-- 内存 -->
    <section class="block row-block">
      <dl class="cols">
        <div><dt><i style="background: var(--track)" />FREE</dt><dd><Qty :text="m.free != null ? fmtBytesShort(m.free) : DASH" /></dd></div>
        <div><dt><i :style="{ background: `var(--${memLv})` }" />USED</dt><dd><Qty :text="fmtBytesShort(m.used)" /></dd></div>
        <div><dt><i style="background: var(--series-2)" />PAGE CACHE</dt><dd><Qty :text="m.cached != null ? fmtBytesShort(cache) : DASH" /></dd></div>
      </dl>
      <Ring :pct="mem(server)" :level="memLv" :segments="memSegments" :size="60" label="内存" />
      <p class="sub-line muted">
        <span>SWAP <template v-if="r.swap.total"><b><Qty :text="fmtBytesShort(r.swap.used)" /></b> / <Qty :text="fmtBytesShort(r.swap.total)" /></template><b v-else>未启用</b></span>
        <span>TOTAL <b><Qty :text="fmtBytesShort(m.total)" /></b></span>
      </p>
    </section>

    <!-- 网络 -->
    <section class="block row-block net">
      <dl class="cols speeds">
        <div><dt>↓ RX</dt><dd><Qty :text="fmtBytesShort(rx(server), true)" /></dd></div>
        <div><dt>↑ TX</dt><dd><Qty :text="fmtBytesShort(tx(server), true)" /></dd></div>
      </dl>
      <dl class="totals" title="开机以来累计（网卡计数，重启后清零）；计费用量见“周期流量”">
        <div><dt>↓<i style="background: var(--accent)" /></dt><dd><Qty :text="fmtBytesShort(totalRx)" /></dd></div>
        <div><dt>↑<i style="background: var(--ok)" /></dt><dd><Qty :text="fmtBytesShort(totalTx)" /></dd></div>
      </dl>
      <Ring v-if="netSegments.length" :pct="undefined" :segments="netSegments" :size="60" label="下行与上行累计比例" />
      <p class="sub-line muted">
        <span>TCP <b>{{ r.conns?.tcp ?? DASH }}</b></span><span>UDP <b>{{ r.conns?.udp ?? DASH }}</b></span>
        <span>TIME_WAIT <b>{{ r.conns?.time_wait ?? DASH }}</b></span>
        <span>PROC <b>{{ r.processes?.total ?? DASH }}</b></span>
      </p>
    </section>

    <!-- 磁盘（设计 4.6、4.7）：每个挂载点一块 -->
    <section v-for="d in r.disk" :key="d.mount" class="block disk">
      <div class="disk-head">
        <div class="disk-name">
          <div class="mount">{{ d.mount }}</div>
          <div class="muted dev">{{ d.device || DASH }}</div>
        </div>
        <div class="disk-cap">
          <div class="muted dev">{{ d.fstype }}</div>
          <div class="cap"><Qty :text="fmtBytesShort(d.used)" /><span class="slash">/</span><Qty :text="fmtBytesShort(d.total)" /></div>
        </div>
        <span class="pill" :title="fmtPct(d.usage)"><span :class="gaugeLevel(d.usage, 'disk')" :style="{ height: `${Math.max(4, d.usage)}%` }" /></span>
      </div>
      <table v-if="io(d)" class="io">
        <thead><tr><th /><th>SPEED</th><th>BYTES</th><th>IOPS</th><th title="每次 IO 的平均耗时（含排队）">WAIT</th></tr></thead>
        <tbody>
          <tr>
            <th>R</th>
            <td><Qty :text="fmtBytesShort(io(d)!.read_speed, true)" /></td>
            <td><Qty :text="fmtBytesShort(io(d)!.read_bytes)" /></td>
            <td><Qty :v="iops(io(d)!.read_iops)" /></td>
            <td rowspan="2" class="wait"><Qty :v="io(d)!.await_ms != null ? io(d)!.await_ms!.toFixed(1) : DASH" :u="io(d)!.await_ms != null ? 'ms' : ''" /></td>
          </tr>
          <tr>
            <th>W</th>
            <td><Qty :text="fmtBytesShort(io(d)!.write_speed, true)" /></td>
            <td><Qty :text="fmtBytesShort(io(d)!.write_bytes)" /></td>
            <td><Qty :v="iops(io(d)!.write_iops)" /></td>
          </tr>
        </tbody>
      </table>
      <p v-if="io(d)?.util != null" class="muted util">{{ io(d)!.device }} · 繁忙 {{ fmtPct(io(d)!.util) }}</p>
    </section>
  </div>
</template>

<style scoped>
/* 宽屏两列：CPU 占左列两行，内存、网络在右列，各磁盘依次排在其后 */
.live { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--space-3); align-items: start; }
.block { background: var(--surface); border: 1px solid var(--border); border-radius: 14px; padding: var(--space-4) var(--space-5); min-width: 0;
  font-variant-numeric: tabular-nums; }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
dl, dd { margin: 0; }
dt { font-size: var(--font-sm); line-height: var(--line-sm); color: var(--text-muted); display: flex; align-items: center; gap: 6px;
  white-space: nowrap; letter-spacing: .04em; }
dt i { width: 5px; height: 11px; border-radius: 3px; display: inline-block; flex: none; }
dd { font-size: 22px; line-height: 28px; white-space: nowrap; font-family: var(--font-mono); letter-spacing: -0.02em; }
dd :deep(.u), .hero :deep(.u), .io :deep(.u), .cap :deep(.u) { font-family: var(--font-family); }


.old-agent { grid-column: 1 / -1; margin: 0; padding: var(--space-2) var(--space-3); border-radius: var(--radius-sm);
  background: var(--surface-2); color: var(--text-muted); }

/* CPU */
.cpu-top { display: flex; align-items: flex-start; gap: var(--space-6); }
.hero-col { flex: none; }
.hero { font-size: 48px; line-height: 52px; font-weight: 300; letter-spacing: -0.03em; }
.hero :deep(.u) { font-size: .4em; }
.hero.ok { color: var(--text); } /* 正常时用主文字色（参考 ServerCat），只有接近或超过阈值才着色 */
.temp { font-size: var(--font-sm); line-height: var(--line-sm); color: var(--ok); font-family: var(--font-mono); }
/* SYS / USER / IOWAIT / STEAL 始终一行（参考 ServerCat）；窄屏缩小字号而不是换行 */
.parts { flex: 1; display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--space-2); padding-top: 4px; min-width: 0; }
.cores { display: flex; flex-direction: column; gap: 6px; margin-top: var(--space-5); }
.core-row { display: grid; grid-template-columns: repeat(40, minmax(0, 1fr)); gap: 4px; }
.seg { height: 13px; border-radius: 99px; background: var(--track); }
.seg.ok { background: var(--ok); }
.seg.warn { background: var(--warn); }
.seg.bad { background: var(--bad); }
.cores-compact { display: grid; grid-template-columns: repeat(auto-fill, minmax(8px, 1fr)); gap: 4px; margin-top: var(--space-5); }
.vbar { height: 40px; border-radius: 3px; background: var(--track); display: flex; align-items: flex-end; overflow: hidden; }
.vbar span { width: 100%; background: var(--ok); }
.vbar span.warn { background: var(--warn); }
.vbar span.bad { background: var(--bad); }
.foot { display: flex; align-items: flex-end; justify-content: space-between; gap: var(--space-3); margin-top: var(--space-5); }
.facts { display: flex; gap: var(--space-6); }
.load { display: flex; align-items: flex-end; gap: var(--space-3); }
.load-text { text-align: right; }
.load-text dt { justify-content: flex-end; }
.load-vals { font-size: var(--font-md); line-height: var(--line-md); color: var(--text-muted); letter-spacing: 0; }
.load-rings { width: 52px; height: 52px; flex: none; }

/* 内存、网络：左侧数值、右侧环 */
.row-block { display: flex; align-items: center; gap: var(--space-4); flex-wrap: wrap; }
.cols { flex: 1; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--space-3); min-width: 0; }
.sub-line { flex-basis: 100%; margin: 0; padding-top: var(--space-3); border-top: 1px solid var(--border);
  display: flex; gap: var(--space-4); flex-wrap: wrap; font-size: var(--font-sm); letter-spacing: .02em; }
.sub-line b { color: var(--text); font-weight: var(--weight-regular); font-family: var(--font-mono); margin-left: 2px; }

/* 磁盘 */
.disk-head { display: flex; align-items: center; gap: var(--space-4); }
.disk-name { flex: 1; min-width: 0; }
.mount { font-family: var(--font-mono); font-size: 22px; line-height: 28px; }
.cap, .io td { font-family: var(--font-mono); }
.dev { font-size: var(--font-md); line-height: var(--line-md); }
.disk-cap { text-align: right; }
.cap { font-size: var(--font-lg); line-height: var(--line-lg); }
.slash { color: var(--text-muted); margin: 0 2px; }
.pill { width: 26px; height: 46px; border-radius: 7px; background: var(--track); display: flex; align-items: flex-end; overflow: hidden; flex: none;
  box-shadow: inset 0 0 0 2px var(--surface-2); }
.pill span { width: 100%; background: var(--ok); }
.pill span.warn { background: var(--warn); }
.pill span.bad { background: var(--bad); }
.io { width: 100%; border-collapse: collapse; margin-top: var(--space-4); border-top: 1px solid var(--border); }
.io th, .io td { padding: 4px 0 4px var(--space-3); text-align: right; font-weight: var(--weight-regular); white-space: nowrap; }
.io thead th { color: var(--text-muted); font-size: var(--font-sm); letter-spacing: .04em; padding-top: var(--space-3); }
.io tbody th { color: var(--text-muted); text-align: left; padding-left: 0; width: 24px; font-size: var(--font-md); }
.io td { font-size: var(--font-lg); line-height: var(--line-lg); }
.io .wait { vertical-align: middle; }
.util { margin: var(--space-2) 0 0; text-align: right; font-size: var(--font-sm); }

/* 网络 */
.totals { display: flex; flex-direction: column; gap: var(--space-1); }
.totals div { display: flex; align-items: center; gap: var(--space-2); }
.totals dt { gap: 4px; }
.totals dd { font-size: var(--font-lg); line-height: var(--line-lg); }
.speeds { grid-template-columns: repeat(2, minmax(0, 1fr)); }

@media (min-width: 900px) {
  .live { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .cpu { grid-row: span 2; }
}
@media (max-width: 600px) {
  .block { padding: var(--space-4); }
  .cpu-top { gap: var(--space-3); }
  .hero { font-size: 40px; line-height: 44px; }
  dt { font-size: var(--font-xs); letter-spacing: .02em; gap: 4px; }
  dd { font-size: 20px; line-height: 26px; }
  .parts dd { font-size: 17px; line-height: 22px; }
  .core-row { gap: 3px; }
  .seg { height: 12px; }
  /* 底部一行：CORES / IDLE / UPTIME 与 LOAD + 负载环（参考 ServerCat） */
  .facts { gap: var(--space-4); }
  .facts dd { font-size: 18px; line-height: 24px; }
  .load { gap: var(--space-2); }
  .load-vals { font-size: var(--font-xs); line-height: var(--line-xs); }
  .load-rings { width: 40px; height: 40px; }
  .io th, .io td { padding-left: var(--space-2); }
  .io td { font-size: var(--font-md); }
}
</style>
