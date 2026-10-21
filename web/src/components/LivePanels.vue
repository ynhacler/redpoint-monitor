<script setup lang="ts">
// 节点详情的实时指标块（设计 11.1、41.6，参考 ServerCat）：CPU → 内存 → 网络 → 端口 → 磁盘，顺序与 App 一致。
// 每项指标只出现一次（修订第 177 条）：原速览卡片并入这里，实时网速曲线放在网络块中。
// 磁盘可能有多块，网络与端口放在磁盘之前，避免被挤到页面底部。块标题与其他卡片一致（中文 h3，无图标）。
// 版式规则：标签用中文、次要色、小号；数值用等宽字体，单位缩小一档并用次要色（Qty）；
// 每块“左侧数值、右侧图形”，图形表达占比（环）或分布（每核分段条、竖向容量条）。
// CPU 时间占比、每核、内存缓存、IOPS 等为可选字段（设计 4.4～4.9），旧版 Agent 不上报时对应部分不显示。
import { computed } from 'vue'
import type { DiskInfo, ServerView } from '../api'
import { DASH, fmtBytes, fmtBytesShort, fmtPct, uptimeParts } from '../format'
import { cpu, gaugeLevel, ioForDisk, mem, rx, tx, type LiveSample } from '../metrics'
import { exposure, exposureNames, isRisky, serviceName, sortPorts } from '../ports'
import Chart, { type Series } from './Chart.vue'
import Qty from './Qty.vue'
import Ring, { type RingSegment } from './Ring.vue'

