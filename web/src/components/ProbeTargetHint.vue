<script setup lang="ts">
import { t } from '../i18n'
// 测速目标地址的来源提示（修订第 131 条）：推荐按运营商与省市查找节点的目录站。
// 只是一个外部链接，面板不访问该网站，也不向它发送任何数据（约束 7）。
const url = 'https://www.zstaticcdn.com/'
</script>

<template>
  <p class="hint small muted">
    {{ t('找测试地址：推荐') }} <a :href="url" target="_blank" rel="noopener noreferrer">{{ t('Zstatic 节点查询') }}</a>{{ t('，按运营商与省市查找三网节点。可填 IPv4、IPv6 或域名（粘贴网址会只保留域名）；添加前可在 VPS 上') }} <code>ping</code> {{ t('一下，确认能连通。') }}
  </p>
</template>

<style scoped>
.hint { margin: 0; }
.hint code { font-family: var(--font-mono); font-size: var(--font-xs); }
</style>
