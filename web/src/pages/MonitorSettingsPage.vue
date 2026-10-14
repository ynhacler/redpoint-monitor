<script setup lang="ts">
// “监测”分组中的设置页（修订第 158 条）：测速目标与频率（设计 42.3.2）、证书监控（设计 33.3）、服务监控（设计 33.2）。
// 原来是“设置”页的三个标签，旧地址 /settings?tab=… 由路由转到这里。
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import ProbeTargets from '../components/ProbeTargets.vue'
import ServiceMonitors from '../components/ServiceMonitors.vue'
import SslMonitors from '../components/SslMonitors.vue'

const props = defineProps<{ kind: 'probes' | 'ssl' | 'services' }>()
const route = useRoute()
const title = computed(() => (route.meta.title as string) ?? '')
</script>

<template>
  <main class="page">
    <div class="page-head"><h1>{{ title }}</h1></div>
    <ProbeTargets v-if="props.kind === 'probes'" />
    <SslMonitors v-else-if="props.kind === 'ssl'" />
    <ServiceMonitors v-else />
  </main>
</template>
