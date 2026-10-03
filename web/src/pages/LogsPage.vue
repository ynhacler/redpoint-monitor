<script setup lang="ts">
// 日志页（设计 24.8）：登录日志（登录、退出、二次验证）与操作日志（节点、注册码、校准、改密码、Agent 注册等）。
// 审计日志只读，按时间倒序，分页加载；地址中的 tab / result 可刷新、可分享。
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, listAuditLogs, UnauthorizedError, type AuditLog } from '../api'
import EmptyState from '../components/EmptyState.vue'
import { DASH, fmtDateTime } from '../format'
import { logout } from '../store'

const route = useRoute()
const router = useRouter()
const tab = computed(() => (route.query.tab === 'operation' ? 'operation' : 'login'))
const result = computed(() => (route.query.result === 'success' || route.query.result === 'failure' ? route.query.result : ''))
function setQuery(q: Record<string, string>) {
  router.replace({ query: { ...route.query, ...q } })
}

const items = ref<AuditLog[]>([])
const cursor = ref('')
const loading = ref(false)
const error = ref('')
const open = ref<number | null>(null)

async function load(more = false) {
  loading.value = true
  error.value = ''
  try {
    const p = await listAuditLogs({
      category: tab.value, result: result.value || undefined, cursor: more ? cursor.value : undefined, limit: 50,
    })
    items.value = more ? [...items.value, ...p.items] : p.items
    cursor.value = p.next_cursor
  } catch (e) {
    if (e instanceof UnauthorizedError) logout()
    else error.value = e instanceof ApiError ? e.message : '加载失败，请稍后重试'
  } finally {
    loading.value = false
  }
}
watch([tab, result], () => {
  open.value = null
  load()
}, { immediate: true })

// 操作名称（设计 24.8）；未列出的显示原始 action，便于以后新增操作时不丢信息
const actionNames: Record<string, string> = {
  'auth.login': '登录', 'auth.logout': '退出登录', 'auth.reauth': '二次验证密码', 'auth.password_change': '修改密码',
  'server.create': '新建节点', 'server.update': '修改节点', 'server.delete': '删除节点',
  'enroll_code.regenerate': '重新生成注册码', 'enroll_code.revoke': '撤销注册码',
  'agent.enroll': 'Agent 注册', 'agent.unregister': 'Agent 卸载', 'agent_token.revoke': '吊销 Agent Token', 'traffic.calibrate': '校准流量',
  'release.sync': '同步官方版本', 'upgrade_task.create': '创建升级任务', 'upgrade_task.cancel': '取消升级任务',
  'upgrade_task.result': 'Agent 升级结果',
}
const reasonNames: Record<string, string> = {
  captcha: '验证码未通过', unknown_user: '用户名不存在', bad_password: '密码错误', password_too_long: '密码过长',
}

function actor(l: AuditLog) {
  if (l.actor_type === 'agent') return l.actor_id ? `Agent（${l.actor_id}）` : 'Agent'
  if (l.actor_type === 'cli') return '命令行'
  if (l.actor_type === 'system') return '系统'
  return l.actor_id || DASH
}
function target(l: AuditLog) {
  if (l.target_type !== 'server' || !l.target_id) return ''
  return l.target_name || `#${l.target_id}（已删除）`
}
// 一行摘要：登录失败原因、记住登录等；其他细节点开后查看
function summary(l: AuditLog) {
  const d = l.details
  if (typeof d.reason === 'string') return reasonNames[d.reason] ?? d.reason
  if (l.action === 'auth.login' && d.remember) return '记住登录 7 天'
  return ''
}
// User-Agent 粗略识别为 “浏览器 · 系统”，完整内容在详情中
function device(ua: string) {
  if (!ua) return ''
  const b = /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox'
    : /Safari\//.test(ua) ? 'Safari' : /curl|Go-http|vpsmon/i.test(ua) ? ua.split(/[ /]/)[0] : ''
  const o = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS'
    : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  return [b, o].filter(Boolean).join(' · ') || ua.slice(0, 40)
}
const hasDetails = (l: AuditLog) => Object.keys(l.details).length > 0 || !!l.user_agent
</script>

