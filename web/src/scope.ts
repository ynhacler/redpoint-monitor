// 节点范围的文字说明（API Key 与 App AK 共用，设计 45.2、17.3）。
import { state } from './store'

/** 授权范围的中文说明，如“全部节点”“分组 jp” */
export function scopeText(type: string, value: string): string {
  if (type === 'group') return `分组：${value}`
  if (type === 'servers') {
    const ids = value.split(',').map(Number)
    const names = ids.map((id) => state.servers.find((s) => s.id === id)?.name ?? `#${id}`)
    return `节点：${names.slice(0, 3).join('、')}${names.length > 3 ? ` 等 ${names.length} 个` : ''}`
  }
  return '全部节点'
}
