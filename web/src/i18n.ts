// 界面语言（设计 52，修订第 201 条）：简体中文为源语言，其他语言按“中文原文 → 译文”对照（locales/*.ts）。
// 用法：t('节点')、t('{n} 台', { n })。没有译文的文字显示中文原文，所以可以按页面分批翻译。
// locale 是响应式的：模板中调用 t() 的地方在切换语言后自动更新。语言由“系统 → 网站配置”设置（全站一致）。
import { ref } from 'vue'
import { en } from './locales/en'

/** 支持的语言 */
export type Locale = 'zh' | 'en'

/** 语言名称（选择框中显示） */
export const localeNames: Record<Locale, string> = { zh: '简体中文', en: 'English' }

const dicts: Record<Locale, Record<string, string>> = { zh: {}, en }

/** 当前语言 */
export const locale = ref<Locale>('zh')

/** 设置语言：未知值按中文处理；同时设置 <html lang>（读屏与浏览器翻译据此判断） */
export function setLocale(l: string) {
  locale.value = l === 'en' ? 'en' : 'zh'
  document.documentElement.lang = locale.value === 'en' ? 'en' : 'zh-CN'
}

/** 翻译：key 为中文原文；vars 替换其中的 {名称} */
export function t(key: string, vars?: Record<string, string | number | null | undefined>): string {
  let s = dicts[locale.value][key] ?? key
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replaceAll(`{${k}}`, String(v ?? ''))
  return s
}
