<script setup lang="ts">
// 容器（设计 33.4，修订第 164 条）：Agent 从 cgroup 读到的运行中 Docker / Podman 容器，按 CPU、内存从高到低。
// Agent 不连接 Docker 套接字，拿不到容器名：管理员可以给容器 ID 起名（保存在面板），查看者只看。
import { computed, ref, watch } from 'vue'
import { errorText, putContainerNames, type ServerView } from '../api'
import { fmtBytes, fmtPct } from '../format'
import { isViewer } from '../store'

const props = defineProps<{ server: ServerView }>()
const containers = computed(() => props.server.latest?.containers ?? [])
// 本地副本：保存后立即显示，不必等下一次刷新
const names = ref<Record<string, string>>({ ...(props.server.container_names ?? {}) })

const editing = ref('')
const draft = ref('')
const saving = ref(false)
const error = ref('')
// 面板返回的名称变化时同步（正在编辑时不覆盖输入）
watch(() => props.server.container_names, (v) => { if (!editing.value) names.value = { ...(v ?? {}) } })

function edit(id: string) {
  editing.value = id
  draft.value = names.value[id] ?? ''
  error.value = ''
}

async function save() {
  const id = editing.value
  const next = { ...names.value, [id]: draft.value.trim() }
  if (!next[id]) delete next[id]
  saving.value = true
  try {
    names.value = (await putContainerNames(props.server.id, next)).names
    editing.value = ''
  } catch (e) {
    error.value = errorText(e, '保存失败')
  } finally {
    saving.value = false
  }
}

const memPct = (used: number, limit?: number) => (limit ? Math.min(100, (used / limit) * 100) : null)
</script>

<template>
  <section class="panel containers">
    <div class="head">
      <h3>容器</h3>
      <span class="muted small">{{ containers.length }} 个运行中</span>
    </div>
    <p class="muted small note">
      从 cgroup 读取（不连接 Docker），因此没有容器名、已停止的容器与重启次数；默认显示容器主进程名。{{ isViewer ? '' : '名字按容器 ID 保存，容器重建后需重新命名。' }}
    </p>
    <p v-if="error" class="small bad">{{ error }}</p>
    <ul class="list">
      <li v-for="c in containers" :key="c.id">
        <div class="who">
          <form v-if="editing === c.id" class="rename" @submit.prevent="save">
            <input v-model="draft" maxlength="40" :placeholder="c.command || c.id" aria-label="容器名称" autofocus />
            <button type="submit" class="small" :disabled="saving">保存</button>
            <button type="button" class="text small" @click="editing = ''">取消</button>
          </form>
          <template v-else>
            <!-- 标识：用户起的名字 → 主进程名（容器重建后不变，修订第 165 条）→ 容器 ID -->
            <strong>{{ names[c.id] || c.command || c.id }}</strong>
            <button v-if="!isViewer" type="button" class="text small" @click="edit(c.id)">{{ names[c.id] ? '改名' : '命名' }}</button>
          </template>
          <div class="muted small">
            <span class="num">{{ c.id }}</span><template v-if="names[c.id] && c.command"> · {{ c.command }}</template> · {{ c.runtime === 'podman' ? 'Podman' : 'Docker' }}<template v-if="c.pids"> · {{ c.pids }} 个进程</template>
          </div>
        </div>
        <div class="nums">
          <span class="num" title="占整机 CPU">CPU {{ fmtPct(c.cpu) }}</span>
          <span class="num">内存 {{ fmtBytes(c.memory) }}<span v-if="c.mem_limit" class="muted"> / {{ fmtBytes(c.mem_limit) }}</span></span>
          <span v-if="memPct(c.memory, c.mem_limit) != null" class="bar" :title="fmtPct(memPct(c.memory, c.mem_limit))">
            <i :class="{ warn: memPct(c.memory, c.mem_limit)! >= 80, bad: memPct(c.memory, c.mem_limit)! >= 95 }"
              :style="{ width: `${memPct(c.memory, c.mem_limit)}%` }" />
          </span>
        </div>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.containers { padding: var(--space-4) var(--space-5); }
.head { display: flex; align-items: baseline; gap: var(--space-2); }
.head h3 { margin: 0; }
.note { margin: var(--space-1) 0 0; }
.list { list-style: none; margin: var(--space-3) 0 0; padding: 0; }
.list li { display: flex; justify-content: space-between; align-items: center; gap: var(--space-3); padding: var(--space-3) 0; flex-wrap: wrap; }
.list li + li { border-top: 1px solid var(--border); }
.who { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.who strong { overflow-wrap: anywhere; margin-right: var(--space-2); }
.who > strong + button { align-self: flex-start; padding: 0; }
.rename { display: flex; gap: var(--space-2); align-items: center; flex-wrap: wrap; }
.rename input { width: 180px; max-width: 100%; }
.nums { display: flex; flex-direction: column; align-items: flex-end; gap: 2px; font-size: var(--font-sm); font-variant-numeric: tabular-nums; }
.bar { width: 120px; height: 4px; border-radius: var(--radius-full); background: var(--surface-2); overflow: hidden; }
.bar i { display: block; height: 100%; background: var(--accent); }
.bar i.warn { background: var(--warn); }
.bar i.bad { background: var(--bad); }
.bad { color: var(--bad); }
/* 窄屏：数值统一放在名称下方一行，各行对齐一致 */
@media (max-width: 600px) {
  .nums { flex-basis: 100%; flex-direction: row; flex-wrap: wrap; align-items: center; gap: var(--space-1) var(--space-3); }
}
</style>
