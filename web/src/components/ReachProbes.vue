<script setup lang="ts">
// 国内探针（设计 42.2，修订第 155 条）：把位于国内的节点设为探针，它每轮探测其他全部节点的 Ping 与 TCP 端口，
// 用来判断国内能否访问（疑似被墙、端口被封、线路不稳定）。整体保存，随探针的下一份上报下发。
// 每台探针每轮最多 64 项（Agent 上限，含它自己的测速目标）：节点多时只覆盖前面的节点。
import { computed, onMounted, ref } from 'vue'
import { errorText, getReachProbes, putReachProbes, type ReachProbe, type ReachProbes } from '../api'
import SaveState, { saveStatus } from './SaveState.vue'
import { installed } from '../store'

const isps = [
  { key: '', name: '未标明' },
  { key: 'ct', name: '电信' },
  { key: 'cu', name: '联通' },
  { key: 'cm', name: '移动' },
] as const
const statusNames: Record<string, string> = { on: '探测中', off: '本机关闭了探测', unsupported: '等待上报' }

type Row = { server_id: number; isp: ReachProbe['isp']; ports: string; icmp: boolean }
const saved = ref<ReachProbes | null>(null)
const rows = ref<Row[]>([])
const error = ref('')
const saving = ref(false)
const done = ref('')

const toRows = (v: ReachProbes) => v.items.map((p) => ({ server_id: p.server_id, isp: p.isp, ports: p.ports.join(', '), icmp: p.icmp }))
async function load() {
  try {
    saved.value = await getReachProbes()
    rows.value = toRows(saved.value)
    error.value = ''
  } catch (e) {
    error.value = errorText(e, '国内探针加载失败')
  }
}
onMounted(load)

// 可选的节点：已安装、还没被选为探针（当前行自己的节点保留）
const options = (r: Row) => installed.value.filter((s) => s.id === r.server_id || !rows.value.some((x) => x.server_id === s.id))
function add() {
  const free = installed.value.find((s) => !rows.value.some((x) => x.server_id === s.id))
  if (free) rows.value.push({ server_id: free.id, isp: '', ports: '22, 443', icmp: true })
}
const parsePorts = (v: string) => v.split(/[\s,，、]+/).filter(Boolean).map(Number)
const dirty = computed(() => saved.value && JSON.stringify(toRows(saved.value)) !== JSON.stringify(rows.value))
const savedInfo = (id: number) => saved.value?.items.find((p) => p.server_id === id)

async function save() {
  saving.value = true
  error.value = ''
  done.value = ''
  try {
    saved.value = await putReachProbes(rows.value.map((r) => ({ server_id: r.server_id, isp: r.isp, ports: parsePorts(r.ports), icmp: r.icmp })))
    rows.value = toRows(saved.value)
    done.value = '已保存'
  } catch (e) {
    error.value = errorText(e, '保存失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section id="reach" class="panel reach">
    <div class="head">
      <h3>国内探针</h3>
      <span class="small muted">判断国内能否访问各节点（疑似被墙、端口被封、线路不稳定）</span>
    </div>
    <p class="small muted intro" title="探测其他全部节点注册时的公网 IPv4 / IPv6：Ping（Agent 需要 CAP_NET_RAW）与最多 3 个 TCP 端口；不同运营商各设一台时可分别判断">
      把一台国内节点设为探针，它每轮探测其他节点；连续 3 轮失败才判定，结果在节点详情的“国内可达性”中。
    </p>
    <p v-if="error" class="small bad">{{ error }}</p>
    <div v-if="rows.length" class="rows">
      <div v-for="(r, i) in rows" :key="i" class="row">
        <label class="small">节点
          <select v-model.number="r.server_id">
            <option v-for="s in options(r)" :key="s.id" :value="s.id">{{ s.name }}</option>
          </select>
        </label>
        <label class="small">运营商
          <select v-model="r.isp">
            <option v-for="x in isps" :key="x.key" :value="x.key">{{ x.name }}</option>
          </select>
        </label>
        <label class="small ports">TCP 端口
          <input v-model="r.ports" inputmode="numeric" placeholder="22, 443" />
        </label>
        <label class="small check"><input v-model="r.icmp" type="checkbox" />Ping</label>
        <span v-if="savedInfo(r.server_id)" class="small muted info">
          {{ statusNames[savedInfo(r.server_id)!.status] }} · {{ savedInfo(r.server_id)!.count }} 项
          <template v-if="savedInfo(r.server_id)!.count >= (saved?.max_checks ?? 64)">（已达上限，后面的节点未覆盖）</template>
          <template v-else-if="savedInfo(r.server_id)!.count === 0">（其他节点还没有公网 IP 可探测）</template>
        </span>
        <button type="button" class="btn text small remove" :aria-label="`移除探针 ${i + 1}`" @click="rows.splice(i, 1)">移除</button>
      </div>
    </div>
    <p v-else-if="saved" class="sc-empty-note small muted">还没有国内探针：点“添加探针”，把一台国内的节点设为探针。</p>
    <div class="actions">
      <button type="button" class="btn secondary" :disabled="rows.length >= 10 || rows.length >= installed.length" @click="add">添加探针</button>
      <button type="button" class="btn" :disabled="saving || !dirty" :title="!dirty ? '没有修改' : undefined" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
      <SaveState :state="saveStatus({ saving, error, dirty: !!dirty, saved: !!done })" :error="error || undefined" />
      <span v-if="done && !dirty" class="small muted">随探针的下一份上报生效</span>
    </div>
  </section>
</template>

<style scoped>
.head { display: flex; align-items: baseline; gap: var(--space-3); flex-wrap: wrap; }
.head h3 { margin: 0; }
.intro { margin: var(--space-2) 0 var(--space-3); max-width: 80ch; }
.rows { display: flex; flex-direction: column; gap: var(--space-2); }
.row { display: flex; flex-wrap: wrap; align-items: flex-end; gap: var(--space-2) var(--space-3); padding: var(--space-3);
  border: 1px solid var(--border); border-radius: var(--radius-sm); }
.row label { display: flex; flex-direction: column; gap: var(--space-1); }
.row select { width: auto; min-width: 8em; }
.ports input { width: 9em; }
.row .check { flex-direction: row; align-items: center; gap: var(--space-1); padding-bottom: var(--space-2); }
.check input { width: auto; }
.info { padding-bottom: var(--space-2); }
.remove { margin-left: auto; }
.actions { display: flex; gap: var(--space-2); align-items: center; margin-top: var(--space-3); flex-wrap: wrap; }
.ok { color: var(--ok); }
.bad { color: var(--bad); }
</style>
