<script setup lang="ts">
// 节点详情中的 Agent 版本与远程升级（设计 29.13、29.14）。
// 【安全】这里只能选择面板已同步并验签的官方版本；节点上的 Agent 与 updater 会独立验签并拒绝降级。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import {
  ApiError, cancelUpgradeTask, createUpgradeTasks, listReleases, listUpgradeTasks, UnauthorizedError,
  type AgentRelease, type UpgradeTask,
} from '../api'
import { DASH, fmtTime } from '../format'
import { compareVersions } from '../versions'

const props = defineProps<{ serverId: number; current?: string }>()
const emit = defineEmits<{ unauthorized: [] }>()

const latest = ref<AgentRelease | null>(null)
const task = ref<UpgradeTask | null>(null)
const busy = ref(false)
const error = ref('')

const active = computed(() => !!task.value && ['pending', 'delivered', 'staged'].includes(task.value.status))
const canUpgrade = computed(() => {
  if (!latest.value || !props.current || active.value) return false
  const c = compareVersions(props.current, latest.value.version)
  return c !== null && c < 0
})

const statusText: Record<string, string> = {
  pending: '等待 Agent 获取任务（每 5 分钟检查一次）',
  delivered: 'Agent 正在下载并校验',
  staged: '已校验，正在安装',
  success: '升级成功',
  failed: '升级失败，未改动',
  rolled_back: '新版本健康检查未通过，已回滚',
  cancelled: '已取消',
}

let timer: number | undefined
function handle(e: unknown) {
  if (e instanceof UnauthorizedError) emit('unauthorized')
  else error.value = e instanceof ApiError ? e.message : '操作失败'
}

async function load() {
  try {
    const [rels, tasks] = await Promise.all([listReleases(), listUpgradeTasks(props.serverId)])
    latest.value = rels.items.find((r) => r.channel === 'stable') ?? null
    task.value = tasks.items[0] ?? null
  } catch (e) {
    handle(e)
  }
  // 进行中的任务每 10 秒刷新
  if (timer) clearTimeout(timer)
  if (active.value) timer = window.setTimeout(load, 10_000)
}
watch(() => props.serverId, load, { immediate: true })
// 新版本上报后，列表中的版本号会变；顺带刷新任务状态
watch(() => props.current, () => active.value && load())
onBeforeUnmount(() => timer && clearTimeout(timer))

async function upgrade() {
  if (!latest.value) return
  if (!confirm(`将此节点的 Agent 升级到 ${latest.value.version}？\n节点会在 5 分钟内获取任务，独立验签后安装；健康检查失败会自动回滚。`)) return
  busy.value = true
  error.value = ''
  try {
    const r = await createUpgradeTasks([props.serverId], latest.value.version)
    if (!r.created.length) error.value = r.skipped[0]?.reason ?? '未创建任务'
    await load()
  } catch (e) {
    handle(e)
  } finally {
    busy.value = false
  }
}

async function cancel() {
  if (!task.value) return
  busy.value = true
  error.value = ''
  try {
    await cancelUpgradeTask(task.value.id)
    await load()
  } catch (e) {
    handle(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="upgrade">
    <span class="num">{{ current || DASH }}</span>
    <button v-if="canUpgrade" type="button" class="secondary small-btn" :disabled="busy" @click="upgrade">
      升级到 {{ latest!.version }}
    </button>
    <span v-else-if="latest && current && !active && compareVersions(current, latest.version) === 0" class="muted small">已是最新</span>
    <p v-if="task && (active || Date.now() / 1000 - task.updated_at < 86400)" class="small task" :class="task.status">
      {{ task.from_version || '?' }} → {{ task.target_version }}：{{ statusText[task.status] }}
      <template v-if="task.reason && task.status !== 'success'">（{{ task.reason }}）</template>
      <span class="muted">· {{ fmtTime(task.updated_at) }}</span>
      <button v-if="task.status === 'pending' || task.status === 'delivered'" type="button" class="link" :disabled="busy" @click="cancel">取消</button>
    </p>
    <p v-if="error" class="small err">{{ error }}</p>
  </div>
</template>

<style scoped>
.upgrade { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1) var(--space-2); }
.small-btn { padding: var(--space-1) var(--space-2); font-size: var(--font-sm); }
.task { flex-basis: 100%; margin: 0; color: var(--text-muted); }
.task.success { color: var(--ok); }
.task.failed, .task.rolled_back { color: var(--bad); }
.link { background: none; border: 0; padding: 0; color: var(--accent); cursor: pointer; font-size: inherit; margin-left: var(--space-1); }
.err { color: var(--bad); margin: 0; flex-basis: 100%; }
</style>
