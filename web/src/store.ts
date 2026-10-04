// 全局状态：登录状态与节点列表的轮询。总览、列表、详情共用同一份数据，避免各页面重复请求。
// 页面还少，用 Vue 的 reactive 即可；TODO(B): 状态变复杂后考虑 Pinia（设计 3.3，需先确认依赖）。
import { computed, reactive, watch } from 'vue'
import {
  errorText, getMe, listServers, login as apiLogin, logoutSession, onUnauthorized,
  type CaptchaAnswer, type EnrollCodeView, type Me, type ServerView,
} from './api'
import { connected, subscribe } from './ws'

export const state = reactive({
  /** 当前登录账号；null 表示未登录（设计 19.1） */
  me: null as Me | null,
  /** 是否已向面板确认过登录状态（首次加载前不跳转，避免闪一下登录页） */
  authChecked: false,
  servers: [] as ServerView[],
  /** 首次加载完成前为 false，用于显示“加载中” */
  loaded: false,
  /** 连接错误；上一次成功的数据继续显示（设计 43.6） */
  error: '',
  updatedAt: null as Date | null,
})

let timer: number | undefined
/** 会话失效时的回调（由 router 设置为跳转登录页），避免 store 依赖 router 造成循环引用 */
let onSignedOut: () => void = () => {}
export function setSignedOutHandler(f: () => void) {
  onSignedOut = f
}

// 任何接口返回 401（会话失效）：停止轮询、清空数据并回到登录页，登录后回到原页面（设计 43.6）
onUnauthorized(() => {
  if (state.me) signedOut()
})

/** 拉取一次节点列表；失败时保留上一次的数据并在顶部提示（设计 43.6）。 */
export async function refresh() {
  try {
    state.servers = await listServers()
    state.updatedAt = new Date()
    state.error = ''
    state.loaded = true
  } catch (e) {
    state.error = errorText(e, '无法连接面板，正在重试')
  }
}

// 实时更新（设计 45.2）：WebSocket 推送 server.metrics 时直接替换该节点；上下线与告警事件触发一次完整刷新。
// 连接正常时只需每 30 秒兜底刷新一次（流量、静音等没有事件的变化）；连接断开时退回每 3 秒轮询。
let unsubs: (() => void)[] = []
let refreshSoon: number | undefined

function scheduleRefresh() {
  if (refreshSoon) return
  refreshSoon = window.setTimeout(() => {
    refreshSoon = undefined
    refresh()
  }, 500) // 合并短时间内的多个事件（如批量离线）
}

function tick() {
  timer = window.setTimeout(async () => {
    await refresh()
    if (timer) tick()
  }, connected.value ? 30_000 : 3000)
}

/** 开始实时更新：订阅事件，并按连接状态轮询；面板从内存返回实时状态，不读数据库。 */
export function startPolling() {
  stopPolling()
  unsubs = [
    subscribe('server.metrics', (ev) => {
      const v = ev.data as ServerView | undefined
      const i = state.servers.findIndex((s) => s.id === ev.server_id)
      if (!v || i < 0) {
        scheduleRefresh() // 新节点等：完整刷新
        return
      }
      state.servers[i] = v
      state.updatedAt = new Date()
    }),
    ...(['server.online', 'server.offline', 'alert.triggered', 'alert.recovered'] as const).map((t) => subscribe(t, scheduleRefresh)),
  ]
  refresh()
  tick()
}

// 重新连上时补一次完整刷新（断线期间的事件不补发）
watch(connected, (on) => {
  if (on && timer) refresh()
})

export function stopPolling() {
  if (timer) window.clearTimeout(timer)
  timer = undefined
  if (refreshSoon) window.clearTimeout(refreshSoon)
  refreshSoon = undefined
  unsubs.forEach((u) => u())
  unsubs = []
}

/** 页面加载时恢复登录状态：会话 Cookie 仍有效则直接进入，否则留在登录页。 */
export async function checkAuth() {
  try {
    state.me = await getMe()
  } catch {
    state.me = null
  } finally {
    state.authChecked = true
  }
}

/** 登录成功后开始轮询（需修改初始密码时先不轮询，接口会返回 403） */
export async function login(username: string, password: string, remember: boolean, captcha?: CaptchaAnswer) {
  state.me = await apiLogin(username, password, remember, captcha)
  if (!state.me.must_change_password) startPolling()
}

/** 修改密码成功后：清除“需修改密码”标记并开始轮询 */
export function passwordChanged() {
  if (state.me) state.me.must_change_password = false
  startPolling()
}

/** 主动退出登录 */
export async function logout() {
  try {
    await logoutSession()
  } catch {
    /* 会话已失效时忽略，照常回到登录页 */
  }
  signedOut()
}

/** 会话结束（退出或失效）：清空数据，回到登录页 */
function signedOut() {
  stopPolling()
  state.me = null
  state.servers = []
  state.loaded = false
  onSignedOut()
}

/** 节点异常程度排序（设计 1.5.6）：离线 > 未知 > 在线；待安装单独显示，不参与 */
export const statusRank = { offline: 0, unknown: 1, online: 2, pending: 3 } as const

/** 已安装节点（不含待安装） */
export const installed = computed(() => state.servers.filter((s) => s.status !== 'pending'))
export const pending = computed(() => state.servers.filter((s) => s.status === 'pending'))

/** 按 ID 查找节点 */
export function serverById(id: number) {
  return state.servers.find((s) => s.id === id)
}

// 刚新建或刚重新生成的注册码结果（含完整注册码）只在内存中交给安装命令页一次。
// 【安全】不写入任何 storage：完整注册码只显示一次（设计 19.11），刷新页面后需重新生成。
let justCreated: EnrollCodeView | undefined

/** 新建节点成功后暂存结果，供随后打开的安装命令页使用 */
export function stashCreated(v: EnrollCodeView) {
  justCreated = v
}

/** 取出并清除暂存结果；ID 不匹配时返回 undefined */
export function takeCreated(id: number): EnrollCodeView | undefined {
  const v = justCreated?.server_id === id ? justCreated : undefined
  justCreated = undefined
  return v
}
