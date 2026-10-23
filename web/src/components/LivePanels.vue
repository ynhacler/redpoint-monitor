<script setup lang="ts">
// 节点详情的详细指标块（设计 11.1、41.6）：关注的进程 → 监听端口 → 磁盘（所有挂载点合为一块，修订第 195 条）。
// CPU、内存、网络与连接在上方的速览卡片中（修订第 182 条：恢复速览卡片，这里不再重复这三项）。
// 版式规则：标签用中文、次要色、小号；数值用等宽字体，单位缩小一档并用次要色（Qty）。
// IOPS、IO 等待等为可选字段（设计 4.7），旧版 Agent 不上报时对应部分不显示。
import { computed } from 'vue'
import type { DiskInfo, ServerView } from '../api'
import { DASH, fmtBytesShort, fmtPct } from '../format'
import { t } from '../i18n'
import { gaugeLevel, ioForDisk } from '../metrics'
import { exposure, exposureNames, isRisky, serviceName, sortPorts } from '../ports'
import Qty from './Qty.vue'

const props = defineProps<{
  /** 节点；调用方保证 latest 存在且在线 */
  server: ServerView
}>()

const r = computed(() => props.server.latest!)

// 磁盘（修订第 195 条）：所有挂载点合为一块——每个挂载点一行容量条；IO 按整块磁盘统计，在下方每块盘一行，
// 并注明这块盘上的挂载点，同一块盘的多个分区不再各占一张卡片、重复说明
const io = (d: DiskInfo) => ioForDisk(d, r.value.disk_io)
const ioRows = computed(() => {
  const rows = new Map<string, { io: NonNullable<ReturnType<typeof io>>; mounts: string[] }>()
  for (const d of r.value.disk) {
    const x = io(d)
    if (!x) continue
    const row = rows.get(x.device)
    if (row) row.mounts.push(d.mount)
    else rows.set(x.device, { io: x, mounts: [d.mount] })
  }
  return [...rows.values()]
})
const hasAwait = computed(() => ioRows.value.some((x) => x.io.await_ms != null))
const hasUtil = computed(() => ioRows.value.some((x) => x.io.util != null))

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
const addrText = (addrs: string[]) => addrs.map((a) => (a === '0.0.0.0' || a === '::' ? t('{addr}（全部）', { addr: a }) : a)).join(t('、'))
const iops = (v: number | undefined) => (v == null ? DASH : v >= 100 ? String(Math.round(v)) : v.toFixed(1))
</script>

