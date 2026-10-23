// 国家 / 地区（设计 1.2.3）：代码为 ISO 3166-1 两位字母，国旗图片来自 flag-icons（MIT）。
// 名称由浏览器内置的 Intl.DisplayNames 提供（跟随界面语言），不需要额外的名称数据。
import { locale, type Locale } from './i18n'

// 只收录两位字母代码的国旗（排除 gb-eng 等地区旗与 eu、un 等组织旗）。
// 每个国旗是单独的资源文件，页面只加载实际用到的几个。
// no-inline：小图标也不要内联成 data URL，否则两百多个国旗会全部打进 JS
const files = import.meta.glob('/node_modules/flag-icons/flags/4x3/??.svg', {
  eager: true, query: '?url&no-inline', import: 'default',
}) as Record<string, string>

const flagUrls: Record<string, string> = {}
for (const [path, url] of Object.entries(files)) {
  const code = path.match(/\/([a-z]{2})\.svg$/)?.[1]
  if (code && code !== 'eu' && code !== 'un') flagUrls[code.toUpperCase()] = url
}

/** 国旗图片地址；没有对应国旗时返回 undefined */
export const flagUrl = (code: string) => flagUrls[code.toUpperCase()]

// 名称跟随界面语言（设计 52）：每种语言建一个 DisplayNames
const displayNames: Partial<Record<Locale, Intl.DisplayNames | null>> = {}
function names(): Intl.DisplayNames | null {
  const l = locale.value
  if (displayNames[l] === undefined) {
    try {
      displayNames[l] = new Intl.DisplayNames([l === 'en' ? 'en' : 'zh-CN'], { type: 'region' })
    } catch {
      displayNames[l] = null // 极旧的浏览器：退回显示代码
    }
  }
  return displayNames[l] ?? null
}

/** 国家 / 地区的名称（界面语言），如 JP → 日本 / Japan */
export function countryName(code: string): string {
  if (!code) return ''
  try {
    return names()?.of(code.toUpperCase()) ?? code
  } catch {
    return code
  }
}

/** VPS 常见所在地，排在下拉列表最前面 */
const common = ['HK', 'JP', 'SG', 'US', 'KR', 'TW', 'DE', 'NL', 'GB', 'FR', 'CA', 'MO', 'RU', 'CN', 'AU', 'IN', 'VN', 'MY', 'TH', 'TR', 'AE', 'BR']

/** 下拉选项：常用在前，其余按名称排序 */
export function countryOptions(): { common: { code: string; name: string }[]; all: { code: string; name: string }[] } {
  const opt = (code: string) => ({ code, name: countryName(code) })
  const all = Object.keys(flagUrls).filter((c) => !common.includes(c)).map(opt)
  all.sort((a, b) => a.name.localeCompare(b.name, locale.value === 'en' ? 'en' : 'zh-CN'))
  return { common: common.filter((c) => flagUrls[c]).map(opt), all }
}