const props = defineProps<{
  /** 节点；调用方保证 latest 存在且在线 */
  server: ServerView
  /** 最近 10 分钟的实时采样（网络块中的网速曲线） */
  samples: LiveSample[]
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
      { key: 'sys', name: '内核', value: NaN, color: 'var(--bad)', hint: '内核态（含中断）' },
      { key: 'user', name: '用户', value: NaN, color: 'var(--ok)', hint: '用户态（含 nice）' },
      { key: 'iowait', name: 'IO 等待', value: NaN, color: 'var(--series-2)', hint: '等待磁盘；计为空闲' },
      { key: 'steal', name: '被抢占', value: NaN, color: 'var(--warn)', hint: '被宿主机占用；持续偏高说明超售' },
    ]
  }
  return [
    { key: 'sys', name: '内核', value: x.system + x.irq + x.softirq, color: 'var(--bad)', hint: '内核态（含中断）' },
    { key: 'user', name: '用户', value: x.user + x.nice, color: 'var(--ok)', hint: '用户态（含 nice）' },
    { key: 'iowait', name: 'IO 等待', value: x.iowait, color: 'var(--series-2)', hint: '等待磁盘；计为空闲' },
    { key: 'steal', name: '被抢占', value: x.steal, color: 'var(--warn)', hint: '被宿主机占用；持续偏高说明超售' },
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

// 网络：当前网速 + 最近 10 分钟曲线（计费用量见“流量”页）
const speedSeries = computed<Series[]>(() => [
  { name: '下行', data: props.samples.map((p) => [p.ts * 1000, p.rx]) },
  { name: '上行', data: props.samples.map((p) => [p.ts * 1000, p.tx]) },
])

// 磁盘：竖向容量条 + 所在磁盘的 IO。IO 按整块磁盘统计，同一块盘的多个分区只在第一个分区显示 IO 表，
// 其余分区注明与哪个挂载点共用，避免同样的数字重复出现
const io = (d: DiskInfo) => ioForDisk(d, r.value.disk_io)
const ioOwner = computed(() => {
  const owner = new Map<string, string>()
  for (const d of r.value.disk) {
    const x = io(d)
    if (x && !owner.has(x.device)) owner.set(x.device, d.mount)
  }
  return owner
})
const sharedWith = (d: DiskInfo) => {
  const x = io(d)
  const o = x ? ioOwner.value.get(x.device) : undefined
  return o && o !== d.mount ? o : ''
}

// 监听端口（设计 4.9.1）：高风险与公网开放的排在前面
const ports = computed(() => (r.value.ports ? sortPorts(r.value.ports) : null))
const portCounts = computed(() => {
  const c = { public: 0, private: 0, local: 0 }
  for (const p of ports.value ?? []) c[exposure(p)]++
  return c
})
// 关注的进程（设计 33.5.1）：按节点设置的顺序列出；Agent 还没上报（刚修改、或旧版 Agent）时计数为 null
const procs = computed(() => {
  const counts = new Map((r.value.watch_procs ?? []).map((p) => [p.name, p.count]))
  return (props.server.watch_procs ?? []).map((name) => ({ name, count: counts.get(name) ?? null }))
})
const procsDown = computed(() => procs.value.filter((p) => p.count === 0).length)
const addrText = (addrs: string[]) => addrs.map((a) => (a === '0.0.0.0' || a === '::' ? `${a}（全部）` : a)).join('、')
const iops = (v: number | undefined) => (v == null ? DASH : v >= 100 ? String(Math.round(v)) : v.toFixed(1))
</script>

<template>
  <div class="live">
    <p v-if="oldAgent" class="old-agent small">
      当前 Agent（{{ r.agent_version || '未知版本' }}）未上报 CPU 占比、每核使用率、内存缓存、连接数等，显示为“—”。升级 Agent 后自动显示。
    </p>
    <!-- CPU -->
    <section class="block cpu">
      <h3 class="title">CPU<span class="title-extra">{{ r.cpu.cores }} 核</span></h3>
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
          <div><dt>空闲</dt><dd><Qty :v="pct1(idle)" u="%" /></dd></div>
          <div><dt>运行</dt><dd><Qty :v="up.v" :u="up.u" /></dd></div>
        </dl>
        <div class="load" :title="`负载 1 / 5 / 15 分钟：${loads.join(' / ')}，核心 ${r.cpu.cores}`">
          <div class="load-text">
            <dt>负载</dt>
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
      <h3 class="title">内存</h3>
      <dl class="cols">
        <div><dt><i style="background: var(--track)" />可用</dt><dd><Qty :text="m.free != null ? fmtBytesShort(m.free) : DASH" /></dd></div>
        <div><dt><i :style="{ background: `var(--${memLv})` }" />已用</dt><dd><Qty :text="fmtBytesShort(m.used)" /></dd></div>
        <div><dt><i style="background: var(--series-2)" />缓存</dt><dd><Qty :text="m.cached != null ? fmtBytesShort(cache) : DASH" /></dd></div>
      </dl>
      <Ring :pct="mem(server)" :level="memLv" :segments="memSegments" :size="60" label="内存" />
      <p class="sub-line muted">
        <span>Swap <template v-if="r.swap.total"><b><Qty :text="fmtBytesShort(r.swap.used)" /></b> / <Qty :text="fmtBytesShort(r.swap.total)" /></template><b v-else>未启用</b></span>
        <span>总量 <b><Qty :text="fmtBytesShort(m.total)" /></b></span>
      </p>
    </section>

    <!-- 网络 -->
    <section class="block row-block net">
      <h3 class="title">网络<span class="title-extra">最近 10 分钟</span></h3>
      <dl class="cols speeds">
        <div><dt><i style="background: var(--accent)" />下行</dt><dd><Qty :text="fmtBytesShort(rx(server), true)" /></dd></div>
        <div><dt><i style="background: var(--ok)" />上行</dt><dd><Qty :text="fmtBytesShort(tx(server), true)" /></dd></div>
      </dl>
      <div class="speed-chart"><Chart title="" :series="speedSeries" :format="(v: number) => fmtBytes(v, true)" hide-legend /></div>
      <p class="sub-line muted">
        <span>TCP <b>{{ r.conns?.tcp ?? DASH }}</b></span><span>UDP <b>{{ r.conns?.udp ?? DASH }}</b></span>
        <span>TIME_WAIT <b>{{ r.conns?.time_wait ?? DASH }}</b></span>
        <span>进程 <b>{{ r.processes?.total ?? DASH }}</b></span>
      </p>
    </section>

    <!-- 关注的进程（设计 33.5.1）：节点设置了才显示 -->
    <section v-if="procs.length" class="block procs">
      <h3 class="title">
        关注的进程
        <span class="title-extra">
          <span v-if="procsDown" class="bad">未运行 <b>{{ procsDown }}</b></span>
          <span v-else-if="procs.every((p) => p.count != null)">全部运行中</span>
        </span>
      </h3>
      <ul class="port-list">
        <li v-for="p in procs" :key="p.name" :class="{ risky: p.count === 0 }">
          <span class="svc mono">{{ p.name }}</span>
          <span v-if="p.count == null" class="exp" title="Agent 尚未上报：刚修改设置时等下一次上报；旧版 Agent 需升级">等待上报</span>
          <span v-else-if="p.count === 0" class="exp bad">未运行</span>
          <span v-else class="exp private">运行中 · {{ p.count }}</span>
        </li>
      </ul>
    </section>

    <!-- 监听端口（设计 4.9.1） -->
    <section class="block ports">
      <h3 class="title">
        监听端口
        <span v-if="ports?.length" class="title-extra">
          <span v-if="portCounts.public">公网 <b>{{ portCounts.public }}</b></span>
          <span v-if="portCounts.private">内网 <b>{{ portCounts.private }}</b></span>
          <span v-if="portCounts.local">本机 <b>{{ portCounts.local }}</b></span>
        </span>
      </h3>
      <p v-if="!ports" class="muted small empty">当前 Agent 未上报监听端口，升级 Agent 后显示。</p>
      <p v-else-if="!ports.length" class="muted small empty">没有监听中的端口。</p>
      <ul v-else class="port-list">
        <li v-for="p in ports" :key="`${p.proto}/${p.port}`" :class="{ risky: isRisky(p) }"
          :title="`${p.proto.toUpperCase()} ${p.port} 监听于 ${addrText(p.addrs)}${isRisky(p) ? '\n此类端口通常不应对公网开放，建议改为只监听 127.0.0.1 或用防火墙限制来源' : ''}`">
          <span class="port">{{ p.port }}<span class="proto">{{ p.proto.toUpperCase() }}</span></span>
          <span class="svc">{{ serviceName(p) || DASH }}<small>{{ p.addrs.join('  ') }}</small></span>
          <span class="exp" :class="isRisky(p) ? 'bad' : exposure(p)">{{ isRisky(p) ? '公网 · 风险' : exposureNames[exposure(p)] }}</span>
        </li>
      </ul>
    </section>

    <!-- 磁盘（设计 4.6、4.7）：每个挂载点一块 -->
    <section v-for="d in r.disk" :key="d.mount" class="block disk">
      <h3 class="title">磁盘</h3>
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
      <p v-if="sharedWith(d)" class="muted shared">与 <span class="mono">{{ sharedWith(d) }}</span> 同在 {{ io(d)!.device }}，IO 见该分区</p>
      <table v-else-if="io(d)" class="io">
        <thead><tr><th /><th>速度</th><th>累计</th><th>IOPS</th><th title="每次 IO 的平均耗时（含排队）">延迟</th></tr></thead>
        <tbody>
          <tr>
            <th>读</th>
            <td><Qty :text="fmtBytesShort(io(d)!.read_speed, true)" /></td>
            <td><Qty :text="fmtBytesShort(io(d)!.read_bytes)" /></td>
            <td><Qty :v="iops(io(d)!.read_iops)" /></td>
            <td rowspan="2" class="wait"><Qty :v="io(d)!.await_ms != null ? io(d)!.await_ms!.toFixed(1) : DASH" :u="io(d)!.await_ms != null ? 'ms' : ''" /></td>
          </tr>
          <tr>
            <th>写</th>
            <td><Qty :text="fmtBytesShort(io(d)!.write_speed, true)" /></td>
            <td><Qty :text="fmtBytesShort(io(d)!.write_bytes)" /></td>
            <td><Qty :v="iops(io(d)!.write_iops)" /></td>
          </tr>
        </tbody>
      </table>
      <p v-if="io(d)?.util != null && !sharedWith(d)" class="muted util">{{ io(d)!.device }} · 繁忙 {{ fmtPct(io(d)!.util) }}</p>
    </section>
  </div>
</template>

<style scoped>
/* 宽屏两列：CPU 占左列两行，内存、网络在右列，各磁盘依次排在其后 */
.live { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--space-3); align-items: start; }
.block { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md); padding: var(--space-4) var(--space-5); min-width: 0;
  font-variant-numeric: tabular-nums; }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
