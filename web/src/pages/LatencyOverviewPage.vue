<script setup lang="ts">
// 测速对比（设计 42.3.5，修订第 137 条）：全部节点到各目标最近 1 小时的延迟与丢包放在一张表中，每行一个节点、每列一个目标；
// 每列延迟最低的节点加粗标出，方便比较哪台 VPS 到哪个运营商线路最好。颜色分级与节点详情相同（设计 42.3.3）。每分钟刷新。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { errorText, getLatencyOverview, type LatencyOverview, type ProbeTarget } from '../api'
import EmptyState from '../components/EmptyState.vue'
import { DASH, fmtLatency, fmtPct } from '../format'

type Cell = LatencyOverview['cells'][number]

const data = ref<LatencyOverview | null>(null)
const error = ref('')
const group = ref('')
let timer: ReturnType<typeof setTimeout> | undefined

async function load() {
  clearTimeout(timer)
  try {
    data.value = await getLatencyOverview()
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '测速对比加载失败')
  }
  timer = setTimeout(load, 60_000)
}
onMounted(load)
onBeforeUnmount(() => clearTimeout(timer))

const isps: Record<string, string> = { ct: '电信', cu: '联通', cm: '移动', intl: '' }
const targetName = (t: ProbeTarget) => (t.isp === 'intl' ? `${t.province}${t.note ? ` ${t.note}` : ''}` : `${t.province}${isps[t.isp] ?? ''}`)

const groups = computed(() => [...new Set((data.value?.servers ?? []).map((s) => s.group).filter(Boolean))].sort())
// 只显示有结果的节点（还没测过或已关闭测速的节点不占行）
const servers = computed(() => {
  const withData = new Set((data.value?.cells ?? []).map((c) => c.server_id))
  return (data.value?.servers ?? []).filter((s) => withData.has(s.id) && (!group.value || s.group === group.value))
})
const cells = computed(() => {
  const m = new Map<string, Cell>()
  for (const c of data.value?.cells ?? []) m.set(`${c.server_id}:${c.target_id}`, c)
  return m
})
const cell = (sid: number, tid: number) => cells.value.get(`${sid}:${tid}`)
// 每列延迟最低的节点（只在当前显示的节点中比较）
const best = computed(() => {
  const out = new Map<number, number>()
  for (const t of data.value?.targets ?? []) {
    let min = Infinity
    let who = 0
    for (const s of servers.value) {
      const lat = cell(s.id, t.id)?.latency_ms
      if (lat != null && lat < min) {
        min = lat
        who = s.id
      }
    }
    if (who) out.set(t.id, who)
  }
  return out
})

// 目标无响应（修订第 173 条）：所有有结果的节点都测不通的目标，问题在目标一侧（多为禁了 ICMP 或地址已失效），
// 不是节点线路的问题：这一列显示为灰色“无响应”，不按不可达标红，避免误报
const deadTargets = computed(() => {
  const out = new Set<number>()
  for (const t of data.value?.targets ?? []) {
    const cs = servers.value.map((sv) => cell(sv.id, t.id)).filter((c): c is Cell => !!c)
    // 至少两台节点都测不通才算：只有一台节点时分不清是目标还是线路的问题
    if (cs.length >= 2 && cs.every((c) => c.latency_ms == null)) out.add(t.id)
  }
  return out
})

// 颜色分级（设计 42.3.3）：< 100 ms 好；> 200 ms 或丢包 > 5% 警告；全部失败为不可达（目标无响应时为灰色）
function level(c?: Cell): string {
  if (!c) return ''
  if (c.latency_ms == null) return deadTargets.value.has(c.target_id) ? 'dead' : 'bad'
  if (c.latency_ms > 200 || c.loss > 5) return 'warn'
  if (c.latency_ms < 100) return 'ok'
  return ''
}
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>测速对比</h1>
      <select v-if="groups.length > 1" v-model="group" class="group" aria-label="分组">
        <option value="">全部分组</option>
        <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
      </select>
    </div>
    <p class="muted small intro">
      各节点到每个测试目标最近 1 小时的延迟中位数与丢包率；每列延迟最低的节点加粗。目标在“监测 → 测速设置”中管理，点击节点名查看曲线。
    </p>
    <p v-if="error" class="banner">{{ error }}</p>
    <EmptyState v-else-if="data && (!servers.length || !data.targets.length)"
      text="最近 1 小时还没有测速结果。在“监测 → 测速设置”中添加并启用目标，Agent 约 20 秒后测第一轮。" />
    <div v-else-if="data" class="panel table-wrap">
      <table>
        <thead>
          <tr>
            <th scope="col" class="node">节点</th>
            <th v-for="t in data.targets" :key="t.id" scope="col" :title="`${t.proto === 'icmp' ? 'ICMP' : `TCP ${t.port}`} → ${t.address}`">
              {{ targetName(t) }}
              <small v-if="deadTargets.has(t.id)" class="dead-tag" :title="`所有节点都测不通：目标可能禁止了 ${t.proto === 'icmp' ? 'Ping（ICMP）' : `TCP ${t.port}`}或已失效，不代表线路不通`">目标无响应</small>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="s in servers" :key="s.id">
            <th scope="row" class="node"><RouterLink :to="`/servers/${s.id}`">{{ s.name }}</RouterLink></th>
            <td v-for="t in data.targets" :key="t.id" :class="[level(cell(s.id, t.id)), { best: best.get(t.id) === s.id }]">
              <template v-if="cell(s.id, t.id)">
                <b class="num">{{ cell(s.id, t.id)!.latency_ms == null ? (deadTargets.has(t.id) ? '无响应' : '不可达') : fmtLatency(cell(s.id, t.id)!.latency_ms) }}</b>
                <small>{{ fmtPct(cell(s.id, t.id)!.loss) }}</small>
              </template>
              <span v-else class="muted">{{ DASH }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </main>
</template>

<style scoped>
.group { width: auto; margin-left: auto; }
.intro { margin: 0 0 var(--space-4); max-width: 80ch; }
.table-wrap { overflow-x: auto; padding: 0; }
table { border-collapse: collapse; font-size: var(--font-sm); min-width: 100%; }
th, td { padding: var(--space-2) var(--space-3); text-align: right; white-space: nowrap; border-bottom: 1px solid var(--border); }
thead th { color: var(--text-muted); font-weight: var(--weight-regular); }
.node { text-align: left; position: sticky; left: 0; background: var(--surface); z-index: 1; }
tbody tr:last-child th, tbody tr:last-child td { border-bottom: none; }
td b { font-weight: var(--weight-regular); font-family: var(--font-mono); }
td small { display: block; color: var(--text-muted); font-family: var(--font-mono); }
td.ok b { color: var(--ok); }
td.warn b, td.warn small { color: var(--warn); }
td.bad b { color: var(--bad); }
td.dead b, td.dead small { color: var(--text-muted); }
.dead-tag { display: block; font-weight: var(--weight-regular); color: var(--text-muted); font-size: var(--font-xs); }
td.best b { font-weight: var(--weight-strong); }
td.best { background: var(--surface-2); }
</style>
