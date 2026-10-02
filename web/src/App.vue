<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  fmtBytes, getToken, listServers, setToken, UnauthorizedError,
  type DiskInfo, type EnrollCodeView, type ServerView,
} from './api'
import InstallCommand from './components/InstallCommand.vue'
import ServerForm from './components/ServerForm.vue'

// 节点列表页（设计 10），以及新建节点（设计 27.2）与安装命令页（设计 27.3.4）。
// 页面还少，用一个 page 状态切换；TODO(B): 页面变多后引入 Vue Router / Pinia（设计 3.3，需先确认依赖）。
type Page =
  | { name: 'list' }
  | { name: 'new' }
  | { name: 'edit'; server: ServerView }
  | { name: 'install'; id: number; initial?: EnrollCodeView }
const page = ref<Page>({ name: 'list' })
const servers = ref<ServerView[]>([])
const needToken = ref(!getToken()) // 为 true 时显示 Token 输入框而不是列表
const tokenInput = ref('')
const error = ref('') // 连接错误提示；上一次成功的列表继续显示在下方（设计 43.6）
const updatedAt = ref<Date | null>(null)
let timer: number | undefined

// 异常优先（设计 1.5.6）：离线 > 未知 > 在线，同级按名称排序。
// 用户打开页面是为了找出哪里出了问题，所以问题必须排在最前面。
// 待安装节点单独列在下方，不参与排序与异常统计（设计 27.7）。
const rank = { offline: 0, unknown: 1, online: 2, pending: 3 } as const
const installed = computed(() =>
  servers.value
    .filter((s) => s.status !== 'pending')
    .sort((a, b) => rank[a.status] - rank[b.status] || a.name.localeCompare(b.name)),
)
const pending = computed(() => servers.value.filter((s) => s.status === 'pending'))
// “异常”包含所有不在线的已安装节点，也包括“未知”（上报已延迟）。
const counts = computed(() => ({
  total: installed.value.length,
  online: installed.value.filter((s) => s.status === 'online').length,
  offline: installed.value.filter((s) => s.status !== 'online').length,
}))

async function refresh() {
  try {
    servers.value = await listServers()
    updatedAt.value = new Date()
    error.value = ''
  } catch (e) {
    // 401：停止轮询，否则会每 3 秒用错误的 Token 请求一次面板。
    // 网络错误：继续轮询，面板恢复后页面自动恢复（设计 43.6）。
    if (e instanceof UnauthorizedError) {
      needToken.value = true
      stop()
    } else {
      error.value = '无法连接服务端'
    }
  }
}

// 每 3 秒轮询一次，与开发用 Agent 的上报间隔一致；面板从内存（Server.latest）返回，不读 SQLite。
function start() {
  refresh()
  timer = window.setInterval(refresh, 3000) // TODO(B): 改为 WebSocket 推送（设计 20）
}
function stop() {
  if (timer) window.clearInterval(timer)
}

function saveToken() {
  setToken(tokenInput.value.trim())
  needToken.value = false
  start()
}

// 子页面报告 Token 失效时回到输入框
function onUnauthorized() {
  page.value = { name: 'list' }
  needToken.value = true
  stop()
}

function showList() {
  page.value = { name: 'list' }
  refresh() // 立即刷新，让刚注册的节点马上显示
}

// 流量条宽度，上限 100%，超额节点不会撑破卡片。
function trafficPct(s: ServerView) {
  return s.traffic.limit > 0 ? Math.min(100, (s.traffic.used / s.traffic.limit) * 100) : 0
}
// 卡片只显示一个总网速：Agent 统计的网卡（默认路由网卡）之和。
const rx = (s: ServerView) => s.latest?.network.reduce((a, n) => a + n.rx_speed, 0) ?? 0
const tx = (s: ServerView) => s.latest?.network.reduce((a, n) => a + n.tx_speed, 0) ?? 0
// 多个挂载点时卡片显示使用率最高的一个：异常优先，快满的盘不能被根分区掩盖（设计 1.5.6）
const fullestDisk = (s: ServerView): DiskInfo | undefined =>
  s.latest?.disk.reduce<DiskInfo | undefined>((a, d) => (!a || d.usage > a.usage ? d : a), undefined)
const disk = (s: ServerView) => fullestDisk(s)?.usage ?? 0
// 非根分区时在数值旁标出挂载点；悬停显示全部挂载点
const diskMount = (s: ServerView) => {
  const d = fullestDisk(s)
  return d && d.mount !== '/' ? d.mount : ''
}
const diskTitle = (s: ServerView) =>
  (s.latest?.disk ?? []).map((d) => `${d.mount}  ${d.usage.toFixed(0)}%  ${fmtBytes(d.used)} / ${fmtBytes(d.total)}`).join('\n')

onMounted(() => {
  if (!needToken.value) start()
})
onUnmounted(stop)
</script>

