<script setup lang="ts">
// 账号页（/account）：“系统 → 账号”（修订第 158 条）；使用初始 / 重置密码登录时也被强制带到这里（设计 17.4）。
import AccountSettings from '../components/AccountSettings.vue'
</script>

<template>
  <main class="page account-page">
    <div class="page-head"><h1>账号</h1></div>
    <AccountSettings />
  </main>
</template>

<style scoped>
.account-page :deep(.account-wrap) { margin: 0 auto; }
.account-page .page-head { max-width: 420px; margin-left: auto; margin-right: auto; }
</style>
