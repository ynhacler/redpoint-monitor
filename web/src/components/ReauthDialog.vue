<script setup lang="ts">
// 重新输入密码（设计 17.4，修订第 306 条）：敏感操作被面板要求重新验证时弹出，见 reauth.ts。
import { ref, watch } from 'vue'
import BottomSheet from './BottomSheet.vue'
import { t } from '../i18n'
import { cancelReauth, reauthState, submitReauth } from '../reauth'

const password = ref('')
watch(() => reauthState.open, () => (password.value = ''))

async function submit() {
  await submitReauth(password.value)
  if (!reauthState.open) password.value = ''
}
</script>

<template>
  <BottomSheet :open="reauthState.open" :title="t('确认身份')" @close="cancelReauth">
    <form class="reauth" @submit.prevent="submit">
      <p class="muted small">{{ t('这是敏感操作，请输入登录密码确认。10 分钟内不再询问。') }}</p>
      <label>
        {{ t('登录密码') }}
        <input v-model="password" type="password" autocomplete="current-password" autofocus />
      </label>
      <p v-if="reauthState.error" class="error small" role="alert">{{ reauthState.error }}</p>
      <div class="actions">
        <button type="button" class="btn secondary" @click="cancelReauth">{{ t('取消') }}</button>
        <button type="submit" class="btn grow" :disabled="reauthState.busy">{{ t('确认') }}</button>
      </div>
    </form>
  </BottomSheet>
</template>

<style scoped>
.reauth { display: flex; flex-direction: column; gap: var(--space-3); }
.reauth label { display: flex; flex-direction: column; gap: var(--space-1); }
.actions { display: flex; gap: var(--space-2); }
.grow { flex: 1; }
.error { color: var(--bad); margin: 0; }
</style>
