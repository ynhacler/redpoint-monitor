<script setup lang="ts">
// 节点安装命令页（设计 27.3.4）：展示命令，并等待主机注册结果。
// TODO(B): 改为 WebSocket 事件 server.enrolled 实时更新（设计 19.11、20），目前每 3 秒轮询一次。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ApiError, getInstallCommand, regenerateEnrollCode, UnauthorizedError, type EnrollCodeView } from '../api'
import CommandBlock from './CommandBlock.vue'

const props = defineProps<{
  /** 节点 ID */
  serverId: number
  /** 刚新建或刚重新生成时的结果，含完整注册码；从列表进入时为空 */
  initial?: EnrollCodeView
}>()
const emit = defineEmits<{
  /** 返回列表 */
  back: []
  /** Token 失效，需要重新输入 */
  unauthorized: []
}>()

const view = ref<EnrollCodeView | undefined>(props.initial)
// 完整注册码只在生成时返回一次（设计 19.11），轮询结果中没有，需要单独保留
const fullCode = ref(props.initial?.enroll_code ?? '')
const error = ref('')
const busy = ref(false)
let timer: number | undefined

// 有完整注册码时展示可直接执行的命令；否则命令里只有脱敏提示，需要重新生成
const command = computed(() => {
  const v = view.value
  if (!v) return ''
  return fullCode.value ? v.install.command.replace(v.enroll_code_hint, fullCode.value) : v.install.command
})
const status = computed(() => view.value?.enroll_status ?? 'NONE')
const enrolled = computed(() => status.value === 'USED')
const expiresText = computed(() => {
  const at = view.value?.enroll_expires_at
  return at ? new Date(at * 1000).toLocaleString() : ''
})

async function refresh() {
  try {
    const v = await getInstallCommand(props.serverId)
    view.value = v
    error.value = ''
    // 已注册、过期或撤销后不再需要轮询
    if (v.enroll_status !== 'ACTIVE') stop()
  } catch (e) {
    if (e instanceof UnauthorizedError) {
      stop()
      emit('unauthorized')
    } else if (e instanceof ApiError) {
      error.value = e.message // 网络错误时保留已显示的命令，继续重试（设计 43.6）
    }
  }
}

function start() {
  stop()
  timer = window.setInterval(refresh, 3000)
}
function stop() {
  if (timer) window.clearInterval(timer)
  timer = undefined
}

async function regenerate() {
  if (enrolled.value && !confirm('该节点已注册。重新生成注册码用于重装或更换主机，新主机注册后旧主机将无法再上报。继续？')) return
  busy.value = true
  try {
    const v = await regenerateEnrollCode(props.serverId)
    view.value = v
    fullCode.value = v.enroll_code ?? ''
    start()
  } catch (e) {
    if (e instanceof UnauthorizedError) emit('unauthorized')
    else if (e instanceof ApiError) error.value = e.message
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  if (!view.value) refresh()
  start()
})
onUnmounted(stop)
</script>

<template>
  <section class="panel">
    <h2>安装命令 —— {{ view?.server_name ?? '…' }}</h2>
    <p v-if="error" class="banner">{{ error }}</p>

    <template v-if="view">
      <!-- 没有已验签的官方版本时只能手动安装（设计 27.3.1、27.3.3） -->
      <div v-if="view.install.mode === 'manual'" class="note">
        面板尚未同步官方签名版本，暂时需要手动安装：
        <ol>
          <li>把 <code>vpsmon-agent</code> 放到主机上（开发阶段：复制自己构建的 <code>dist/vpsmon-agent-linux-*</code>）。</li>
          <li>在主机上以 root 执行下面的命令，它会注册、安装为系统服务并开始上报。</li>
        </ol>
        如果程序不在 PATH 中，把命令开头的 <code>vpsmon-agent</code> 换成它的路径，如 <code>./vpsmon-agent-linux-amd64</code>。
      </div>

      <template v-if="status === 'ACTIVE' && fullCode">
        <CommandBlock :command="command" />
        <p class="muted">注册码 {{ expiresText }} 前有效，仅可使用一次。完整注册码只显示这一次，离开本页后需重新生成。</p>
      </template>
      <template v-else-if="status === 'ACTIVE'">
        <CommandBlock :command="command" />
        <p class="muted">完整注册码只在生成时显示一次，上面的命令中已隐藏。如果没有保存，请重新生成。</p>
      </template>
      <p v-else-if="status === 'EXPIRED'" class="muted">注册码已过期，请重新生成。</p>
      <p v-else-if="status === 'REVOKED'" class="muted">注册码已撤销，请重新生成。</p>
      <p v-else-if="status === 'NONE'" class="muted">该节点还没有注册码。</p>

      <p class="state">
        <template v-if="enrolled"><span class="ok">✓ 已连接 {{ view.server_name }}</span></template>
        <template v-else-if="status === 'ACTIVE'"><span class="dot pending"></span> 等待主机连接…</template>
      </p>
    </template>
    <p v-else class="muted">加载中…</p>

    <div class="actions">
      <button type="button" :disabled="busy" @click="regenerate">
        {{ enrolled ? '重新安装 / 更换主机' : '重新生成注册码' }}
      </button>
      <button type="button" class="secondary" @click="emit('back')">{{ enrolled ? '返回列表' : '稍后安装' }}</button>
    </div>
  </section>
</template>

<style scoped>
.note { background: var(--code-bg); border-left: 3px solid var(--warn); padding: 10px 14px; border-radius: 6px; margin: 12px 0; }
.note ol { margin: 6px 0; padding-left: 20px; }
.state { display: flex; align-items: center; gap: 8px; margin-top: 16px; }
.actions { display: flex; gap: 8px; margin-top: 20px; }
</style>
