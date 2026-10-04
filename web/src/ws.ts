// 实时事件（设计 20）：连接面板的 /ws，按事件类型分发给订阅者。
// 有订阅者时才连接，最后一个订阅者取消后断开；断线后按 1 秒起、最长 30 秒的退避自动重连。
// 断线期间的事件不补发：订阅方应在 connected 变为 true 时重新拉取一次数据，并保留低频轮询兜底。
import { ref } from 'vue'
import type { WsEvent } from './api.gen'

type Handler = (ev: WsEvent) => void

/** 当前是否已连接 */
export const connected = ref(false)

const handlers = new Map<string, Set<Handler>>()
let socket: WebSocket | null = null
let retry = 0
let retryTimer: number | undefined

function subscribers() {
  let n = 0
  for (const s of handlers.values()) n += s.size
  return n
}

function connect() {
  if (socket || subscribers() === 0) return
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const ws = new WebSocket(`${proto}//${location.host}/ws`)
  socket = ws
  // 主动断开后旧连接的回调可能晚于新连接建立，只处理当前连接的事件
  ws.onopen = () => {
    if (socket !== ws) return
    retry = 0
    connected.value = true
  }
  ws.onmessage = (m) => {
    let ev: WsEvent
    try {
      ev = JSON.parse(m.data)
    } catch {
      return
    }
    for (const h of handlers.get(ev.type) ?? []) h(ev)
  }
  ws.onclose = () => {
    if (socket !== ws) return
    socket = null
    connected.value = false
    if (subscribers() === 0) return
    // 会话失效时握手返回 401：页面上的其他请求会触发统一的 401 处理回到登录页；这里只按退避重试
    const delay = Math.min(30_000, 1000 * 2 ** retry++) * (0.8 + Math.random() * 0.4)
    retryTimer = window.setTimeout(connect, delay)
  }
}

function disconnect() {
  if (retryTimer) window.clearTimeout(retryTimer)
  retryTimer = undefined
  retry = 0
  socket?.close(1000)
  socket = null
  connected.value = false
}

/** 订阅一种事件，返回取消订阅的函数（在组件卸载时调用） */
export function subscribe(type: WsEvent['type'], h: Handler): () => void {
  if (!handlers.has(type)) handlers.set(type, new Set())
  handlers.get(type)!.add(h)
  connect()
  return () => {
    handlers.get(type)?.delete(h)
    if (subscribers() === 0) disconnect()
  }
}
