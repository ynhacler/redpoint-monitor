<script setup lang="ts">
// Agent 升级页（设计 29.1、29.14、29.16）：官方最新版本、各节点当前版本，批量升级或灰度升级，最近任务与取消。
// 灰度升级：按列表顺序分批（第一批 N 台 → 第二批累计 P% → 其余），每批结束后观察；失败过多或已升级的节点离线时自动暂停。
// 【安全】只能选择面板已同步并验签的官方版本；节点上的 Agent 与 updater 独立验签并拒绝降级（设计 29.13）。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  cancelUpgradeTask, createRollout, createUpgradeTasks, errorText, listReleases, listRollouts, listUpgradeTasks, rolloutAction, syncReleases,
  type AgentRelease, type ServerView, type UpgradeRollout, type UpgradeTask,
} from '../api'
import EmptyState from '../components/EmptyState.vue'
import { DASH, fmtDateTime } from '../format'
import { state } from '../store'
import { compareVersions } from '../versions'

const latest = ref<AgentRelease | null>(null)
const mirrorOn = ref(false)
const tasks = ref<UpgradeTask[]>([])
const error = ref('')
const notice = ref('')
const busy = ref(false)
const group = ref('')
const selected = ref(new Set<number>())
const rollouts = ref<UpgradeRollout[]>([])

// 升级方式：直接升级全部所选，或灰度升级（设计 29.16）
const mode = ref<'all' | 'canary'>('all')
const firstCount = ref(2)
const secondPercent = ref(20)
const observe = ref(30)
const maxFailures = ref(0)

const rolloutStatus: Record<string, string> = { running: '进行中', paused: '已暂停', completed: '已完成', cancelled: '已取消' }
const rolloutOpen = (r: UpgradeRollout) => r.status === 'running' || r.status === 'paused'

const statusNames: Record<string, string> = {
  pending: '等待获取', delivered: '下载校验中', staged: '安装中', success: '成功',
  failed: '失败', rolled_back: '已回滚', cancelled: '已取消',
}
const isActive = (t: UpgradeTask) => t.status === 'pending' || t.status === 'delivered' || t.status === 'staged'

function handle(e: unknown) {
  error.value = errorText(e)
}

let timer: number | undefined
async function load() {
  try {
    const [rels, list, ros] = await Promise.all([listReleases(), listUpgradeTasks(), listRollouts()])
    latest.value = rels.items.find((r) => r.channel === 'stable') ?? null
    mirrorOn.value = rels.mirror
    tasks.value = list.items
    rollouts.value = ros.items
  } catch (e) {
    handle(e)
  }
  if (timer) clearTimeout(timer)
  if (tasks.value.some(isActive) || rollouts.value.some(rolloutOpen)) timer = window.setTimeout(load, 10_000)
}
onMounted(load)
onBeforeUnmount(() => timer && clearTimeout(timer))

// 每个节点最近一个任务（列表按 id 倒序）
const lastTask = computed(() => {
  const m = new Map<number, UpgradeTask>()
  for (const t of tasks.value) if (!m.has(t.server_id)) m.set(t.server_id, t)
  return m
})

type Row = { s: ServerView; version: string; state: 'old' | 'latest' | 'unknown' | 'busy' }
const rows = computed<Row[]>(() => {
  const out: Row[] = []
  for (const s of state.servers) {
    if (s.status === 'pending') continue
    if (group.value && s.group !== group.value) continue
    const version = s.latest?.agent_version ?? ''
    const t = lastTask.value.get(s.id)
    let st: Row['state'] = 'unknown'
    if (t && isActive(t)) st = 'busy'
    else if (latest.value && version) {
      const c = compareVersions(version, latest.value.version)
      st = c === null ? 'unknown' : c < 0 ? 'old' : 'latest'
    }
    out.push({ s, version, state: st })
  }
  // 可升级的排在前面，其次按名称
  const rank = { old: 0, busy: 1, unknown: 2, latest: 3 }
  return out.sort((a, b) => rank[a.state] - rank[b.state] || a.s.name.localeCompare(b.s.name))
})
const groups = computed(() => [...new Set(state.servers.map((s) => s.group).filter(Boolean))].sort())
const upgradable = computed(() => rows.value.filter((r) => r.state === 'old'))
const chosen = computed(() => upgradable.value.filter((r) => selected.value.has(r.s.id)))

function toggle(id: number) {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}
function selectAll(on: boolean) {
  selected.value = new Set(on ? upgradable.value.map((r) => r.s.id) : [])
}

