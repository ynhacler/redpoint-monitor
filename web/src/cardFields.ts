// 节点卡片显示字段（手机 UI 规范 P2，修订第 288 条）：可以隐藏到期、运行时间、负载、温度、线路标志、网络、IO 与流量。
// 名称、状态与 CPU / 内存 / 磁盘始终显示；隐藏的到期与流量在需要关注时（7 天内到期或已过期、流量用到 80% 以上）仍会显示，
// 不会因为精简而漏看问题。选择只保存在本浏览器（localStorage），是个人偏好，不同步到面板。
import { reactive } from 'vue'

/** 可隐藏的字段 */
export type CardField = 'expiry' | 'uptime' | 'load' | 'temp' | 'badges' | 'network' | 'io' | 'traffic'

/** 设置抽屉中的字段与名称，按卡片上的顺序 */
export const cardFieldOptions: { k: CardField; t: string; hint?: string }[] = [
  { k: 'expiry', t: '到期时间', hint: '7 天内到期或已过期时仍显示' },
  { k: 'uptime', t: '运行时间' },
  { k: 'load', t: '负载' },
  { k: 'temp', t: '温度' },
  { k: 'badges', t: 'v4 / v6 与线路' },
  { k: 'network', t: '网络速率' },
  { k: 'io', t: '磁盘 IO', hint: '只在电脑宽度的卡片上' },
  { k: 'traffic', t: '流量', hint: '用到 80% 以上时仍显示' },
]

const KEY = 'vpsmon.cardHidden'

function read(): Set<CardField> {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '[]')
    if (Array.isArray(v)) return new Set(v.filter((x): x is CardField => cardFieldOptions.some((o) => o.k === x)))
  } catch {
    // 隐私模式或数据损坏：全部显示
  }
  return new Set()
}

/** 当前隐藏的字段（全局共享，各卡片响应变化） */
export const cardHidden = reactive(read())

/** 字段是否显示 */
export function cardShows(k: CardField): boolean {
  return !cardHidden.has(k)
}

/** 切换字段显示并保存 */
export function toggleCardField(k: CardField) {
  if (cardHidden.has(k)) cardHidden.delete(k)
  else cardHidden.add(k)
  save()
}

/** 恢复全部显示 */
export function resetCardFields() {
  cardHidden.clear()
  save()
}

function save() {
  try {
    localStorage.setItem(KEY, JSON.stringify([...cardHidden]))
  } catch {
    // 无法保存时只在本次打开期间生效
  }
}