<template>
  <main>
    <header>
      <div class="title">
        <h1>VPS Monitor</h1>
        <button v-if="!needToken && page.name === 'list'" type="button" @click="page = { name: 'new' }">+ 新建节点</button>
      </div>
      <p v-if="!needToken && page.name === 'list'" class="summary">
        共 {{ counts.total }} 台 · <span class="ok">在线 {{ counts.online }}</span> ·
        <span :class="{ bad: counts.offline }">异常 {{ counts.offline }}</span>
        <template v-if="pending.length"> · 待安装 {{ pending.length }}</template>
        <span v-if="updatedAt" class="muted"> · 更新于 {{ updatedAt.toLocaleTimeString() }}</span>
      </p>
    </header>

    <ServerForm
      v-if="!needToken && page.name === 'new'"
      @created="(v) => (page = { name: 'install', id: v.server_id, initial: v })"
      @cancel="showList"
      @unauthorized="onUnauthorized"
    />
    <ServerForm
      v-if="!needToken && page.name === 'edit'"
      :key="page.server.id"
      :server="page.server"
      @done="showList"
      @cancel="showList"
      @unauthorized="onUnauthorized"
    />
    <InstallCommand
      v-if="!needToken && page.name === 'install'"
      :key="page.id"
      :server-id="page.id"
      :initial="page.initial"
      @back="showList"
      @unauthorized="onUnauthorized"
    />

    <form v-if="needToken" class="token" @submit.prevent="saveToken">
      <label for="tok">管理员 Token（运行 <code>make dev-init</code> 后见 <code>.dev/admin.token</code>）</label>
      <div class="row">
        <input id="tok" v-model="tokenInput" type="password" placeholder="adm_..." autocomplete="off" />
        <button type="submit">进入</button>
      </div>
    </form>

    <p v-if="error && page.name === 'list'" class="banner">{{ error }}</p>

    <section v-if="!needToken && page.name === 'list'" class="grid">
      <article v-for="s in installed" :key="s.id" class="card" :class="s.status">
        <div class="head">
          <span class="dot" :class="s.status" :aria-label="s.status"></span>
          <h2>{{ s.name }}</h2>
          <span class="muted os">{{ s.latest?.system.os }} {{ s.latest?.system.os_version }}</span>
          <button type="button" class="link-btn" title="编辑" @click="page = { name: 'edit', server: s }">编辑</button>
        </div>

        <!-- 离线时隐藏旧指标：旧数值看起来像实时数据，会误导用户。 -->
        <template v-if="s.latest && s.status !== 'offline'">
          <dl class="metrics">
            <div><dt>CPU</dt><dd>{{ s.latest.cpu.usage.toFixed(0) }}%</dd></div>
            <div><dt>内存</dt><dd>{{ s.latest.memory.usage.toFixed(0) }}%</dd></div>
            <div :title="diskTitle(s)">
              <dt>{{ diskMount(s) ? `磁盘 ${diskMount(s)}` : '磁盘' }}</dt>
              <dd :class="{ bad: disk(s) >= 90 }">{{ disk(s).toFixed(0) }}%</dd>
            </div>
            <div><dt>网络</dt><dd>↓{{ fmtBytes(rx(s), true) }} ↑{{ fmtBytes(tx(s), true) }}</dd></div>
          </dl>
        </template>
        <p v-else class="muted">离线{{ s.last_seen_at ? '，最后上报 ' + new Date(s.last_seen_at * 1000).toLocaleString() : '，尚未上报' }}</p>

        <div class="traffic">
          <div class="traffic-label">
            <span>本周期流量</span>
            <span>
              {{ fmtBytes(s.traffic.used) }}
              <template v-if="s.traffic.limit"> / {{ fmtBytes(s.traffic.limit) }}</template>
              <template v-else> · 不限</template>
            </span>
          </div>
          <!-- 流量条 ≥80% 变黄，≥95% 变红（设计 41.3 UsageBar）。 -->
          <div v-if="s.traffic.limit" class="bar">
            <div class="fill" :class="{ warn: trafficPct(s) >= 80, crit: trafficPct(s) >= 95 }" :style="{ width: trafficPct(s) + '%' }"></div>
          </div>
        </div>
      </article>
      <p v-if="!servers.length" class="muted">还没有节点。点击右上角“新建节点”添加第一台服务器。</p>
    </section>

    <!-- 待安装节点：单独显示，不触发离线告警，提供安装命令入口（设计 27.7） -->
    <section v-if="!needToken && page.name === 'list' && pending.length" class="pending-list">
      <h3>待安装</h3>
      <div class="grid">
        <article v-for="s in pending" :key="s.id" class="card pending">
          <div class="head">
            <span class="dot pending" aria-label="pending"></span>
            <h2>{{ s.name }}</h2>
            <span class="muted os">{{ [s.provider, s.region, s.group].filter(Boolean).join(' · ') }}</span>
          </div>
          <p class="muted">尚未在主机上安装 Agent。</p>
          <div class="row">
            <button type="button" class="secondary" @click="page = { name: 'install', id: s.id }">查看安装命令</button>
            <button type="button" class="secondary" @click="page = { name: 'edit', server: s }">编辑</button>
          </div>
        </article>
      </div>
    </section>
  </main>
</template>
