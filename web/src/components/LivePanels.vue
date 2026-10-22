<script setup lang="ts">
// 节点详情的详细指标块（设计 11.1、41.6）：关注的进程 → 监听端口 → 磁盘（每个挂载点一块）。
// CPU、内存、网络与连接在上方的速览卡片中（修订第 182 条：恢复速览卡片，这里不再重复这三项）。
// 版式规则：标签用中文、次要色、小号；数值用等宽字体，单位缩小一档并用次要色（Qty）。
// IOPS、IO 等待等为可选字段（设计 4.7），旧版 Agent 不上报时对应部分不显示。
import { computed } from 'vue'
import type { DiskInfo, ServerView } from '../api'
import { DASH, fmtBytesShort, fmtPct } from '../format'
import { gaugeLevel, ioForDisk } from '../metrics'
import { exposure, exposureNames, isRisky, serviceName, sortPorts } from '../ports'
import Qty from './Qty.vue'

const props = defineProps<{
  /** 节点；调用方保证 latest 存在且在线 */
  server: ServerView
}>()

const r = computed(() => props.server.latest!)

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
/* 宽屏两列：进程、端口占整行，各磁盘两两并排 */
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
dd :deep(.u), .io :deep(.u), .cap :deep(.u) { font-family: var(--font-family); }


/* 块标题：与详情页其他卡片一致（h3，无图标） */
.title { display: flex; align-items: baseline; gap: var(--space-2); margin: 0 0 var(--space-3); flex-basis: 100%;
  font-size: var(--font-md); line-height: var(--line-md); font-weight: var(--weight-strong); color: var(--text); }
.title-extra { margin-left: auto; display: flex; gap: var(--space-3); font-size: var(--font-sm); font-weight: var(--weight-regular); color: var(--text-muted); }
.title-extra b { color: var(--text); font-family: var(--font-mono); font-weight: var(--weight-regular); }


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

@media (min-width: 900px) {
  .live { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 600px) {
  .block { padding: var(--space-4); }
  dt { font-size: var(--font-xs); letter-spacing: .02em; gap: 4px; }
  dd { font-size: 20px; line-height: 26px; }
  .io th, .io td { padding-left: var(--space-2); }
  .io td { font-size: var(--font-md); }
  .port-list { grid-template-columns: minmax(0, 1fr); }
}
</style>
