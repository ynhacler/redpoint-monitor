<script setup lang="ts">
// 测速目标的勾选表（设计 42.3.2）：内置目标按地区 × 运营商排列（迁移 38 后已没有内置目标），自己添加的目标逐个列出。
// 全局设置（“监测 → 测速设置”）与节点自己的设置共用；勾选的含义由调用方决定（全局启用 / 这台节点要测）。
import { computed } from 'vue'
import type { ProbeTarget } from '../api'
import { fmtLatency, fmtPct } from '../format'

const props = defineProps<{
  targets: ProbeTarget[]
  /** 已勾选的目标 ID */
  selected: number[]
  /** 正在保存的目标 ID（禁用该勾选框） */
  busy?: number | null
}>()
const emit = defineEmits<{ toggle: [target: ProbeTarget] }>()

const isps = [
  { key: 'ct', name: '电信' },
  { key: 'cu', name: '联通' },
  { key: 'cm', name: '移动' },
] as const
const ispName = (k: string) => (k === 'intl' ? '境外' : isps.find((i) => i.key === k)?.name ?? k)

const rows = computed(() => {
  const out: { province: string; cells: Partial<Record<string, ProbeTarget>> }[] = []
  for (const t of props.targets) {
    if (!t.builtin || t.isp === 'intl') continue
    let r = out.find((x) => x.province === t.province)
    if (!r) out.push((r = { province: t.province, cells: {} }))
    r.cells[t.isp] = t
  }
  return out
})
const intl = computed(() => props.targets.filter((t) => t.builtin && t.isp === 'intl'))
const custom = computed(() => props.targets.filter((t) => !t.builtin))
const on = (t: ProbeTarget) => props.selected.includes(t.id)
// 最近 24 小时全部节点的汇总（修订第 134 条）：失效的目标提示更换
const healthBadge: Record<string, { text: string; cls: string }> = {
  dead: { text: '疑似失效', cls: 'bad' }, degraded: { text: '丢包偏高', cls: 'warn' },
}
function healthText(t: ProbeTarget): string {
  const h = t.health
  if (!h || h.state === 'nodata') return '24 小时内没有结果'
  const lat = h.latency_ms == null ? '全部失败' : fmtLatency(h.latency_ms)
  return `24 小时：${h.nodes} 个节点 · ${lat} · 丢包 ${fmtPct(h.loss)}`
}
const where = (t: ProbeTarget) => `${t.proto === 'icmp' ? 'ICMP' : `TCP ${t.port}`} → ${t.address}`
</script>

<template>
  <div>
    <table v-if="rows.length">
      <thead><tr><th scope="col">地区</th><th v-for="i in isps" :key="i.key" scope="col">{{ i.name }}</th></tr></thead>
      <tbody>
        <tr v-for="r in rows" :key="r.province">
          <th scope="row">{{ r.province }}</th>
          <td v-for="i in isps" :key="i.key">
            <label v-if="r.cells[i.key]" class="check" :title="where(r.cells[i.key]!)">
              <input type="checkbox" :checked="on(r.cells[i.key]!)" :disabled="busy === r.cells[i.key]!.id"
                :aria-label="`${r.province}${i.name}`" @change="emit('toggle', r.cells[i.key]!)" />
              <span class="small muted mono addr">{{ r.cells[i.key]!.address }}</span>
            </label>
          </td>
        </tr>
      </tbody>
    </table>
    <!-- 境外（修订第 122 条）：主要地区云厂商的测速主机 -->
    <template v-if="intl.length">
      <h4 class="sub">境外</h4>
      <ul class="intl">
        <li v-for="t in intl" :key="t.id">
          <label class="check" :title="where(t)">
            <input type="checkbox" :checked="on(t)" :disabled="busy === t.id" :aria-label="`境外${t.province}`" @change="emit('toggle', t)" />
            <span>{{ t.province }}</span><small class="muted">{{ t.note }}</small>
          </label>
        </li>
      </ul>
    </template>
    <h4 v-if="custom.length && (rows.length || intl.length)" class="sub">自己添加的</h4>
    <ul v-if="custom.length" class="custom">
      <li v-for="t in custom" :key="t.id">
        <label class="check"><input type="checkbox" :checked="on(t)" :disabled="busy === t.id" @change="emit('toggle', t)" />
          <span>{{ t.province }} · {{ ispName(t.isp) }}</span></label>
        <span class="small muted mono">{{ where(t) }}</span>
        <span v-if="t.health" class="small health">
          <b v-if="healthBadge[t.health.state]" class="badge" :class="healthBadge[t.health.state].cls"
            :title="t.health.state === 'dead' ? '最近 24 小时几乎没有一次成功：地址可能已失效，或被防火墙拦截，建议更换' : ''">{{ healthBadge[t.health.state].text }}</b>
          <span class="muted">{{ healthText(t) }}</span>
        </span>
        <slot name="custom-extra" :target="t" />
      </li>
    </ul>
  </div>
</template>

<style scoped>
table { width: 100%; border-collapse: collapse; font-size: var(--font-sm); }
th, td { padding: var(--space-2) var(--space-3); text-align: left; border-bottom: 1px solid var(--border); }
th:first-child { padding-left: 0; width: 5em; }
thead th { color: var(--text-muted); font-weight: var(--weight-regular); }
tbody th { font-weight: var(--weight-regular); }
.check { display: inline-flex; align-items: center; gap: var(--space-2); cursor: pointer; }
.check input { width: auto; margin: 0; }
.mono { font-family: var(--font-mono); }
.muted { color: var(--text-muted); }
.sub { margin: var(--space-4) 0 var(--space-2); font-size: var(--font-sm); font-weight: var(--weight-strong); }
.intl { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: var(--space-2) var(--space-3); font-size: var(--font-sm); }
.intl small { margin-left: var(--space-1); }
.custom { list-style: none; margin: 0; padding: 0; }
.custom li { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2) var(--space-4); padding: var(--space-2) 0; }
.custom li + li { border-top: 1px solid var(--border); }
.health { display: inline-flex; align-items: center; gap: var(--space-2); }
.badge { font-weight: var(--weight-regular); font-size: var(--font-xs); padding: 0 var(--space-2); border-radius: var(--radius-full); border: 1px solid currentColor; }
.badge.bad { color: var(--bad); }
.badge.warn { color: var(--warn); }
@media (max-width: 600px) {
  th, td { padding: var(--space-2) var(--space-1); }
  .addr { display: none; } /* 窄屏只显示勾选框，地址在悬停提示中 */
}
</style>
