<script setup lang="ts">
// 三网延迟目标的勾选表（设计 42.3.2）：每行一个地区、每列一个运营商；自定义目标在表后单独列出。
// 全局设置（“设置 → 三网”）与节点自己的设置共用；勾选的含义由调用方决定（全局启用 / 这台节点要测）。
import { computed } from 'vue'
import type { ProbeTarget } from '../api'

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
    <h4 v-if="custom.length" class="sub">自定义</h4>
    <ul v-if="custom.length" class="custom">
      <li v-for="t in custom" :key="t.id">
        <label class="check"><input type="checkbox" :checked="on(t)" :disabled="busy === t.id" @change="emit('toggle', t)" />
          <span>{{ t.province }} · {{ ispName(t.isp) }}</span></label>
        <span class="small muted mono">{{ where(t) }}</span>
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
@media (max-width: 600px) {
  th, td { padding: var(--space-2) var(--space-1); }
  .addr { display: none; } /* 窄屏只显示勾选框，地址在悬停提示中 */
}
</style>
