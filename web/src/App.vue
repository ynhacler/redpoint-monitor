<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { fmtBytes, getToken, listServers, setToken, UnauthorizedError, type ServerView } from './api'

// 节点列表页（设计 10）。目前只有这一个页面，状态直接用 ref；页面变多后再引入 Router / Pinia（设计 3.3）。
const servers = ref<ServerView[]>([])
const needToken = ref(!getToken()) // 为 true 时显示 Token 输入框而不是列表
const tokenInput = ref('')
const error = ref('') // 连接错误提示；上一次成功的列表继续显示在下方（设计 43.6）
const updatedAt = ref<Date | null>(null)
let timer: number | undefined

// 异常优先（设计 1.5.6）：离线 > 未知 > 在线，同级按名称排序。
// 用户打开页面是为了找出哪里出了问题，所以问题必须排在最前面。
const rank = { offline: 0, unknown: 1, online: 2 } as const
const sorted = computed(() =>
  [...servers.value].sort((a, b) => rank[a.status] - rank[b.status] || a.name.localeCompare(b.name)),
)
// “异常”包含所有不在线的节点，也包括“未知”（上报已延迟）。
const counts = computed(() => ({
  total: servers.value.length,
  online: servers.value.filter((s) => s.status === 'online').length,
  offline: servers.value.filter((s) => s.status !== 'online').length,
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

// 流量条宽度，上限 100%，超额节点不会撑破卡片。
function trafficPct(s: ServerView) {
  return s.traffic.limit > 0 ? Math.min(100, (s.traffic.used / s.traffic.limit) * 100) : 0
}
// 卡片只显示一个总网速：Agent 统计的网卡（默认路由网卡）之和。
const rx = (s: ServerView) => s.latest?.network.reduce((a, n) => a + n.rx_speed, 0) ?? 0
const tx = (s: ServerView) => s.latest?.network.reduce((a, n) => a + n.tx_speed, 0) ?? 0
// 卡片只显示根分区；其他挂载点放在详情页。
const disk = (s: ServerView) => s.latest?.disk.find((d) => d.mount === '/')?.usage ?? 0

onMounted(() => {
  if (!needToken.value) start()
})
onUnmounted(stop)
</script>

<template>
  <main>
    <header>
      <h1>VPS Monitor</h1>
      <p v-if="!needToken" class="summary">
        共 {{ counts.total }} 台 · <span class="ok">在线 {{ counts.online }}</span> ·
        <span :class="{ bad: counts.offline }">异常 {{ counts.offline }}</span>
        <span v-if="updatedAt" class="muted"> · 更新于 {{ updatedAt.toLocaleTimeString() }}</span>
      </p>
    </header>

    <form v-if="needToken" class="token" @submit.prevent="saveToken">
      <label for="tok">管理员 Token（运行 <code>make dev-init</code> 后见 <code>.dev/admin.token</code>）</label>
      <div class="row">
        <input id="tok" v-model="tokenInput" type="password" placeholder="adm_..." autocomplete="off" />
        <button type="submit">进入</button>
      </div>
    </form>

    <p v-if="error" class="banner">{{ error }}</p>

    <section v-if="!needToken" class="grid">
      <article v-for="s in sorted" :key="s.id" class="card" :class="s.status">
        <div class="head">
          <span class="dot" :class="s.status" :aria-label="s.status"></span>
          <h2>{{ s.name }}</h2>
          <span class="muted os">{{ s.latest?.system.os }} {{ s.latest?.system.os_version }}</span>
        </div>

        <!-- 离线时隐藏旧指标：旧数值看起来像实时数据，会误导用户。 -->
        <template v-if="s.latest && s.status !== 'offline'">
          <dl class="metrics">
            <div><dt>CPU</dt><dd>{{ s.latest.cpu.usage.toFixed(0) }}%</dd></div>
            <div><dt>内存</dt><dd>{{ s.latest.memory.usage.toFixed(0) }}%</dd></div>
            <div><dt>磁盘</dt><dd>{{ disk(s).toFixed(0) }}%</dd></div>
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
      <p v-if="!servers.length" class="muted">还没有服务器。运行 <code>make dev-init</code> 和 <code>make dev-agent</code>。</p>
    </section>
  </main>
</template>
