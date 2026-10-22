<script setup lang="ts">
// 节点 IP 信息开关（设计 49，修订第 191 条）：打开后面板每周把各节点的公网 IP 发给 ipinfo.io，查询 ASN、运营方与位置，
// 显示在节点详情的概况中。【隐私】默认关闭（设计 1.8：IP 上传默认关闭），说明写清楚会发送什么、发给谁。
import { onMounted, ref } from 'vue'
import { errorText, getIPInfoSetting, putIPInfoSetting } from '../api'

const enabled = ref(false)
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
onMounted(async () => {
  try {
    enabled.value = (await getIPInfoSetting()).enabled
  } catch (e) {
    error.value = errorText(e, '加载失败')
  } finally {
    loaded.value = true
  }
})
async function toggle(on: boolean) {
  saving.value = true
  error.value = ''
  try {
    enabled.value = (await putIPInfoSetting(on)).enabled
  } catch (e) {
    error.value = errorText(e, '保存失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="panel ipinfo">
    <label class="row">
      <input type="checkbox" :checked="enabled" :disabled="!loaded || saving" @change="toggle(($event.target as HTMLInputElement).checked)" />
      <span><b>用 ipinfo.io 查询节点的 ASN 与位置</b></span>
    </label>
    <p class="small muted">
      打开后，面板每周把各节点的公网 IP 发给第三方服务 ipinfo.io（免费额度，不需要账号），查询 ASN、运营方与城市，显示在节点详情的概况中；
      地址变化时立即重新查询。关闭后不再发送任何 IP，已查到的信息保留。
    </p>
    <p v-if="error" class="small bad">{{ error }}</p>
  </section>
</template>

<style scoped>
.ipinfo { padding: var(--space-4) var(--space-5); }
.row { display: flex; align-items: center; gap: var(--space-2); cursor: pointer; }
.row input { width: auto; }
.ipinfo p { margin: var(--space-2) 0 0; }
</style>
