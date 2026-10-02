<script setup lang="ts">
// 节点详情的实时区块（设计 11.1、41.6）：CPU → 内存 → 磁盘 → 网络，顺序与 App 一致。
// CPU 时间占比、每核、内存缓存、IOPS、连接数等为可选字段（设计 4.4～4.9），旧版 Agent 不上报时对应部分不显示。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { DASH, fmtBytes, fmtDuration, fmtPct } from '../format'
import { cpu, gaugeLevel, ioForDisk, mem, rx, tx } from '../metrics'
import Ring, { type RingSegment } from './Ring.vue'
import UsageBar from './UsageBar.vue'

const props = defineProps<{
  /** 节点；调用方保证 latest 存在且在线 */
  server: ServerView
}>()

const r = computed(() => props.server.latest!)
const b = computed(() => r.value.cpu.breakdown)

// CPU 时间分类（设计 4.4）：steal 高说明宿主机超售，iowait 高说明在等磁盘，都是 VPS 上最值得注意的两项
const cpuParts = computed(() => {
  const x = b.value
  if (!x) return []
  return [
    { key: 'user', name: '用户', value: x.user + x.nice, color: 'var(--accent)', hint: '用户态（含 nice）' },
    { key: 'system', name: '系统', value: x.system, color: 'var(--series-2)', hint: '内核态' },
    { key: 'iowait', name: 'IO 等待', value: x.iowait, color: 'var(--warn)', hint: '等待磁盘；计为空闲' },
    { key: 'steal', name: '窃取', value: x.steal, color: 'var(--bad)', hint: '被宿主机占用；持续偏高说明超售' },
    { key: 'irq', name: '中断', value: x.irq + x.softirq, color: 'var(--series-3)', hint: '硬中断 + 软中断' },
  ]
})
const fmt1 = (v: number) => (v >= 10 ? Math.round(v) : Math.round(v * 10) / 10) + '%'
const load = computed(() =>
  [r.value.cpu.load1, r.value.cpu.load5, r.value.cpu.load15].filter((v) => v != null).map((v) => v!.toFixed(2)).join(' / '),
)
// 负载相对核心数：超过核心数说明有任务在排队
const loadHigh = computed(() => r.value.cpu.cores > 0 && r.value.cpu.load1 > r.value.cpu.cores)

// 内存：已用（不含缓存）+ 缓存，分段显示（设计 4.5）
const m = computed(() => r.value.memory)
const cache = computed(() => (m.value.buffers ?? 0) + (m.value.cached ?? 0))
const memSegments = computed<RingSegment[] | undefined>(() => {
  if (!m.value.total || m.value.cached == null) return undefined
  const lv = gaugeLevel(mem(props.server), 'mem')
  return [
    { value: m.value.usage, color: `var(--${lv})` },
    { value: (cache.value / m.value.total) * 100, color: 'var(--series-2)' },
  ]
})
const swapPct = computed(() => (r.value.swap.total ? (r.value.swap.used / r.value.swap.total) * 100 : undefined))

// 网络：开机以来累计（与计费周期无关，计费周期见流量卡片）
const totalRx = computed(() => r.value.network.reduce((a, n) => a + n.rx_bytes, 0))
const totalTx = computed(() => r.value.network.reduce((a, n) => a + n.tx_bytes, 0))
const iops = (v: number | undefined) => (v == null ? DASH : v >= 100 ? Math.round(v).toString() : v.toFixed(1))
</script>