dl, dd { margin: 0; }
dt { font-size: var(--font-sm); line-height: var(--line-sm); color: var(--text-muted); display: flex; align-items: center; gap: 6px;
  white-space: nowrap; letter-spacing: .04em; }
dt i { width: 5px; height: 11px; border-radius: 3px; display: inline-block; flex: none; }
dd { font-size: 22px; line-height: 28px; white-space: nowrap; font-family: var(--font-mono); letter-spacing: -0.02em; }
dd :deep(.u), .hero :deep(.u), .io :deep(.u), .cap :deep(.u) { font-family: var(--font-family); }


/* 块标题：与详情页其他卡片一致（h3，无图标） */
.title { display: flex; align-items: baseline; gap: var(--space-2); margin: 0 0 var(--space-3); flex-basis: 100%;
  font-size: var(--font-md); line-height: var(--line-md); font-weight: var(--weight-strong); color: var(--text); }
.title-extra { margin-left: auto; display: flex; gap: var(--space-3); font-size: var(--font-sm); font-weight: var(--weight-regular); color: var(--text-muted); }
.title-extra b { color: var(--text); font-family: var(--font-mono); font-weight: var(--weight-regular); }

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

.shared { margin: var(--space-4) 0 0; padding-top: var(--space-3); border-top: 1px solid var(--border); font-size: var(--font-sm); }
.mono { font-family: var(--font-mono); color: var(--text); }

