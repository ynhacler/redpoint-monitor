<script setup lang="ts">
// 设置页（修订第 126 条）：测速目标与频率（设计 42.3.2）、证书监控（设计 33.3）、服务监控（设计 33.2）、账号与密码（设计 17.4）。
// 原来放在“告警”页的这些标签移到这里，告警页只保留告警、规则、静音与通知；旧地址 /alerts?tab=… 由路由转到这里。
// 标签写在地址中（?tab=ssl），刷新与分享后保持。
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AccountSettings from '../components/AccountSettings.vue'
import ProbeTargets from '../components/ProbeTargets.vue'
import ServiceMonitors from '../components/ServiceMonitors.vue'
import SslMonitors from '../components/SslMonitors.vue'

const tabs = [
  { key: 'probes', name: '测速' },
  { key: 'ssl', name: '证书' },
  { key: 'services', name: '服务' },
  { key: 'account', name: '账号' },
] as const
type Tab = (typeof tabs)[number]['key']

const route = useRoute()
const router = useRouter()
const tab = computed<Tab>(() => tabs.find((t) => t.key === route.query.tab)?.key ?? 'probes')
const select = (t: Tab) => router.replace({ query: { tab: t === 'probes' ? undefined : t } })
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>设置</h1>
      <div class="segmented" role="tablist">
        <button v-for="t in tabs" :key="t.key" type="button" role="tab" :aria-selected="tab === t.key" :class="{ active: tab === t.key }"
          @click="select(t.key)">{{ t.name }}</button>
      </div>
    </div>
    <ProbeTargets v-if="tab === 'probes'" />
    <SslMonitors v-else-if="tab === 'ssl'" />
    <ServiceMonitors v-else-if="tab === 'services'" />
    <AccountSettings v-else />
  </main>
</template>

<style scoped>
.page-head .segmented { margin-left: auto; }
</style>
