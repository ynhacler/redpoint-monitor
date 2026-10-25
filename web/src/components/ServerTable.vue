<script setup lang="ts">
// 节点表格视图（修订第 239 条，需求说明书 NODE-06 / WEB-02）：电脑上一屏看更多节点，与卡片视图同一份筛选与排序结果。
// 列：状态、名称（国旗、分组）、CPU、内存、磁盘、下行 / 上行、本周期流量、到期、1 分钟负载；数值右对齐、等宽数字。
// 点 CPU / 内存 / 磁盘 / 流量 / 名称的列标题按该列排序（与上方“排序”下拉同一设置）；点整行进入详情。离线时不显示旧数值（设计 43.6）。
import { useRouter } from 'vue-router'
import type { ServerView } from '../api'
import { DASH, fmtBytesShort, fmtDuration, fmtExpiry, fmtPct, fmtTraffic } from '../format'
import { t } from '../i18n'
import { cpu, diskSummary, displayStatus, gaugeLevel, isLive, mem, rx, trafficPct, tx } from '../metrics'
import Flag from './Flag.vue'
import StatusDot from './StatusDot.vue'

defineProps<{
  /** 已筛选、排好序的节点 */
  servers: ServerView[]
  /** 当前排序方式 */
  sortBy: string
}>()
const emit = defineEmits<{
  /** 点列标题排序 */
  sort: [key: string]
}>()

const router = useRouter()
const open = (s: ServerView) => router.push(`/servers/${s.id}`)
const pct = (v: number | undefined) => (v == null ? DASH : fmtPct(v))
// 没有实时数据时的说明：离线多久；节点并非离线（面板刚启动、还没收到这一轮上报）时为“等待上报”
const offline = (s: ServerView) =>
  s.status !== 'offline' && s.last_seen_at ? t('等待上报')
    : s.last_seen_at ? t('离线 {d}', { d: fmtDuration(Date.now() / 1000 - s.last_seen_at) }) : t('尚未上报')
const trafficLevel = (p: number | null | undefined) => (p == null ? '' : p >= 95 ? 'bad' : p >= 80 ? 'warn' : '')
const cols: { key: string; name: string; sort?: string }[] = [
  { key: 'name', name: '名称', sort: 'name' }, { key: 'cpu', name: 'CPU', sort: 'cpu' }, { key: 'mem', name: '内存', sort: 'mem' },
  { key: 'disk', name: '磁盘', sort: 'disk' }, { key: 'net', name: '下行 / 上行' }, { key: 'traffic', name: '本周期流量', sort: 'traffic' },
  { key: 'expire', name: '到期' }, { key: 'load', name: '负载' },
]
</script>

<template>
  <div class="panel table-wrap">
    <table class="servers">
      <thead>
        <tr>
          <th class="st"><span class="sr-only">{{ t('状态') }}</span></th>
          <th v-for="c in cols" :key="c.key" :class="[c.key, { sortable: !!c.sort, sorted: c.sort === sortBy }]"
            :aria-sort="c.sort === sortBy ? (c.sort === 'name' ? 'ascending' : 'descending') : undefined">
            <button v-if="c.sort" type="button" class="th-btn" :title="t('按{name}排序', { name: t(c.name) })" @click="emit('sort', c.sort)">
              {{ t(c.name) }}<span v-if="c.sort === sortBy" aria-hidden="true">{{ c.sort === 'name' ? ' ↑' : ' ↓' }}</span>
            </button>
            <template v-else>{{ t(c.name) }}</template>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="s in servers" :key="s.id" tabindex="0" @click="open(s)" @keydown.enter="open(s)">
          <td class="st"><StatusDot :status="displayStatus(s)" dot-only /></td>
          <td class="name">
            <span class="nm"><Flag :code="s.country" /><RouterLink :to="`/servers/${s.id}`" class="ellipsis" :title="s.name" @click.stop>{{ s.name }}</RouterLink></span>
            <span v-if="s.group" class="muted small">{{ s.group }}</span>
          </td>
          <template v-if="isLive(s)">
            <td class="num cpu" :class="gaugeLevel(cpu(s), 'cpu')">{{ pct(cpu(s)) }}</td>
            <td class="num mem" :class="gaugeLevel(mem(s), 'mem')">{{ pct(mem(s)) }}</td>
            <td class="num disk" :class="gaugeLevel(diskSummary(s)?.usage, 'disk')">{{ pct(diskSummary(s)?.usage) }}</td>
            <td class="num net">{{ fmtBytesShort(rx(s), true) }} / {{ fmtBytesShort(tx(s), true) }}</td>
          </template>
          <td v-else colspan="4" class="muted small offline" :class="{ bad: s.status === 'offline' }">{{ offline(s) }}</td>
          <td class="num traffic">
            <template v-if="s.traffic.limit">
              {{ fmtTraffic(s.traffic.used, s.traffic.unit) }} / {{ fmtTraffic(s.traffic.limit, s.traffic.unit) }}
              <span class="tp" :class="trafficLevel(trafficPct(s))">{{ fmtPct(trafficPct(s)) }}</span>
            </template>
            <template v-else>{{ fmtTraffic(s.traffic.used, s.traffic.unit) }}</template>
          </td>
          <td class="num expire" :class="fmtExpiry(s.expire_date, s.billing_period)?.level"
            :title="fmtExpiry(s.expire_date, s.billing_period)?.title">{{ fmtExpiry(s.expire_date, s.billing_period)?.text ?? DASH }}</td>
          <td class="num load">{{ isLive(s) && s.latest ? s.latest.cpu.load1.toFixed(2) : DASH }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.table-wrap { padding: 0; overflow-x: auto; }
.servers { width: 100%; border-collapse: collapse; font-size: var(--font-md); }
th, td { padding: var(--space-2) var(--space-3); text-align: right; white-space: nowrap; border-bottom: 1px solid var(--border); }
th { font-size: var(--font-sm); font-weight: normal; color: var(--text-muted); background: var(--surface-2); position: sticky; top: 0; }
th.name, td.name, td.offline { text-align: left; }
th.st, td.st { width: 24px; padding-right: 0; }
tbody tr { cursor: pointer; }
tbody tr:hover, tbody tr:focus-visible { background: var(--surface-2); outline: none; }
tbody tr:last-child td { border-bottom: 0; }
td.name { max-width: 280px; }
.nm { display: flex; align-items: center; gap: var(--space-2); min-width: 0; }
.nm a { color: var(--text); font-weight: var(--weight-strong); text-decoration: none; min-width: 0; }
.ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
td.name .small { display: block; }
.num { font-variant-numeric: tabular-nums; }
.th-btn { background: none; border: 0; padding: 0; font: inherit; color: inherit; cursor: pointer; }
th.sorted .th-btn { color: var(--text); font-weight: var(--weight-strong); }
.tp { margin-left: var(--space-2); color: var(--text-muted); }
.ok { color: inherit; }
.warn, .tp.warn { color: var(--warn); }
.bad, .tp.bad { color: var(--bad); }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
</style>