/* 端口：整行宽度，端口以小块排列 */
.ports, .procs { grid-column: 1 / -1; }
.procs .port-list { grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); }
.procs .port-list li { grid-template-columns: minmax(0, 1fr) auto; }
.procs .bad { color: var(--bad); }
.empty { margin: 0; }
.port-list { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: var(--space-2); }
.port-list li { display: grid; grid-template-columns: 58px minmax(0, 1fr) auto; align-items: center; gap: var(--space-3);
  padding: var(--space-2) var(--space-3); border-radius: 10px; background: var(--surface-2); }
.port { font-family: var(--font-mono); font-size: var(--font-lg); line-height: var(--line-lg); display: flex; flex-direction: column; }
.proto { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); letter-spacing: .04em; font-family: var(--font-family); }
.svc { font-size: var(--font-sm); min-width: 0; display: flex; flex-direction: column; }
.svc, .svc small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.svc small { font-size: var(--font-xs); color: var(--text-muted); font-family: var(--font-mono); }
.exp { font-size: var(--font-xs); padding: 1px var(--space-2); border-radius: 99px; background: var(--surface); color: var(--text-muted); white-space: nowrap; }
.exp.public { color: var(--accent); }
.exp.private { color: var(--ok); }
.exp.bad { color: var(--bad); font-weight: var(--weight-strong); }
.risky { box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--bad) 45%, transparent); }
.risky .port { color: var(--bad); }

/* 网络 */
.speeds { grid-template-columns: repeat(2, minmax(0, 1fr)); }
/* Chart 自带面板样式，嵌在网络块中时去掉边框与内边距 */
.speed-chart { flex-basis: 100%; min-width: 0; }
.speed-chart :deep(.chart) { border: 0; padding: 0; background: none; }
.speed-chart :deep(.chart h3) { display: none; }

@media (min-width: 900px) {
  .live { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  /* CPU 占左列两行并拉伸到与右列同高，底部一行贴底 */
  .cpu { grid-row: span 2; align-self: stretch; display: flex; flex-direction: column; }
  .cpu .foot { margin-top: auto; padding-top: var(--space-5); }
  .cpu .title { flex-basis: auto; }
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
  /* 底部一行：空闲 / 运行与负载 + 负载环（参考 ServerCat） */
  .facts { gap: var(--space-4); }
  .facts dd { font-size: 18px; line-height: 24px; }
  .load { gap: var(--space-2); }
  .load-vals { font-size: var(--font-xs); line-height: var(--line-xs); }
  .load-rings { width: 40px; height: 40px; }
  .io th, .io td { padding-left: var(--space-2); }
  .io td { font-size: var(--font-md); }
  .port-list { grid-template-columns: minmax(0, 1fr); }
}
</style>