<template>
  <div class="live">
    <!-- CPU -->
    <section class="panel cpu">
      <div class="top">
        <h3>CPU</h3>
        <span v-if="r.system.cpu_model" class="muted small ellipsis" :title="r.system.cpu_model">{{ r.system.cpu_model }}</span>
      </div>
      <div class="cpu-main">
        <div class="big num" :class="gaugeLevel(cpu(server), 'cpu')">{{ Math.round(cpu(server) ?? 0) }}<small>%</small></div>
        <dl v-if="cpuParts.length" class="parts">
          <div v-for="p in cpuParts" :key="p.key" :title="p.hint">
            <dt><i :style="{ background: p.color }" />{{ p.name }}</dt>
            <dd class="num" :class="{ warn: (p.key === 'steal' || p.key === 'iowait') && p.value >= 10 }">{{ fmt1(p.value) }}</dd>
          </div>
        </dl>
      </div>
      <div v-if="cpuParts.length" class="stack" aria-hidden="true">
        <span v-for="p in cpuParts" :key="p.key" :style="{ width: `${p.value}%`, background: p.color }" />
      </div>
      <div v-if="r.cpu.per_core?.length" class="cores" :aria-label="`每核使用率，共 ${r.cpu.per_core.length} 核`">
        <div v-for="(v, i) in r.cpu.per_core" :key="i" class="core" :title="`CPU${i}  ${fmtPct(v)}`">
          <span class="core-bar"><span :class="gaugeLevel(v, 'cpu')" :style="{ height: `${Math.max(2, v)}%` }" /></span>
          <span class="core-n num">{{ i }}</span>
        </div>
      </div>
      <dl class="facts">
        <div><dt>核心</dt><dd class="num">{{ r.cpu.cores }}</dd></div>
        <div v-if="b"><dt>空闲</dt><dd class="num">{{ fmt1(b.idle) }}</dd></div>
        <div><dt>负载 1/5/15</dt><dd class="num" :class="{ warn: loadHigh }">{{ load }}</dd></div>
        <div v-if="r.cpu.temp_c"><dt>温度</dt><dd class="num" :class="{ warn: r.cpu.temp_c >= 75, bad: r.cpu.temp_c >= 90 }">{{ r.cpu.temp_c.toFixed(0) }}℃</dd></div>
        <div><dt>运行时间</dt><dd class="num">{{ fmtDuration(r.system.uptime) }}</dd></div>
        <div v-if="r.processes"><dt>进程</dt><dd class="num">{{ r.processes.total }}<span class="muted"> · 运行 {{ r.processes.running }}</span></dd></div>
      </dl>
    </section>

    <!-- 内存 -->
    <section class="panel mem">
      <div class="top"><h3>内存</h3><span class="muted small num">{{ fmtBytes(m.total) }}</span></div>
      <div class="mem-main">
        <dl class="facts">
          <div><dt><i :style="{ background: `var(--${gaugeLevel(mem(server), 'mem')})` }" />已用</dt><dd class="num">{{ fmtBytes(m.used) }}</dd></div>
          <div v-if="m.cached != null"><dt><i style="background: var(--series-2)" />缓存</dt><dd class="num">{{ fmtBytes(cache) }}</dd></div>
          <div v-if="m.free != null"><dt><i style="background: var(--track)" />空闲</dt><dd class="num">{{ fmtBytes(m.free) }}</dd></div>
          <div v-if="m.available != null"><dt>可用</dt><dd class="num">{{ fmtBytes(m.available) }}</dd></div>
        </dl>
        <Ring :pct="mem(server)" :level="gaugeLevel(mem(server), 'mem')" :segments="memSegments" :size="72" label="内存" />
      </div>
      <div v-if="r.swap.total" class="swap small">
        <div class="row-between"><span class="muted">Swap</span><span class="num">{{ fmtBytes(r.swap.used) }} / {{ fmtBytes(r.swap.total) }}</span></div>
        <UsageBar :pct="swapPct ?? 0" />
      </div>
      <p v-else class="muted small swap">未启用 Swap</p>
    </section>

    <!-- 网络 -->
    <section class="panel net">
      <div class="top"><h3>网络</h3><span class="muted small">{{ r.network.map((n) => n.interface).join(' · ') }}</span></div>
      <div class="speeds">
        <div><span class="muted small">↓ 下行</span><span class="num speed">{{ fmtBytes(rx(server), true) }}</span></div>
        <div><span class="muted small">↑ 上行</span><span class="num speed">{{ fmtBytes(tx(server), true) }}</span></div>
      </div>
      <dl class="facts">
        <div title="网卡计数，重启后清零；计费用量见“本周期流量”"><dt>开机以来</dt><dd class="num">↓{{ fmtBytes(totalRx) }} ↑{{ fmtBytes(totalTx) }}</dd></div>
        <template v-if="r.conns">
          <div><dt>TCP</dt><dd class="num">{{ r.conns.tcp }}</dd></div>
          <div><dt>UDP</dt><dd class="num">{{ r.conns.udp }}</dd></div>
          <div title="大量 TIME_WAIT 通常意味着短连接过多"><dt>TIME_WAIT</dt><dd class="num">{{ r.conns.time_wait }}</dd></div>
        </template>
      </dl>
    </section>

    <!-- 磁盘（设计 4.6、4.7） -->
    <section v-if="r.disk.length" class="panel disks">
      <h3>磁盘</h3>
      <div v-for="d in r.disk" :key="d.mount" class="disk">
        <div class="disk-head">
          <span class="mount">{{ d.mount }}</span>
          <span class="muted small">{{ [d.device, d.fstype].filter(Boolean).join(' · ') }}</span>
          <span class="num small disk-num" :class="gaugeLevel(d.usage, 'disk')">{{ fmtBytes(d.used) }} / {{ fmtBytes(d.total) }} · {{ fmtPct(d.usage) }}</span>
        </div>
        <UsageBar :pct="d.usage" />
        <table v-if="ioForDisk(d, r.disk_io)" class="io small num">
          <thead>
            <tr><th />
              <th>速度</th><th>累计</th><th v-if="ioForDisk(d, r.disk_io)!.read_iops != null">IOPS</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <th>读</th><td>{{ fmtBytes(ioForDisk(d, r.disk_io)!.read_speed, true) }}</td>
              <td>{{ fmtBytes(ioForDisk(d, r.disk_io)!.read_bytes) }}</td>
              <td v-if="ioForDisk(d, r.disk_io)!.read_iops != null">{{ iops(ioForDisk(d, r.disk_io)!.read_iops) }}</td>
            </tr>
            <tr>
              <th>写</th><td>{{ fmtBytes(ioForDisk(d, r.disk_io)!.write_speed, true) }}</td>
              <td>{{ fmtBytes(ioForDisk(d, r.disk_io)!.write_bytes) }}</td>
              <td v-if="ioForDisk(d, r.disk_io)!.write_iops != null">{{ iops(ioForDisk(d, r.disk_io)!.write_iops) }}</td>
            </tr>
          </tbody>
          <caption v-if="ioForDisk(d, r.disk_io)!.util != null" class="muted">
            {{ ioForDisk(d, r.disk_io)!.device }} · 繁忙 {{ fmt1(ioForDisk(d, r.disk_io)!.util!) }} · 平均耗时 {{ (ioForDisk(d, r.disk_io)!.await_ms ?? 0).toFixed(1) }} ms
          </caption>
        </table>
      </div>
    </section>
  </div>
