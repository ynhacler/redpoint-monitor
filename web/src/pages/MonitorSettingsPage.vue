<script setup lang="ts">
// “系统 → 监测”（修订第 176 条）：测速目标与频率（设计 42.3.2）与服务监控（设计 33.2）合并为一页。
// 证书已并入告警规则（修订第 173 条）；旧地址 /probes、/services、/settings?tab=… 由路由转到这里。
import ProbeTargets from '../components/ProbeTargets.vue'
import ServiceMonitors from '../components/ServiceMonitors.vue'
</script>

<template>
  <main class="page">
    <div class="page-head"><h1>监测</h1></div>
    <h2 id="probes">测速</h2>
    <ProbeTargets />
    <h2 id="services" class="later">服务</h2>
    <ServiceMonitors />
  </main>
</template>

<style scoped>
.later { margin-top: var(--space-6); }
</style>
