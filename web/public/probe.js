// 连通性测试的探测脚本（设计 48，修订第 190 条）。
// 运行在 sandbox iframe 中（不带 allow-same-origin：拿不到面板的 Cookie、页面与存储），面板页面用 postMessage 下发目标，
// 这里用 no-cors 的 fetch 计时（只看连接与响应是否到达，不读取内容），结果用 postMessage 交回。
// 每个目标先请求一次（含 DNS、TLS 握手），再请求一次取较小值作为延迟；失败时区分超时与连接失败。
// 目标可带备用地址：前一个地址“连接失败”时依次改测下一个（修订第 203 条）。Cloudflare 对可疑来源返回的人机验证页
// 带 Cross-Origin-Resource-Policy: same-origin，浏览器拒收，看起来和连不上一样；换同一服务的另一个地址即可。超时不换。
'use strict'

/** 对一个地址计时一次：成功返回毫秒数；失败返回 'timeout'（超时）或 'error'（连接失败，或浏览器按对方的 CORP 等规则拒收响应） */
async function once(url, timeoutMs) {
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), timeoutMs)
  const t0 = performance.now()
  try {
    // 不加随机参数：cache: 'no-store' 已绕过缓存，而有的站点对带参数的请求跳转到 http（如京东），被浏览器拦截（修订第 194 条）
    await fetch(url, {
      mode: 'no-cors', cache: 'no-store', credentials: 'omit', redirect: 'follow', referrerPolicy: 'no-referrer', signal: ctrl.signal,
    })
    return Math.round(performance.now() - t0)
  } catch {
    return ctrl.signal.aborted ? 'timeout' : 'error'
  } finally {
    clearTimeout(timer)
  }
}

async function measure(urls, timeoutMs) {
  for (const url of urls) {
    const first = await once(url, timeoutMs)
    if (first === 'timeout') return { ms: null, fail: first }
    if (first === 'error') continue
    const second = await once(url, timeoutMs)
    return { ms: typeof second === 'number' ? Math.min(first, second) : first }
  }
  return { ms: null, fail: 'error' }
}

window.addEventListener('message', async (e) => {
  // 只接受父页面的消息；目标只能是 https 地址，每个目标最多 3 个地址
  if (e.source !== window.parent || !e.data || e.data.type !== 'run' || !Array.isArray(e.data.targets)) return
  const timeoutMs = Math.min(Math.max(Number(e.data.timeoutMs) || 5000, 1000), 15000)
  const https = (u) => typeof u === 'string' && u.startsWith('https://')
  // 旧版面板页面只发 url（浏览器缓存了旧页面时）
  const queue = e.data.targets
    .filter((t) => t && typeof t.id === 'string')
    .map((t) => ({ id: t.id, urls: Array.isArray(t.urls) ? t.urls : [t.url] }))
    .filter((t) => t.urls.length > 0 && t.urls.length <= 3 && t.urls.every(https))
  const run = e.data.run
  // 同时最多 6 个请求（浏览器对每个域名的并发也有限制），避免互相拖慢计时
  const worker = async () => {
    while (queue.length) {
      const t = queue.shift()
      const { ms, fail } = await measure(t.urls, timeoutMs)
      window.parent.postMessage({ type: 'result', run, id: t.id, ms, fail }, '*')
    }
  }
  await Promise.all(Array.from({ length: 6 }, worker))
  window.parent.postMessage({ type: 'done', run }, '*')
})
window.parent.postMessage({ type: 'ready' }, '*')