<template>
  <div class="live">
    <!-- 关注的进程（设计 33.5.1）：节点设置了才显示 -->
    <section v-if="procs.length" class="block procs">
      <h3 class="title">
        {{ t('关注的进程') }}
        <span class="title-extra">
          <span v-if="procsDown" class="bad">{{ t('未运行') }} <b>{{ procsDown }}</b></span>
          <span v-else-if="procs.every((p) => p.count != null)">{{ t('全部运行中') }}</span>
        </span>
      </h3>
      <ul class="port-list">
        <li v-for="p in procs" :key="p.name" :class="{ risky: p.count === 0 }">
          <span class="svc mono">{{ p.name }}</span>
          <span v-if="p.count == null" class="exp" :title="t('Agent 尚未上报：刚修改设置时等下一次上报；旧版 Agent 需升级')">{{ t('等待上报') }}</span>
          <span v-else-if="p.count === 0" class="exp bad">{{ t('未运行') }}</span>
          <span v-else class="exp private">{{ t('运行中') }} · {{ p.count }}</span>
        </li>
      </ul>
    </section>

    <!-- 监听端口（设计 4.9.1） -->
    <section class="block ports">
      <h3 class="title">
        {{ t('监听端口') }}
        <span v-if="ports?.length" class="title-extra">
          <span v-if="portCounts.public">{{ t('公网') }} <b>{{ portCounts.public }}</b></span>
          <span v-if="portCounts.private">{{ t('内网') }} <b>{{ portCounts.private }}</b></span>
          <span v-if="portCounts.local">{{ t('本机') }} <b>{{ portCounts.local }}</b></span>
        </span>
      </h3>
      <p v-if="!ports" class="muted small empty">{{ t('当前 Agent 未上报监听端口，升级 Agent 后显示。') }}</p>
      <p v-else-if="!ports.length" class="muted small empty">{{ t('没有监听中的端口。') }}</p>
      <ul v-else class="port-list">
        <li v-for="p in ports" :key="`${p.proto}/${p.port}`" :class="{ risky: isRisky(p) }"
          :title="t('{proto} {port} 监听于 {addrs}', { proto: p.proto.toUpperCase(), port: p.port, addrs: addrText(p.addrs) }) + (isRisky(p) ? '\n' + t('此类端口通常不应对公网开放，建议改为只监听 127.0.0.1 或用防火墙限制来源') : '')">
          <span class="port">{{ p.port }}<span class="proto">{{ p.proto.toUpperCase() }}</span></span>
          <span class="svc">{{ serviceName(p) || DASH }}<small>{{ p.addrs.join('  ') }}</small></span>
          <span class="exp" :class="isRisky(p) ? 'bad' : exposure(p)">{{ isRisky(p) ? t('公网 · 风险') : t(exposureNames[exposure(p)]) }}</span>
        </li>
      </ul>
    </section>

    <!-- 磁盘（设计 4.6、4.7）：所有挂载点一块（修订第 195 条） -->
    <section v-if="r.disk.length" class="block disk">
      <h3 class="title">
        {{ t('磁盘') }}
        <span v-if="r.disk.length > 1" class="title-extra">{{ t('挂载点') }} <b>{{ r.disk.length }}</b></span>
      </h3>
      <ul class="mounts">
        <li v-for="d in r.disk" :key="d.mount">
          <div class="m-name">
            <span class="mount" :title="d.mount">{{ d.mount }}</span>
            <span class="muted dev">{{ [d.device, d.fstype].filter(Boolean).join(' · ') || DASH }}</span>
          </div>
          <span class="bar" :title="fmtPct(d.usage)"><span :class="gaugeLevel(d.usage, 'disk')" :style="{ width: `${Math.max(2, d.usage)}%` }" /></span>
          <span class="cap"><Qty :text="fmtBytesShort(d.used)" /><span class="slash">/</span><Qty :text="fmtBytesShort(d.total)" /></span>
          <span class="pct" :class="gaugeLevel(d.usage, 'disk')">{{ fmtPct(d.usage) }}</span>
        </li>
      </ul>
      <div v-if="ioRows.length" class="io-wrap">
        <table class="io">
          <thead>
            <tr>
              <th>{{ t('磁盘 IO') }}</th><th>{{ t('读') }}</th><th>{{ t('写') }}</th><th>{{ t('IOPS 读 / 写') }}</th>
              <th v-if="hasAwait" :title="t('每次 IO 的平均耗时（含排队）')">{{ t('延迟') }}</th><th v-if="hasUtil">{{ t('繁忙') }}</th>
              <th class="total">{{ t('累计读 / 写') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="x in ioRows" :key="x.io.device">
              <th><span class="mono">{{ x.io.device }}</span><small class="muted" :title="x.mounts.join(t('、'))">{{ x.mounts.join(' ') }}</small></th>
              <td><Qty :text="fmtBytesShort(x.io.read_speed, true)" /></td>
              <td><Qty :text="fmtBytesShort(x.io.write_speed, true)" /></td>
              <td>{{ iops(x.io.read_iops) }}<span class="slash">/</span>{{ iops(x.io.write_iops) }}</td>
              <td v-if="hasAwait"><Qty :v="x.io.await_ms != null ? x.io.await_ms.toFixed(1) : DASH" :u="x.io.await_ms != null ? 'ms' : ''" /></td>
              <td v-if="hasUtil">{{ x.io.util != null ? fmtPct(x.io.util) : DASH }}</td>
              <td class="total"><Qty :text="fmtBytesShort(x.io.read_bytes)" /><span class="slash">/</span><Qty :text="fmtBytesShort(x.io.write_bytes)" /></td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>

<style scoped>
/* 宽屏两列：进程、端口、磁盘占整行 */
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


/* 磁盘：整行宽度；每个挂载点一行，IO 每块盘一行（修订第 195 条） */
.disk { grid-column: 1 / -1; }
.mounts { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-1); }
.mounts li { display: grid; grid-template-columns: minmax(120px, 1.2fr) minmax(80px, 2fr) auto 52px; align-items: center; gap: var(--space-4);
  padding: var(--space-2) 0; }
.mounts li + li { border-top: 1px solid var(--border); }
.m-name { display: flex; align-items: baseline; gap: var(--space-2); min-width: 0; }
.mount { font-family: var(--font-mono); font-size: var(--font-lg); line-height: var(--line-lg); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.dev { font-size: var(--font-sm); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.bar { height: 8px; border-radius: 99px; background: var(--track); overflow: hidden; }
.bar span { display: block; height: 100%; border-radius: 99px; background: var(--ok); }
.bar span.warn { background: var(--warn); }
.bar span.bad { background: var(--bad); }
.cap { font-family: var(--font-mono); font-size: var(--font-md); white-space: nowrap; text-align: right; }
.pct { font-family: var(--font-mono); font-size: var(--font-md); text-align: right; color: var(--text-muted); }
.pct.warn { color: var(--warn); }
.pct.bad { color: var(--bad); }
.slash { color: var(--text-muted); margin: 0 2px; }
.io-wrap { overflow-x: auto; margin-top: var(--space-3); border-top: 1px solid var(--border); }
.io { width: 100%; border-collapse: collapse; }
.io th, .io td { padding: 4px 0 4px var(--space-4); text-align: right; font-weight: var(--weight-regular); white-space: nowrap; }
.io thead th { color: var(--text-muted); font-size: var(--font-sm); letter-spacing: .04em; padding-top: var(--space-3); }
.io thead th:first-child, .io tbody th { text-align: left; padding-left: 0; }
.io tbody th { display: flex; align-items: baseline; gap: var(--space-2); }
.io tbody th small { font-size: var(--font-sm); max-width: 220px; overflow: hidden; text-overflow: ellipsis; }
.io td { font-family: var(--font-mono); font-size: var(--font-md); line-height: var(--line-md); }
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
  .mounts li { grid-template-columns: minmax(0, 1fr) auto 44px; row-gap: var(--space-1); column-gap: var(--space-3); }
  .mounts .bar { grid-column: 1 / -1; grid-row: 2; }
  .io th, .io td { padding-left: var(--space-3); }
  .io .total { display: none; }
  .io tbody th small { display: none; }
  .port-list { grid-template-columns: minmax(0, 1fr); }
}
</style>