async function upgrade() {
  if (!latest.value || !chosen.value.length) return
  const n = chosen.value.length
  if (!confirm(`将 ${n} 个节点的 Agent 升级到 ${latest.value.version}？\n节点会在 5 分钟内获取任务，独立验签后安装；健康检查失败会自动回滚。`)) return
  busy.value = true
  error.value = notice.value = ''
  try {
    const r = await createUpgradeTasks(chosen.value.map((x) => x.s.id), latest.value.version)
    notice.value = `已创建 ${r.created.length} 个任务` + (r.skipped.length ? `，跳过 ${r.skipped.length} 个：${r.skipped.map((x) => `${name(x.server_id)}（${x.reason}）`).join('、')}` : '')
    selected.value = new Set()
    await load()
  } catch (e) {
    handle(e)
  } finally {
    busy.value = false
  }
}

const canaryReady = computed(() => mode.value === 'canary' && chosen.value.length >= 2)
const hasOpenRollout = computed(() => rollouts.value.some(rolloutOpen))

async function canary() {
  if (!latest.value || chosen.value.length < 2) return
  const n = chosen.value.length
  if (!confirm(`灰度升级 ${n} 个节点到 ${latest.value.version}？\n第一批 ${firstCount.value} 台，第二批累计 ${secondPercent.value}%，然后其余全部；` +
    `每批结束后观察 ${observe.value} 分钟，失败超过 ${maxFailures.value} 台或已升级的节点离线时自动暂停。`)) return
  busy.value = true
  error.value = notice.value = ''
  try {
    const r = await createRollout({
      server_ids: chosen.value.map((x) => x.s.id), version: latest.value.version,
      stages: [{ count: firstCount.value }, { percent: secondPercent.value }, { percent: 100 }],
      observe_minutes: observe.value, max_failures: maxFailures.value,
    })
    notice.value = `已开始灰度升级：第一批 ${r.progress[0]?.tasks ?? 0} 台` + (r.skipped.length ? `，跳过 ${r.skipped.length} 个` : '')
    selected.value = new Set()
    await load()
  } catch (e) {
    handle(e)
  } finally {
    busy.value = false
  }
}

async function act(r: UpgradeRollout, action: 'pause' | 'resume' | 'cancel') {
  if (action === 'cancel' && !confirm('取消灰度升级？尚未开始安装的任务会一并取消，已升级的节点不受影响。')) return
  if (action === 'resume' && r.reason && !confirm(`继续灰度升级？\n${r.reason}\n继续即表示已确认上述情况，之后只有新的失败或离线才会再次暂停。`)) return
  busy.value = true
  error.value = ''
  try {
    await rolloutAction(r.id, action)
    await load()
  } catch (e) {
    handle(e)
  } finally {
    busy.value = false
  }
}

/** 观察期结束、开始下一批的大致时间 */
function nextAt(r: UpgradeRollout) {
  return r.stage_done_at ? fmtDateTime(r.stage_done_at + r.observe_minutes * 60) : ''
}

async function cancel(t: UpgradeTask) {
  busy.value = true
  error.value = ''
  try {
    await cancelUpgradeTask(t.id)
    await load()
  } catch (e) {
    handle(e)
  } finally {
    busy.value = false
  }
}

async function sync() {
  busy.value = true
  error.value = notice.value = ''
  try {
    const r = await syncReleases()
    notice.value = `已同步官方版本 ${r.version}`
    await load()
  } catch (e) {
    handle(e)
  } finally {
    busy.value = false
  }
}

function name(id: number) {
  return state.servers.find((s) => s.id === id)?.name ?? `#${id}`
}
</script>

