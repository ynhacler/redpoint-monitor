<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { fmtBytes, getToken, listServers, setToken, UnauthorizedError, type ServerView } from './api'

// Single-page server list. State is plain refs; no router/store until there is more than one view.
const servers = ref<ServerView[]>([])
const needToken = ref(!getToken()) // show the token form instead of the list
const tokenInput = ref('')
const error = ref('') // connection banner; the last good list stays visible underneath
const updatedAt = ref<Date | null>(null)
let timer: number | undefined

// Abnormal first (design 1.5.6): offline > unknown > online, then by name.
// The user opens the page to find what is broken, so problems must be at the top.
const rank = { offline: 0, unknown: 1, online: 2 } as const
const sorted = computed(() =>
  [...servers.value].sort((a, b) => rank[a.status] - rank[b.status] || a.name.localeCompare(b.name)),
)
// "异常" counts everything not online, including "unknown" (report is late).
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
    // 401: stop polling, otherwise we would hammer the server with a bad token every 3s.
    // Network errors: keep polling so the page recovers by itself when the server is back.
    if (e instanceof UnauthorizedError) {
      needToken.value = true
      stop()
    } else {
      error.value = '无法连接服务端'
    }
  }
}

// Polling every 3s matches the dev agent interval; the server answers from memory
// (Server.latest), so this does not touch SQLite.
function start() {
  refresh()
  timer = window.setInterval(refresh, 3000) // TODO: switch to WebSocket push
}
function stop() {
  if (timer) window.clearInterval(timer)
}

function saveToken() {
  setToken(tokenInput.value.trim())
  needToken.value = false
  start()
}

// Traffic bar fill, capped at 100% so an over-quota server does not overflow the card.
function trafficPct(s: ServerView) {
  return s.traffic.limit > 0 ? Math.min(100, (s.traffic.used / s.traffic.limit) * 100) : 0
}
// Card shows one total speed: sum over the interfaces the agent counted (default-route NICs).
const rx = (s: ServerView) => s.latest?.network.reduce((a, n) => a + n.rx_speed, 0) ?? 0
const tx = (s: ServerView) => s.latest?.network.reduce((a, n) => a + n.tx_speed, 0) ?? 0
// Root filesystem only on the card; other mounts will go on the detail page.
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

        <!-- Hide stale metrics when offline: old numbers would look live and mislead. -->
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
          <!-- Quota bar turns amber at 80%, red at 95%. -->
          <div v-if="s.traffic.limit" class="bar">
            <div class="fill" :class="{ warn: trafficPct(s) >= 80, crit: trafficPct(s) >= 95 }" :style="{ width: trafficPct(s) + '%' }"></div>
          </div>
        </div>
      </article>
      <p v-if="!servers.length" class="muted">还没有服务器。运行 <code>make dev-init</code> 和 <code>make dev-agent</code>。</p>
    </section>
  </main>
</template>
