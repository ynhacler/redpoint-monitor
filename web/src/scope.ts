// 节点范围的文字说明（API Key 与 App AK 共用，设计 45.2、17.3）。
import { t } from './i18n'
import { state } from './store'

/** 授权范围的说明（界面语言），如“全部节点”“分组 jp” */
export function scopeText(type: string, value: string): string {
  if (type === 'group') return t('分组：{name}', { name: value })
  if (type === 'servers') {
    const ids = value.split(',').map(Number)
    const names = ids.map((id) => state.servers.find((s) => s.id === id)?.name ?? `#${id}`)
    const list = names.slice(0, 3).join(t('、'))
    return names.length > 3 ? t('节点：{list} 等 {n} 个', { list, n: names.length }) : t('节点：{list}', { list })
  }
  return t('全部节点')
}
