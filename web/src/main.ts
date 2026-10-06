import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import './style.css'

// 面板升级后，仍打开着的旧页面按需加载的分块已不存在（面板返回 404）：重新加载一次页面以取得新版本。
// 用 sessionStorage 记录，避免分块确实无法加载时无限刷新；存储不可用时不自动刷新。
function reloadOnce() {
  try {
    const key = 'vpsmon-chunk-reload'
    const last = Number(sessionStorage.getItem(key) ?? 0)
    if (Date.now() - last < 60_000) return
    sessionStorage.setItem(key, String(Date.now()))
    window.location.reload()
  } catch {
    /* 存储不可用：不自动刷新 */
  }
}
window.addEventListener('vite:preloadError', (e) => {
  e.preventDefault()
  reloadOnce()
})
router.onError((err) => {
  if (/dynamically imported module|Importing a module script failed|Failed to fetch/.test(String(err))) reloadOnce()
})

createApp(App).use(router).mount('#app')