<template>
  <main class="page">
    <div class="head">
      <h1>日志</h1>
      <div class="segmented" role="group" aria-label="日志类型">
        <button type="button" :class="{ active: tab === 'login' }" @click="setQuery({ tab: 'login' })">登录日志</button>
        <button type="button" :class="{ active: tab === 'operation' }" @click="setQuery({ tab: 'operation' })">操作日志</button>
      </div>
      <select :value="result" aria-label="结果" @change="setQuery({ result: ($event.target as HTMLSelectElement).value })">
        <option value="">全部结果</option>
        <option value="success">成功</option>
        <option value="failure">失败</option>
      </select>
    </div>
    <p class="muted small">
      {{ tab === 'login' ? '登录、退出与敏感操作前的密码验证。' : '节点、注册码、流量校准、修改密码与 Agent 注册等操作。' }}
      记录只读，保留 1 年。
    </p>

    <p v-if="error" class="banner">{{ error }}</p>
    <EmptyState v-else-if="!loading && !items.length" text="暂无记录" />

    <ul v-if="items.length" class="panel list">
      <li v-for="l in items" :key="l.id" :class="{ fail: l.result === 'failure' }">
        <button type="button" class="row" :disabled="!hasDetails(l)" :aria-expanded="open === l.id" @click="open = open === l.id ? null : l.id">
          <span class="time num small">{{ fmtDateTime(l.ts) }}</span>
          <span class="result small" :class="l.result === 'success' ? 'ok' : 'bad'">{{ l.result === 'success' ? '成功' : '失败' }}</span>
          <span class="what">
            <strong>{{ actionNames[l.action] ?? l.action }}</strong>
            <span v-if="target(l)"> · {{ target(l) }}</span>
            <span v-if="summary(l)" class="muted"> · {{ summary(l) }}</span>
          </span>
          <span class="who small">{{ actor(l) }}</span>
          <span class="where small muted">{{ l.client_ip || DASH }}<template v-if="device(l.user_agent)"> · {{ device(l.user_agent) }}</template></span>
        </button>
        <dl v-if="open === l.id" class="details small">
          <template v-for="(v, k) in l.details" :key="k">
            <dt>{{ k }}</dt><dd class="num">{{ typeof v === 'object' ? JSON.stringify(v) : v }}</dd>
          </template>
          <template v-if="l.user_agent"><dt>User-Agent</dt><dd>{{ l.user_agent }}</dd></template>
        </dl>
      </li>
    </ul>

    <div v-if="cursor || loading" class="more">
      <button v-if="cursor" type="button" class="secondary" :disabled="loading" @click="load(true)">{{ loading ? '加载中…' : '加载更多' }}</button>
      <span v-else class="muted small">加载中…</span>
    </div>
  </main>
</template>

<style scoped>
.head { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; }
.head h1 { margin: 0 auto 0 0; }
.head select { width: auto; }
.list { list-style: none; margin: var(--space-4) 0 0; padding: 0; }
.list li + li { border-top: 1px solid var(--border); }
.row { all: unset; box-sizing: border-box; width: 100%; cursor: pointer; display: grid; align-items: baseline;
  grid-template-columns: 150px 40px minmax(0, 1fr) 120px 220px; gap: var(--space-3); padding: var(--space-3) var(--space-4); }
.row:disabled { cursor: default; }
.row:not(:disabled):hover { background: var(--surface-2); }
.row:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
.what, .who, .where { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.result.ok { color: var(--ok); }
.result.bad { color: var(--bad); font-weight: var(--weight-strong); }
.details { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--space-1) var(--space-4);
  margin: 0; padding: 0 var(--space-4) var(--space-3); color: var(--text-muted); }
.details dd { margin: 0; overflow-wrap: anywhere; color: var(--text); }
.more { display: flex; justify-content: center; margin-top: var(--space-4); }
@media (max-width: 800px) {
  .row { grid-template-columns: auto auto minmax(0, 1fr); row-gap: var(--space-1); }
  .what { grid-column: 1 / -1; grid-row: 1; white-space: normal; }
  .where { grid-column: 1 / -1; }
}
</style>
