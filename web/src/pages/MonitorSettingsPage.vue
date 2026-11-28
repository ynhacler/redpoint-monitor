<script setup lang="ts">
// “系统 → 监测”（修订第 176 条）：测速目标与频率（设计 42.3.2）与服务监控（设计 33.2）合并为一页。
// 证书已并入告警规则（修订第 173 条）；旧地址 /probes、/services、/settings?tab=… 由路由转到这里。
import ProbeTargets from '../components/ProbeTargets.vue'
import IPInfoSettings from '../components/IPInfoSettings.vue'
import ServiceMonitors from '../components/ServiceMonitors.vue'
import { t } from '../i18n'
</script>

<template>
  <main class="page">
    <div class="page-head"><h1>{{ t('监测') }}</h1></div>
    <h2 id="probes">{{ t('测速') }}</h2>
    <ProbeTargets />
    <h2 id="services" class="later">{{ t('服务') }}</h2>
    <ServiceMonitors />
    <h2 id="ipinfo" class="later">{{ t('IP 信息') }}</h2>
    <IPInfoSettings />
  </main>
</template>

<style scoped>
/* 分区标题（修订第 342 条）：左侧强调色短条，分区之间细线分隔 */
h2 { display: flex; align-items: center; gap: var(--space-2); margin: 0 0 var(--space-3); font-size: var(--font-lg); line-height: var(--line-lg); }
h2::before { content: ''; width: 4px; height: 18px; border-radius: 2px; background: var(--accent); flex: none; }
.later { margin-top: var(--space-7); padding-top: var(--space-5); border-top: 1px solid var(--border); }
</style>
