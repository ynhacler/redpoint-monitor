<script setup lang="ts">
// 总览（设计 9）：统计卡片 + “需要关注”列表 + Top 5。先看到问题，再看到正常（设计 41.1）。
import { computed } from 'vue'
import type { ServerView } from '../api'
import EmptyState from '../components/EmptyState.vue'
import Icon from '../components/Icon.vue'
import StatusDot from '../components/StatusDot.vue'
import { fmtBytes, fmtPct } from '../format'
import { cpu, fullestDisk, isLive, issues, mem, rx, trafficPct, tx } from '../metrics'
import { installed, pending, state } from '../store'

const online = computed(() => installed.value.filter((s) => s.status === 'online').length)
const offline = computed(() => installed.value.filter((s) => s.status === 'offline').length)
const attention = computed(() =>
  installed.value
    .map((s) => ({ s, problems: issues(s) }))
    .filter((x) => x.problems.length)
    .sort((a, b) => (a.problems[0].level === b.problems[0].level ? 0 : a.problems[0].level === 'bad' ? -1 : 1)),
)
const trafficWarn = computed(() => installed.value.filter((s) => (trafficPct(s) ?? 0) >= 80).length)

// Top 5：只统计在线节点的实时数值（设计 9）
type Top = { title: string; rows: { s: ServerView; v: number; text: string }[] }
const tops = computed<Top[]>(() => {
  const live = installed.value.filter(isLive)
  const top = (title: string, val: (s: ServerView) => number | undefined, fmt: (v: number) => string): Top => ({
    title,
    rows: live
      .map((s) => ({ s, v: val(s) ?? -1 }))
      .filter((r) => r.v >= 0)
      .sort((a, b) => b.v - a.v)
      .slice(0, 5)
      .map((r) => ({ ...r, text: fmt(r.v) })),
  })
  return [
    top('CPU', cpu, fmtPct),
    top('内存', mem, fmtPct),
    top('磁盘', (s) => fullestDisk(s)?.usage, fmtPct),
    top('网速（下行 + 上行）', (s) => (rx(s) ?? 0) + (tx(s) ?? 0), (v) => fmtBytes(v, true)),
  ]
})
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>总览</h1>
      <span v-if="state.updatedAt" class="muted small">更新于 {{ state.updatedAt.toLocaleTimeString() }}</span>
    </div>

    <p v-if="!state.loaded" class="muted">加载中…</p>
    <EmptyState v-else-if="!state.servers.length" text="还没有节点。新建一个节点，按页面上的命令在主机上安装 Agent。">
      <RouterLink to="/servers/new" class="btn"><Icon name="plus" />新建节点</RouterLink>
    </EmptyState>

    <template v-else>
      <!-- 统计卡片（设计 9） -->
      <div class="stats">
        <RouterLink to="/servers" class="stat panel"><div class="muted small">节点</div><div class="n num">{{ installed.length }}</div></RouterLink>
        <RouterLink to="/servers" class="stat panel"><div class="muted small">在线</div><div class="n num ok">{{ online }}</div></RouterLink>
        <RouterLink to="/servers" class="stat panel"><div class="muted small">离线</div><div class="n num" :class="{ bad: offline }">{{ offline }}</div></RouterLink>
        <RouterLink to="/servers" class="stat panel"><div class="muted small">需要关注</div><div class="n num" :class="{ warn: attention.length }">{{ attention.length }}</div></RouterLink>
        <RouterLink to="/servers" class="stat panel"><div class="muted small">流量 ≥ 80%</div><div class="n num" :class="{ warn: trafficWarn }">{{ trafficWarn }}</div></RouterLink>
        <RouterLink v-if="pending.length" to="/servers" class="stat panel"><div class="muted small">待安装</div><div class="n num muted">{{ pending.length }}</div></RouterLink>
      </div>

      <section class="section">
        <h3>需要关注</h3>
        <div v-if="attention.length" class="panel list">
          <RouterLink v-for="{ s, problems } in attention" :key="s.id" :to="`/servers/${s.id}`" class="item">
            <StatusDot :status="s.status" dot-only />
            <span class="item-name">{{ s.name }}</span>
            <span class="tags">
              <span v-for="p in problems" :key="p.text" class="tag" :class="p.level">{{ p.text }}</span>
            </span>
          </RouterLink>
        </div>
        <p v-else class="panel muted all-good">全部节点运行正常。</p>
      </section>

      <section class="section">
        <h3>Top 5</h3>
        <div class="tops">
          <div v-for="t in tops" :key="t.title" class="panel top">
            <div class="muted small top-title">{{ t.title }}</div>
            <RouterLink v-for="r in t.rows" :key="r.s.id" :to="`/servers/${r.s.id}`" class="top-row">
              <span class="item-name">{{ r.s.name }}</span>
              <span class="num">{{ r.text }}</span>
            </RouterLink>
            <p v-if="!t.rows.length" class="muted small">暂无在线节点</p>
          </div>
        </div>
      </section>
    </template>
  </main>
</template>

<style scoped>
.stats { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: var(--space-3); }
.stat { color: inherit; text-decoration: none; padding: var(--space-4); }
.stat:hover { border-color: var(--text-muted); text-decoration: none; }
.n { font-size: var(--font-num); line-height: var(--line-num); font-weight: var(--weight-strong); margin-top: var(--space-1); }
.list { padding: var(--space-1) 0; }
.item { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-2) var(--space-4); color: inherit; text-decoration: none; }
.item + .item { border-top: 1px solid var(--border); }
.item:hover { background: var(--surface-2); text-decoration: none; }
.item-name { font-weight: var(--weight-strong); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.tags { display: flex; gap: var(--space-2); flex-wrap: wrap; margin-left: auto; justify-content: flex-end; }
.tag { font-size: var(--font-xs); line-height: var(--line-xs); padding: 1px var(--space-2); border-radius: var(--radius-full); border: 1px solid currentColor; }
.tag.bad { color: var(--bad); }
.tag.warn { color: var(--warn); }
.all-good { margin: 0; }
.tops { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: var(--space-3); }
.top { padding: var(--space-4); }
.top-title { margin-bottom: var(--space-2); }
.top-row { display: flex; justify-content: space-between; gap: var(--space-3); padding: var(--space-1) 0; color: inherit; text-decoration: none; }
.top-row:hover .item-name { color: var(--accent); }
.top-row .item-name { font-weight: var(--weight-regular); }
</style>
