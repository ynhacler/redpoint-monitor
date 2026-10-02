// 深浅色模式（设计 41.2.1）：默认跟随系统，用户可固定为浅色或深色。
// 选择只保存在本浏览器（localStorage），是个人偏好，不需要同步到面板。
import { ref } from 'vue'

export type ThemeMode = 'system' | 'light' | 'dark'
const KEY = 'vpsmon.theme'

function load(): ThemeMode {
  try {
    const v = localStorage.getItem(KEY)
    return v === 'light' || v === 'dark' ? v : 'system'
  } catch {
    return 'system' // 隐私模式下 storage 不可用
  }
}

export const themeMode = ref<ThemeMode>(load())

/** 应用主题：system 时移除 data-theme，交给 prefers-color-scheme；并通知图表重绘 */
export function applyTheme(mode: ThemeMode) {
  themeMode.value = mode
  if (mode === 'system') document.documentElement.removeAttribute('data-theme')
  else document.documentElement.setAttribute('data-theme', mode)
  try {
    localStorage.setItem(KEY, mode)
  } catch {
    /* storage 不可用时只在本次会话生效 */
  }
  window.dispatchEvent(new Event('vpsmon-theme'))
}

/** 依次切换：跟随系统 → 浅色 → 深色 */
export function cycleTheme() {
  applyTheme(themeMode.value === 'system' ? 'light' : themeMode.value === 'light' ? 'dark' : 'system')
}
