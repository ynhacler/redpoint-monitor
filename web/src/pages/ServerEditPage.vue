<script setup lang="ts">
// 新建 / 编辑节点页（设计 27.2、19.5）。表单本身在 ServerForm 中；这里负责加载节点与页面跳转。
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, getServer, UnauthorizedError, type EnrollCodeView, type ServerView } from '../api'
import Icon from '../components/Icon.vue'
import ServerForm from '../components/ServerForm.vue'
import { logout, refresh, stashCreated } from '../store'

const props = defineProps<{
  /** 编辑时为节点 ID；新建时为空 */
  id?: string
}>()
const router = useRouter()
const server = ref<ServerView>()
const loading = ref(!!props.id)
const error = ref('')

// 编辑时从面板读取最新数据，而不是用列表里可能过时的副本
onMounted(async () => {
  if (!props.id) return
  try {
    server.value = await getServer(Number(props.id))
  } catch (e) {
    if (e instanceof UnauthorizedError) logout()
    else if (e instanceof ApiError) error.value = e.message
  } finally {
    loading.value = false
  }
})

function created(v: EnrollCodeView) {
  stashCreated(v)
  refresh()
  router.push(`/servers/${v.server_id}/install`)
}
function done() {
  refresh()
  // 删除后详情页已不存在，回到列表；保存后回到详情
  router.push(props.id && server.value ? `/servers/${props.id}` : '/servers')
}
function deleted() {
  refresh()
  router.push('/servers')
}
</script>

<template>
  <main class="page">
    <RouterLink :to="id ? `/servers/${id}` : '/servers'" class="back muted small"><Icon name="arrow-left" :size="14" />返回</RouterLink>
    <p v-if="loading" class="muted">加载中…</p>
    <p v-else-if="error" class="banner">{{ error }}</p>
    <ServerForm
      v-else
      :server="server"
      @created="created"
      @done="done"
      @deleted="deleted"
      @cancel="router.back()"
      @unauthorized="logout"
    />
  </main>
</template>

<style scoped>
.back { display: inline-flex; align-items: center; gap: var(--space-1); margin-bottom: var(--space-3); }
</style>
