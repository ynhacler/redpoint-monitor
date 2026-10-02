// 全局状态：登录状态与节点列表的轮询。总览、列表、详情共用同一份数据，避免各页面重复请求。
// 页面还少，用 Vue 的 reactive 即可；TODO(B): 状态变复杂后考虑 Pinia（设计 3.3，需先确认依赖）。
import { computed, reactive } from 'vue'
import {
  getMe, listServers, login as apiLogin, logoutSession, UnauthorizedError,
  type CaptchaAnswer, type EnrollCodeView, type Me, type ServerView,
} from './api'

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

/** 拉取一次节点列表。401（会话失效）时停止轮询并回到登录页。 */
export async function refresh() {
  try {
    state.servers = await listServers()
    state.updatedAt = new Date()
    state.error = ''
    state.loaded = true
  } catch (e) {
    if (e instanceof UnauthorizedError) signedOut()
    else state.error = '无法连接面板，正在重试'
  }
}

/** 开始每 3 秒轮询；面板从内存返回实时状态，不读数据库。TODO(B): 改为 WebSocket（设计 20）。 */
export function startPolling() {
  stopPolling()
  refresh()
  timer = window.setInterval(refresh, 3000)
}

export function stopPolling() {
  if (timer) window.clearInterval(timer)
  timer = undefined
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
