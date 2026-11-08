<script setup lang="ts">
// 推送去向（修订第 320 条）：按渠道列出会收到哪些推送，把分散的设置合在一起看——
// 告警推送开关（修订第 316 条）、每个渠道的告警分组与最低级别（修订第 318 条）、订阅推送的种类与渠道（修订第 316 条）。
// 另列出没有任何渠道接收的告警分组。只在页面上计算，规则与面板 NotifyChannel.wants / dispatchSub 一致。
import { computed, onMounted, ref } from 'vue'
import { getSubPush, listAppDevices, type NotifyChannel, type SubPush } from '../api'
import { alertGroups, severityNames } from '../alertRules'
import { t } from '../i18n'
import { alertsOn } from '../store'

const props = defineProps<{
  /** 全部通知渠道（随渠道列表刷新） */
  channels: NotifyChannel[]
}>()

const sub = ref<SubPush | null>(null)
const devices = ref(0)
onMounted(async () => {
  try {
    sub.value = await getSubPush()
  } catch {
    sub.value = null // 旧面板没有订阅推送
  }
  try {
    devices.value = (await listAppDevices()).filter((d) => d.status === 'active').length
  } catch {
    devices.value = 0
  }
})

const enabled = computed(() => props.channels.filter((c) => c.enabled))
const groupsOf = (c: NotifyChannel) => (c.alert_groups.length ? alertGroups.filter((g) => (c.alert_groups as string[]).includes(g.key)) : alertGroups)

// 某个渠道收到的订阅提醒种类
function subKinds(c: NotifyChannel | null): string[] {
  const p = sub.value
  if (!p?.enabled) return []
  if (c && p.channel_ids.length && !p.channel_ids.includes(c.id)) return []
  if (!c && !p.app) return []
  return [p.charge && t('扣费'), p.end && t('结束'), p.budget && t('超预算')].filter(Boolean) as string[]
}

type Row = { key: string; name: string; alerts: string; subs: string; off?: boolean }
const rows = computed<Row[]>(() => {
  const out: Row[] = enabled.value.map((c) => {
    const groups = groupsOf(c)
    const level = c.min_severity === 'info' ? t('全部级别') : t('{v}及以上', { v: t(severityNames[c.min_severity]) })
    return {
      key: `c${c.id}`, name: c.name,
      alerts: !alertsOn.value ? t('不推送（告警推送已关闭）')
        : `${groups.length === alertGroups.length ? t('全部告警') : groups.map((g) => t(g.name)).join('、')} · ${level}`,
      subs: subKinds(c).join('、') || t('不推送'),
    }
  })
  if (devices.value) {
    out.push({ key: 'app', name: t('App（{n} 台已配对设备）', { n: devices.value }),
      alerts: alertsOn.value ? t('全部告警 · 警告及以上（按设备的授权范围）') : t('不推送（告警推送已关闭）'),
      subs: subKinds(null).join('、') || t('不推送') })
  }
  return out
})

// 告警推送打开时，没有任何启用的渠道接收的分组
const uncovered = computed(() => (!alertsOn.value || !enabled.value.length ? []
  : alertGroups.filter((g) => !enabled.value.some((c) => !c.alert_groups.length || (c.alert_groups as string[]).includes(g.key)))))
</script>

<template>
  <section class="panel routes" :aria-label="t('推送去向')">
    <h3>{{ t('推送去向') }}</h3>
    <p v-if="!enabled.length && !devices" class="small muted">{{ t('还没有启用的通知渠道，推送不会发到任何地方。') }}</p>
    <ul v-else>
      <li v-for="r in rows" :key="r.key">
        <b class="name">{{ r.name }}</b>
        <span class="small"><span class="muted">{{ t('告警：') }}</span>{{ r.alerts }}</span>
        <span class="small"><span class="muted">{{ t('订阅：') }}</span>{{ r.subs }}</span>
      </li>
    </ul>
    <p v-if="uncovered.length" class="small warn">{{ t('没有渠道接收：{v}。这些告警只在页面中显示。', { v: uncovered.map((g) => t(g.name)).join('、') }) }}</p>
  </section>
</template>

<style scoped>
.routes { padding: var(--space-3) var(--space-4); margin-bottom: var(--space-3); }
.routes h3 { margin: 0 0 var(--space-2); font-size: var(--font-md); }
.routes ul { list-style: none; margin: 0; padding: 0; }
.routes li { display: flex; flex-direction: column; gap: 2px; padding: var(--space-2) 0; border-top: 1px solid var(--border); }
.routes li:first-child { border-top: 0; padding-top: 0; }
.name { font-weight: var(--weight-strong); }
.warn { color: var(--warn); margin: var(--space-2) 0 0; }
.routes > p { margin: 0; }
</style>
