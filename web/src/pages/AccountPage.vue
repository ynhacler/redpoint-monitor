<script setup lang="ts">
// 账号页（/account）：使用初始 / 重置密码登录时被强制带到这里（设计 17.4）；平时的入口是“设置 → 账号”（修订第 126 条），
// 路由守卫会把已改过密码的访问转到那里。
import AccountSettings from '../components/AccountSettings.vue'
</script>

<template>
  <main class="page account-page">
    <AccountSettings />
  </main>
</template>

<style scoped>
.account-page :deep(.account-wrap) { margin: var(--space-6) auto 0; }
</style>
