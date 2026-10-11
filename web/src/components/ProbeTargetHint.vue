<script setup lang="ts">
// 测速目标地址的来源提示（修订第 131 条）：推荐按运营商与省市查找节点的目录站。
// 只是一个外部链接，面板不访问该网站，也不向它发送任何数据（约束 7）。
const url = 'https://www.zstaticcdn.com/'
</script>

<template>
  <p class="hint small muted">
    找测试地址：推荐 <a :href="url" target="_blank" rel="noopener noreferrer">Zstatic 节点查询</a>，按运营商与省市查找三网节点。
    这里只能填 IP：查到的是域名时，先在 VPS 上 <code>ping 域名</code> 得到 IP，确认能连通再添加。
  </p>
</template>

<style scoped>
.hint { margin: 0; }
.hint code { font-family: var(--font-mono); font-size: var(--font-xs); }
</style>
