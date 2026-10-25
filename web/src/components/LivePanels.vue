<script setup lang="ts">
// 节点详情的详细指标块（设计 11.1、41.6）：关注的进程 → 监听端口 → 磁盘（所有挂载点合为一块，修订第 195 条）。
// CPU、内存、网络与连接在上方的速览卡片中（修订第 182 条：恢复速览卡片，这里不再重复这三项）。
// 版式规则：标签用中文、次要色、小号；数值用等宽字体，单位缩小一档并用次要色（Qty）。
// IOPS、IO 等待等为可选字段（设计 4.7），旧版 Agent 不上报时对应部分不显示。
import { computed, ref } from 'vue'
import type { DiskInfo, ServerView } from '../api'
import { DASH, fmtBytes, fmtBytesShort, fmtPct } from '../format'
import { t } from '../i18n'
import { gaugeLevel, ioForDisk } from '../metrics'
import { exposure, exposureNames, family, isRisky, publicKind, reachNames, serviceName, sortPorts } from '../ports'
import { fmtTime } from '../format'
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
// 磁盘口径（修订第 228 条）：总容量、已用（含 root 保留块）、普通用户可用、保留空间；使用率 = 已用 ÷（已用 + 可用），与 df 一致
const reservedOf = (d: DiskInfo) => (d.available ? Math.max(0, d.total - d.used - d.available) : 0)
const hasReserved = computed(() => r.value.disk.some((d) => reservedOf(d) > d.total * 0.005))
const diskTitle = (d: DiskInfo) => [
  t('总容量 {v}', { v: fmtBytes(d.total) }), t('已用 {v}', { v: fmtBytes(d.used) }),
  d.available ? t('可用 {v}', { v: fmtBytes(d.available) }) : '',
  reservedOf(d) ? t('保留 {v}（root 专用）', { v: fmtBytes(reservedOf(d)) }) : '',
  t('使用率 {v}', { v: fmtPct(d.usage) }),
].filter(Boolean).join(' · ')
const dfOpen = ref(false)
const hasAwait = computed(() => ioRows.value.some((x) => x.io.await_ms != null))
const hasUtil = computed(() => ioRows.value.some((x) => x.io.util != null))

// 监听端口（设计 4.9.1）：高风险与公网开放的排在前面
const ports = computed(() => (r.value.ports ? sortPorts(r.value.ports) : null))
// 端口多时默认显示前 10 个（修订第 265 条，第二轮 UI2-014；风险与对外监听的排在前面），其余点“展开全部”
const PORT_LIMIT = 10
const portsOpen = ref(false)
const shownPorts = computed(() => (portsOpen.value || !ports.value ? ports.value ?? [] : ports.value.slice(0, PORT_LIMIT)))
const portCounts = computed(() => {
  const c = { public: 0, private: 0, local: 0 }
  for (const p of ports.value ?? []) c[exposure(p)]++
  return c
})
// 面板检测公网可达的结果（修订第 171 条，只有管理员的详情中有）；监听范围与是否可达分开显示（修订第 227 条）
const reach = computed(() => new Map((props.server.port_exposure?.results ?? []).map((x) => [`${x.proto}/${x.port}`, x])))
const reachOf = (p: { proto: string; port: number }) => reach.value.get(`${p.proto}/${p.port}`)
const scopeText = (p: Parameters<typeof exposure>[0]) =>
  exposure(p) === 'public' ? t(publicKind(p) === 'all' ? '全部地址' : '公网地址') : t(exposureNames[exposure(p)])
