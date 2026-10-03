<script setup lang="ts">
// 节点安装命令页（设计 27.3.4）：展示命令，并等待主机注册结果。
// TODO(B): 改为 WebSocket 事件 server.enrolled 实时更新（设计 19.11、20），目前每 3 秒轮询一次。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ApiError, getInstallCommand, regenerateEnrollCode, syncReleases, UnauthorizedError, type EnrollCodeView } from '../api'
import { fmtTime } from '../format'
import CommandBlock from './CommandBlock.vue'
import StatusDot from './StatusDot.vue'

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
const withCode = (cmd: string) => {
  const v = view.value
  return v && fullCode.value ? cmd.replace(v.enroll_code_hint, fullCode.value) : cmd
}
const command = computed(() => (view.value ? withCode(view.value.install.command) : ''))
const manualCommand = computed(() => (view.value ? withCode(view.value.install.manual_command) : ''))
const showManual = ref(false)
const rotateCommand = computed(() => (view.value ? withCode(view.value.install.rotate_command) : ''))
const showRotate = ref(false)

// 同步官方版本：成功后重新获取命令（默认命令需要已验签的版本）
const syncing = ref(false)
const syncMsg = ref('')
async function sync() {
  syncing.value = true
  syncMsg.value = ''
  try {
    const r = await syncReleases()
    syncMsg.value = `已同步并验签 v${r.version}`
    await refresh()
  } catch (e) {
    if (e instanceof UnauthorizedError) emit('unauthorized')
    else syncMsg.value = e instanceof ApiError ? e.message : '同步失败'
  } finally {
    syncing.value = false
  }
}
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
      <!-- 默认命令：下载按版本固定的脚本 → 按已验签清单中的 SHA256 校验 → 执行（设计 27.3.1） -->
      <p v-if="view.install.mode === 'default' && view.install.release" class="muted small">
        在主机上以 root 执行。命令会先校验安装脚本的 SHA256（来自官方签名的 v{{ view.install.release.version }} 发布清单，
        公钥 {{ view.install.release.key_id }}，{{ fmtTime(view.install.release.synced_at) }} 同步），不一致时不会执行。
        <template v-if="view.install.release.mirrored">脚本与程序从<b>本面板镜像</b>下载，适合访问 GitHub 不稳定的主机。</template>
        <template v-else>脚本与程序从 GitHub 官方发布下载。</template>
      </p>
      <!-- 没有已验签的官方版本时只能手动安装（设计 27.3.3） -->
      <div v-else class="note">
        面板尚未同步官方签名版本，暂时需要手动安装：把 <code>vpsmon-agent</code> 放到主机上，再以 root 执行下面的命令。
        <div class="sync">
          <button type="button" class="secondary" :disabled="syncing" @click="sync">{{ syncing ? '同步中…' : '同步官方版本' }}</button>
          <span v-if="syncMsg" class="small">{{ syncMsg }}</span>
        </div>
      </div>

      <template v-if="status === 'ACTIVE' && fullCode">
        <CommandBlock :command="command" />
        <p class="muted">注册码 {{ expiresText }} 前有效，仅可使用一次。完整注册码只显示这一次，离开本页后需重新生成。</p>
      </template>
      <template v-else-if="status === 'ACTIVE'">
        <CommandBlock :command="command" />
        <p class="muted">完整注册码只在生成时显示一次，上面的命令中已隐藏。如果没有保存，请重新生成。</p>
      </template>
      <template v-if="status === 'ACTIVE' && view.install.mode === 'default'">
        <button type="button" class="text" @click="showManual = !showManual">{{ showManual ? '▾' : '▸' }} 程序已在主机上？使用手动命令</button>
        <CommandBlock v-if="showManual" :command="manualCommand" />
      </template>
      <template v-if="status === 'ACTIVE'">
        <button type="button" class="text" @click="showRotate = !showRotate">{{ showRotate ? '▾' : '▸' }} 主机上已安装 Agent（如 Token 已吊销）？更换 Token</button>
        <template v-if="showRotate">
          <CommandBlock :command="rotateCommand" />
          <p class="muted small">不需要卸载重装：用这个一次性注册码换取新 Token 并重启服务，该节点之前的 Token 随即失效。</p>
        </template>
      </template>
      <p v-else-if="status === 'EXPIRED'" class="muted">注册码已过期，请重新生成。</p>
      <p v-else-if="status === 'REVOKED'" class="muted">注册码已撤销，请重新生成。</p>
      <p v-else-if="status === 'NONE'" class="muted">该节点还没有注册码。</p>

      <p class="state">
        <template v-if="enrolled"><span class="ok">✓ 已连接 {{ view.server_name }}</span></template>
        <template v-else-if="status === 'ACTIVE'"><StatusDot status="pending" dot-only /> 等待主机连接…</template>
      </p>
    </template>
    <p v-else class="muted">加载中…</p>

    <div class="actions">
      <button type="button" :disabled="busy" @click="regenerate">
        {{ enrolled ? '重新安装 / 更换主机' : '重新生成注册码' }}
      </button>
      <button type="button" class="secondary" @click="emit('back')">{{ enrolled ? '查看节点' : '稍后安装' }}</button>
    </div>
  </section>
</template>

<style scoped>
.note { background: var(--surface-2); border-left: 3px solid var(--warn); padding: var(--space-3) var(--space-4); border-radius: var(--radius-sm); margin: var(--space-3) 0; }
.note ol { margin: var(--space-2) 0; padding-left: var(--space-5); }
.sync { display: flex; align-items: center; gap: var(--space-3); margin-top: var(--space-2); }
.state { display: flex; align-items: center; gap: var(--space-2); margin-top: var(--space-4); }
.actions { display: flex; gap: var(--space-2); margin-top: var(--space-5); }
</style>