</template>

<style scoped>
.live { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: var(--space-3); }
.cpu, .disks { grid-column: 1 / -1; }
.top { display: flex; align-items: baseline; justify-content: space-between; gap: var(--space-3); min-width: 0; }
.ellipsis { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
.ok { color: inherit; }
dl { margin: 0; }
dd { margin: 0; }
dt { font-size: var(--font-xs); line-height: var(--line-xs); color: var(--text-muted); display: flex; align-items: center; gap: var(--space-1); white-space: nowrap; }
dt i { width: 8px; height: 8px; border-radius: 2px; display: inline-block; }

.cpu-main { display: flex; align-items: center; gap: var(--space-6); margin-top: var(--space-3); flex-wrap: wrap; }
.big { font-size: 40px; line-height: 44px; font-weight: var(--weight-strong); }
.big small { font-size: var(--font-lg); color: var(--text-muted); margin-left: 2px; font-weight: var(--weight-regular); }
.parts { display: grid; grid-template-columns: repeat(auto-fill, minmax(76px, 1fr)); gap: var(--space-2) var(--space-4); flex: 1; min-width: 220px; }
.stack { display: flex; height: 6px; border-radius: var(--radius-full); overflow: hidden; background: var(--track); margin-top: var(--space-3); }
.stack span { display: block; height: 100%; }
.cores { display: grid; grid-template-columns: repeat(auto-fill, minmax(22px, 1fr)); gap: var(--space-2) var(--space-1); margin-top: var(--space-4); }
.core { display: flex; flex-direction: column; align-items: center; gap: 2px; }
.core-bar { width: 10px; height: 36px; border-radius: 3px; background: var(--track); display: flex; align-items: flex-end; overflow: hidden; }
.core-bar span { width: 100%; background: var(--ok); }
.core-bar span.warn { background: var(--warn); }
.core-bar span.bad { background: var(--bad); }
.core-n { font-size: 10px; line-height: 12px; color: var(--text-muted); }
.facts { display: grid; grid-template-columns: repeat(auto-fill, minmax(110px, 1fr)); gap: var(--space-3); margin-top: var(--space-4); }

.mem-main { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
.mem-main .facts { flex: 1; grid-template-columns: repeat(2, minmax(0, 1fr)); margin-top: var(--space-3); }
.swap { margin-top: var(--space-4); }
.row-between { display: flex; justify-content: space-between; margin-bottom: var(--space-1); }

.speeds { display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-3); margin-top: var(--space-3); }
.speeds div { display: flex; flex-direction: column; }
.speed { font-size: var(--font-xl); line-height: var(--line-xl); }

.disks { display: flex; flex-direction: column; gap: var(--space-4); }
.disk-head { display: flex; gap: var(--space-3); align-items: baseline; margin-bottom: var(--space-1); flex-wrap: wrap; }
.mount { font-weight: var(--weight-strong); }
.disk-num { margin-left: auto; }
.io { width: 100%; max-width: 480px; border-collapse: collapse; margin-top: var(--space-2); caption-side: bottom; text-align: left; }
.io caption { text-align: left; padding-top: var(--space-1); font-family: var(--font-family); }
.io th, .io td { padding: 2px var(--space-3) 2px 0; font-weight: var(--weight-regular); }
.io thead th { color: var(--text-muted); font-size: var(--font-xs); font-family: var(--font-family); }
.io tbody th { color: var(--text-muted); width: 24px; font-family: var(--font-family); }
@media (max-width: 600px) {
  .live { grid-template-columns: 1fr; }
  .big { font-size: var(--font-num); line-height: var(--line-num); }
}
</style>