<template>
  <main class="page">
    <div class="head">
      <h1>Agent 升级</h1>
      <button type="button" class="secondary" :disabled="busy" @click="sync">同步官方版本</button>
    </div>
    <p class="muted small">
      <template v-if="latest">官方最新稳定版 <b class="num">{{ latest.version }}</b>（签名公钥 {{ latest.key_id }}，同步于 {{ fmtDateTime(latest.synced_at) }}）。</template>
      <template v-else>尚未同步到官方版本，点击“同步官方版本”。</template>
      <template v-if="latest?.mirrored">已镜像到本面板，节点从本面板下载。</template>
      <template v-else-if="latest && mirrorOn">镜像已开启，同步时会一并下载全部构建。</template>
      只能升级到官方签名的更高版本；节点需启用远程升级（新安装默认启用，已安装的执行
      <code>sudo vpsmon-agent enable-remote-upgrade</code>）。
    </p>

    <p v-if="error" class="banner">{{ error }}</p>
    <p v-if="notice" class="notice small">{{ notice }}</p>

    <section class="section">
      <div class="toolbar">
        <label class="check"><input type="checkbox" :checked="upgradable.length > 0 && chosen.length === upgradable.length"
          :disabled="!upgradable.length" @change="selectAll(($event.target as HTMLInputElement).checked)" />全选可升级（{{ upgradable.length }}）</label>
        <select v-if="groups.length" v-model="group" aria-label="分组">
          <option value="">全部分组</option>
          <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
        </select>
        <div class="seg" role="radiogroup" aria-label="升级方式">
          <label :class="{ on: mode === 'all' }"><input v-model="mode" type="radio" value="all" />全部升级</label>
          <label :class="{ on: mode === 'canary' }"><input v-model="mode" type="radio" value="canary" />灰度升级</label>
        </div>
        <button v-if="mode === 'all'" type="button" :disabled="busy || !chosen.length || !latest" @click="upgrade">
          <template v-if="!chosen.length">选择要升级的节点</template>
          <template v-else>升级 {{ chosen.length }} 个节点<template v-if="latest">到 {{ latest.version }}</template></template>
        </button>
        <button v-else type="button" :disabled="busy || !canaryReady || !latest || hasOpenRollout" @click="canary">
          <template v-if="hasOpenRollout">已有进行中的灰度升级</template>
          <template v-else-if="chosen.length < 2">灰度升级至少选择 2 个节点</template>
          <template v-else>灰度升级 {{ chosen.length }} 个节点</template>
        </button>
      </div>
      <div v-if="mode === 'canary'" class="canary panel small">
        <label>第一批<input v-model.number="firstCount" type="number" min="1" max="500" />台</label>
        <label>第二批累计<input v-model.number="secondPercent" type="number" min="1" max="100" />%</label>
        <span class="muted">第三批：其余全部</span>
        <label>每批观察<input v-model.number="observe" type="number" min="5" max="1440" />分钟</label>
        <label>允许失败<input v-model.number="maxFailures" type="number" min="0" max="100" />台</label>
        <p class="muted hint">按列表顺序分批。每批全部结束后观察，失败（含回滚）超过允许台数或已升级的节点离线时自动暂停；不能升级的节点轮到时跳过，由后面的节点补足。</p>
      </div>
      <EmptyState v-if="state.loaded && !rows.length" text="没有已安装 Agent 的节点" />
      <ul v-else class="panel list">
        <li v-for="r in rows" :key="r.s.id">
          <label class="row">
            <input type="checkbox" :disabled="r.state !== 'old'" :checked="selected.has(r.s.id)" @change="toggle(r.s.id)" />
            <span class="name">
              <RouterLink :to="`/servers/${r.s.id}`" @click.stop>{{ r.s.name }}</RouterLink>
              <span v-if="r.s.group" class="muted small"> · {{ r.s.group }}</span>
              <span v-if="r.s.status === 'offline'" class="bad small"> · 离线</span>
            </span>
            <span class="num ver">{{ r.version || DASH }}</span>
            <span class="small state" :class="r.state">
              <template v-if="r.state === 'old'">可升级</template>
              <template v-else-if="r.state === 'latest'">已是最新</template>
              <template v-else-if="r.state === 'busy'">升级中：{{ statusNames[lastTask.get(r.s.id)!.status] }}</template>
              <template v-else>{{ DASH }}</template>
            </span>
          </label>
        </li>
      </ul>
    </section>

    <section v-if="rollouts.length" class="section">
      <h3>灰度升级</h3>
      <ul class="panel list">
        <li v-for="r in rollouts.slice(0, 5)" :key="r.id" class="rollout">
          <div class="rhead">
            <span class="num">→ {{ r.target_version }}</span>
            <span class="small rstatus" :class="r.status">{{ rolloutStatus[r.status] }}</span>
            <span class="muted small">{{ r.total }} 个节点 · 第 {{ r.current_stage }} / {{ r.stages.length }} 批 · {{ fmtDateTime(r.created_at) }}</span>
            <span class="actions">
              <button v-if="r.status === 'running'" type="button" class="link" :disabled="busy" @click="act(r, 'pause')">暂停</button>
              <button v-if="r.status === 'paused'" type="button" class="link" :disabled="busy" @click="act(r, 'resume')">继续</button>
              <button v-if="rolloutOpen(r)" type="button" class="link danger" :disabled="busy" @click="act(r, 'cancel')">取消</button>
            </span>
          </div>
          <ol class="stages small">
            <li v-for="p in r.progress" :key="p.stage" :class="{ cur: p.stage === r.current_stage && rolloutOpen(r) }">
              <span>第 {{ p.stage }} 批</span>
              <span v-if="!p.target && !p.tasks" class="muted">并入上一批</span>
              <span v-else class="num">{{ p.success }}/{{ p.tasks || p.target }}</span>
              <span v-if="p.failed" class="bad num">失败 {{ p.failed }}</span>
              <span v-if="p.active" class="muted num">进行中 {{ p.active }}</span>
            </li>
          </ol>
          <p v-if="r.status === 'running' && r.stage_done_at && r.current_stage < r.stages.length" class="muted small">
            观察中，约 {{ nextAt(r) }} 开始第 {{ r.current_stage + 1 }} 批
          </p>
          <p v-if="r.reason" class="small" :class="r.status === 'paused' ? 'warn' : 'muted'">{{ r.reason }}</p>
          <details v-if="r.skipped.length" class="small">
            <summary class="muted">跳过 {{ r.skipped.length }} 个节点</summary>
            <ul class="skips"><li v-for="k in r.skipped" :key="k.server_id">{{ name(k.server_id) }}：{{ k.reason }}</li></ul>
          </details>
        </li>
      </ul>
    </section>

    <section class="section">
      <h3>最近任务</h3>
      <p v-if="!tasks.length" class="muted small">还没有升级任务。</p>
      <ul v-else class="panel list">
        <li v-for="t in tasks.slice(0, 30)" :key="t.id" class="task">
          <span class="time num small">{{ fmtDateTime(t.created_at) }}</span>
          <span class="name">{{ t.server_name || name(t.server_id) }}</span>
          <span class="num small">{{ t.from_version || '?' }} → {{ t.target_version }}</span>
          <span class="small status" :class="t.status">
            {{ statusNames[t.status] }}<template v-if="t.reason && t.status !== 'success'">：{{ t.reason }}</template>
            <button v-if="t.status === 'pending' || t.status === 'delivered'" type="button" class="link" :disabled="busy" @click="cancel(t)">取消</button>
          </span>
        </li>
      </ul>
    </section>
  </main>
