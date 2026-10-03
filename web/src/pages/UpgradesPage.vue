<script setup lang="ts">
// Agent 升级页（设计 29.1、29.14）：官方最新版本、各节点当前版本，批量创建升级任务，最近任务与取消。
// 【安全】只能选择面板已同步并验签的官方版本；节点上的 Agent 与 updater 独立验签并拒绝降级（设计 29.13）。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ApiError, cancelUpgradeTask, createUpgradeTasks, listReleases, listUpgradeTasks, syncReleases, UnauthorizedError,
  type AgentRelease, type ServerView, type UpgradeTask,
} from '../api'
import EmptyState from '../components/EmptyState.vue'
import { DASH, fmtDateTime } from '../format'
import { logout, state } from '../store'
import { compareVersions } from '../versions'

const latest = ref<AgentRelease | null>(null)
const tasks = ref<UpgradeTask[]>([])
const error = ref('')
const notice = ref('')
const busy = ref(false)
const group = ref('')
const selected = ref(new Set<number>())

const statusNames: Record<string, string> = {
  pending: '等待获取', delivered: '下载校验中', staged: '安装中', success: '成功',
  failed: '失败', rolled_back: '已回滚', cancelled: '已取消',
}
const isActive = (t: UpgradeTask) => t.status === 'pending' || t.status === 'delivered' || t.status === 'staged'

function handle(e: unknown) {
  if (e instanceof UnauthorizedError) logout()
  else error.value = e instanceof ApiError ? e.message : '操作失败，请稍后重试'
}

let timer: number | undefined
async function load() {
  try {
    const [rels, list] = await Promise.all([listReleases(), listUpgradeTasks()])
    latest.value = rels.items.find((r) => r.channel === 'stable') ?? null
    tasks.value = list.items
  } catch (e) {
    handle(e)
  }
  if (timer) clearTimeout(timer)
  if (tasks.value.some(isActive)) timer = window.setTimeout(load, 10_000)
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
        <button type="button" :disabled="busy || !chosen.length || !latest" @click="upgrade">
          <template v-if="!chosen.length">选择要升级的节点</template>
          <template v-else>升级 {{ chosen.length }} 个节点<template v-if="latest">到 {{ latest.version }}</template></template>
        </button>
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
@media (max-width: 700px) {
  .row { grid-template-columns: auto minmax(0, 1fr) auto; }
  .row .state { grid-column: 2 / -1; }
  .task { grid-template-columns: auto minmax(0, 1fr); row-gap: var(--space-1); }
  .toolbar button { margin-left: 0; width: 100%; }
}
</style>