const portTitle = (p: Parameters<typeof exposure>[0]) => [
  t('{proto} {port} 监听于 {addrs}', { proto: p.proto.toUpperCase(), port: p.port, addrs: addrText(p.addrs) }),
  t('监听范围：{scope} · {family}', { scope: scopeText(p), family: family(p) }),
  t('外部可达：{v}', { v: reachOf(p) ? t(reachNames[reachOf(p)!.state]) : t('未检测') }),
  isRisky(p) ? t('此类端口通常不应对公网开放，建议改为只监听 127.0.0.1 或用防火墙限制来源') : '',
].filter(Boolean).join('\n')
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
          <span v-if="portCounts.public">{{ t('对外监听') }} <b>{{ portCounts.public }}</b></span>
          <span v-if="portCounts.private">{{ t('内网') }} <b>{{ portCounts.private }}</b></span>
          <span v-if="portCounts.local">{{ t('仅本机') }} <b>{{ portCounts.local }}</b></span>
        </span>
      </h3>
      <p v-if="!ports" class="muted small empty">{{ t('当前 Agent 未上报监听端口，升级 Agent 后显示。') }}</p>
      <p v-else-if="!ports.length" class="muted small empty">{{ t('没有监听中的端口。') }}</p>
      <ul v-else class="port-list">
        <li v-for="p in shownPorts" :key="`${p.proto}/${p.port}`" :class="{ risky: isRisky(p) }" :title="portTitle(p)">
          <span class="port">{{ p.port }}<span class="proto">{{ p.proto.toUpperCase() }}</span></span>
          <!-- 端口备注（修订第 258 条，来自端口基线）优先于常见服务名 -->
          <span class="svc">{{ server.port_baseline?.labels?.[`${p.port}/${p.proto}`] || serviceName(p) || DASH }}<small>{{ p.addrs.join('  ') }}</small></span>
          <span class="exp-col">
            <span class="exp" :class="isRisky(p) ? 'bad' : exposure(p)">{{ isRisky(p) ? t('{scope} · 风险端口', { scope: scopeText(p) }) : scopeText(p) }}</span>
            <small v-if="exposure(p) === 'public' && reachOf(p)" class="reach" :class="reachOf(p)!.state">{{ t(reachNames[reachOf(p)!.state]) }}</small>
          </span>
        </li>
      </ul>
      <button v-if="ports && ports.length > PORT_LIMIT" type="button" class="text small info-btn" :aria-expanded="portsOpen" @click="portsOpen = !portsOpen">
        {{ portsOpen ? t('收起') : t('展开全部 {n} 个', { n: ports.length }) }}</button>
      <p v-if="portCounts.public" class="muted small port-note">
        {{ t('“对外监听”只表示端口绑定在全部地址或公网地址上；能否从公网访问还取决于防火墙与安全组。') }}
        <template v-if="server.port_exposure">{{ t('外部可达为面板 {time} 检测 {target} 的结果。', { time: fmtTime(server.port_exposure.checked_at), target: server.port_exposure.target }) }}</template>
        <template v-else>{{ t('外部可达未检测，可在“端口基线”中从面板检测。') }}</template>
      </p>
    </section>

    <!-- 磁盘（设计 4.6、4.7）：所有挂载点一块（修订第 195 条） -->
    <section v-if="r.disk.length" class="block disk">
      <h3 class="title">
        {{ t('磁盘') }}
        <span v-if="r.disk.length > 1" class="title-extra">{{ t('挂载点') }} <b>{{ r.disk.length }}</b></span>
      </h3>
      <ul class="mounts">
        <li v-for="d in r.disk" :key="d.mount" :title="diskTitle(d)">
          <div class="m-name">
            <span class="mount" :title="d.mount">{{ d.mount }}</span>
            <span class="muted dev">{{ [d.device, d.fstype].filter(Boolean).join(' · ') || DASH }}</span>
          </div>
          <span class="bar" :title="fmtPct(d.usage)"><span :class="gaugeLevel(d.usage, 'disk')" :style="{ width: `${Math.max(2, d.usage)}%` }" /></span>
          <span class="cap"><Qty :text="fmtBytesShort(d.used)" /><span class="slash">/</span><Qty :text="fmtBytesShort(d.total)" /></span>
          <span class="pct" :class="gaugeLevel(d.usage, 'disk') === 'ok' ? '' : gaugeLevel(d.usage, 'disk')">{{ fmtPct(d.usage) }}</span>
        </li>
      </ul>
      <!-- 口径说明默认收起，点 ⓘ 展开（修订第 250 条：手机不依赖悬停） -->
      <button v-if="hasReserved" type="button" class="text small info-btn" :aria-expanded="dfOpen" @click="dfOpen = !dfOpen">ⓘ {{ t('使用率口径') }}</button>
      <p v-if="hasReserved && dfOpen" class="muted small port-note">{{ t('使用率与 df 一致：已用 ÷（已用 + 可用）。root 保留空间不算可用，所以不等于“已用 ÷ 总容量”。') }}
        <template v-for="d in r.disk" :key="d.mount"><br />{{ d.mount }}：{{ diskTitle(d) }}</template></p>
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
      <!-- 手机：每块盘一张小卡片，指标换行排列，不横向滚动（修订第 250 条，UI-009） -->
      <ul v-if="ioRows.length" class="io-cards">
        <li v-for="x in ioRows" :key="x.io.device">
          <div class="io-dev"><span class="mono">{{ x.io.device }}</span><small class="muted">{{ x.mounts.join(' ') }}</small></div>
          <dl>
            <div><dt>{{ t('读') }}</dt><dd><Qty :text="fmtBytesShort(x.io.read_speed, true)" /></dd></div>
            <div><dt>{{ t('写') }}</dt><dd><Qty :text="fmtBytesShort(x.io.write_speed, true)" /></dd></div>
            <div><dt>{{ t('IOPS 读 / 写') }}</dt><dd>{{ iops(x.io.read_iops) }} / {{ iops(x.io.write_iops) }}</dd></div>
            <div v-if="hasAwait"><dt>{{ t('延迟') }}</dt><dd><Qty :v="x.io.await_ms != null ? x.io.await_ms.toFixed(1) : DASH" :u="x.io.await_ms != null ? 'ms' : ''" /></dd></div>
            <div v-if="hasUtil"><dt>{{ t('繁忙') }}</dt><dd>{{ x.io.util != null ? fmtPct(x.io.util) : DASH }}</dd></div>
            <div><dt>{{ t('累计读 / 写') }}</dt><dd><Qty :text="fmtBytesShort(x.io.read_bytes)" /> / <Qty :text="fmtBytesShort(x.io.write_bytes)" /></dd></div>
          </dl>
        </li>
      </ul>
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
.io-cards { display: none; }
.info-btn { margin-top: var(--space-2); padding: var(--space-1) 0; min-height: 32px; color: var(--text-muted); }
@media (max-width: 600px) {
  .io-wrap { display: none; }
  .io-cards { display: flex; flex-direction: column; gap: var(--space-2); list-style: none; margin: var(--space-3) 0 0; padding: var(--space-3) 0 0;
    border-top: 1px solid var(--border); }
  .io-dev { display: flex; align-items: baseline; gap: var(--space-2); min-width: 0; }
  .io-dev small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .io-cards dl { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--space-2) var(--space-3); margin: var(--space-1) 0 0; }
  .io-cards dt { font-size: var(--font-xs); color: var(--text-muted); }
  .io-cards dd { margin: 0; font-family: var(--font-family); font-size: var(--font-md); line-height: var(--line-md); font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere; }
}
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
.exp-col { display: flex; flex-direction: column; align-items: flex-end; gap: 2px; min-width: 0; }
.reach { font-size: var(--font-xs); color: var(--text-muted); white-space: nowrap; }
.reach.open { color: var(--warn); }
.reach.refused, .reach.timeout { color: var(--ok); }
.port-note { margin: var(--space-2) 0 0; }
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
