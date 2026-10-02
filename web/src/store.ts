// 全局状态：开发用 Token 与节点列表的轮询。总览、列表、详情共用同一份数据，避免各页面重复请求。
// 页面还少，用 Vue 的 reactive 即可；TODO(B): 状态变复杂后考虑 Pinia（设计 3.3，需先确认依赖）。
import { computed, reactive } from 'vue'
import { getToken, listServers, setToken, UnauthorizedError, type EnrollCodeView, type ServerView } from './api'

export const state = reactive({
  /** 是否需要输入 Token（未保存或已失效） */
  needToken: !getToken(),
  servers: [] as ServerView[],
  /** 首次加载完成前为 false，用于显示“加载中” */
  loaded: false,
  /** 连接错误；上一次成功的数据继续显示（设计 43.6） */
  error: '',
  updatedAt: null as Date | null,
})

let timer: number | undefined

/** 拉取一次节点列表。401 时停止轮询并要求重新输入 Token。 */
export async function refresh() {
  try {
    state.servers = await listServers()
    state.updatedAt = new Date()
    state.error = ''
    state.loaded = true
  } catch (e) {
    if (e instanceof UnauthorizedError) {
      logout()
    } else {
      state.error = '无法连接面板，正在重试'
    }
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

export function login(token: string) {
  setToken(token.trim())
  state.needToken = false
  startPolling()
}

export function logout() {
  stopPolling()
  setToken('')
  state.needToken = true
  state.loaded = false
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