</template>

<style scoped>
.head { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; }
.head h1 { margin: 0 auto 0 0; }
.notice { color: var(--ok); }
.toolbar { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; margin-bottom: var(--space-3); }
.toolbar select { width: auto; }
.toolbar button { margin-left: auto; }
.check { display: inline-flex; align-items: center; gap: var(--space-2); font-size: var(--font-sm); }
.check input, .row input { width: auto; }
.list { list-style: none; margin: 0; padding: 0; }
.list li + li { border-top: 1px solid var(--border); }
.row { display: grid; grid-template-columns: auto minmax(0, 1fr) 160px 160px; align-items: baseline; gap: var(--space-3);
  padding: var(--space-3) var(--space-4); cursor: pointer; }
.name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.state.old { color: var(--warn); }
.state.latest { color: var(--ok); }
.state.busy { color: var(--accent); }
.task { display: grid; grid-template-columns: 150px minmax(0, 1fr) 160px minmax(0, 1.5fr); gap: var(--space-3);
  align-items: baseline; padding: var(--space-3) var(--space-4); }
.status.success { color: var(--ok); }
.status.failed, .status.rolled_back { color: var(--bad); }
.link { background: none; border: 0; padding: 0; color: var(--accent); cursor: pointer; font-size: inherit; margin-left: var(--space-2); }
.bad { color: var(--bad); }
.warn { color: var(--warn); }
.seg { display: inline-flex; border: 1px solid var(--border); border-radius: var(--radius-sm); overflow: hidden; }
.seg label { padding: var(--space-1) var(--space-3); font-size: var(--font-sm); cursor: pointer; }
.seg label.on { background: var(--accent); color: var(--on-accent); }
.seg input { position: absolute; opacity: 0; pointer-events: none; }
.canary { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3) var(--space-4); padding: var(--space-3) var(--space-4);
  margin-bottom: var(--space-3); }
.canary label { display: inline-flex; align-items: center; gap: var(--space-2); }
.canary input { width: 72px; }
.canary .hint { flex-basis: 100%; margin: 0; }
.rollout { padding: var(--space-3) var(--space-4); }
.rhead { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-2) var(--space-3); }
.rhead .actions { margin-left: auto; }
.rstatus.running { color: var(--accent); }
.rstatus.paused { color: var(--warn); }
.rstatus.completed { color: var(--ok); }
.stages { display: flex; flex-wrap: wrap; gap: var(--space-2); list-style: none; margin: var(--space-2) 0 0; padding: 0; }
.stages li { display: inline-flex; gap: var(--space-2); padding: var(--space-1) var(--space-2); border: 1px solid var(--border);
  border-radius: var(--radius-sm); }
.stages li.cur { border-color: var(--accent); }
.rollout p { margin: var(--space-2) 0 0; }
.skips { margin: var(--space-1) 0 0; padding-left: var(--space-5); }
.danger { color: var(--bad); }
@media (max-width: 700px) {
  .row { grid-template-columns: auto minmax(0, 1fr) auto; }
  .row .state { grid-column: 2 / -1; }
  .task { grid-template-columns: auto minmax(0, 1fr); row-gap: var(--space-1); }
  .toolbar button { margin-left: 0; width: 100%; }
}
</style>
