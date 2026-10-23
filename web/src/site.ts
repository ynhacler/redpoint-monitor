// 网站配置（设计 52）：应用名称、语言与登录是否需要验证码。页面加载时先读取（登录前也可读），
// 应用名称显示在顶栏、登录页与浏览器标签，语言决定界面文字（i18n.ts）。
import { reactive } from 'vue'
import { getSite, type SiteView } from './api'
import { setLocale } from './i18n'

/** 当前网站配置；读取失败时为默认值 */
export const site = reactive<SiteView>({ app_name: 'VPS Monitor', language: 'zh', captcha: true, captcha_forced_off: false })

/** 应用新的网站配置（读取或保存之后） */
export function applySite(v: SiteView) {
  Object.assign(site, v)
  setLocale(v.language)
}

/** 页面加载时读取一次；面板暂时连不上时用默认值，不阻塞页面 */
export async function loadSite() {
  try {
    applySite(await Promise.race([getSite(), new Promise<never>((_, rej) => setTimeout(() => rej(new Error('timeout')), 3000))]))
  } catch {
    setLocale(site.language)
  }
}
