// 连通性测试（设计 48，修订第 190 条）：从浏览器测试当前网络到各地常用网站的连通性与延迟，用来判断分流规则是否生效、线路是否通畅。
// 测试在浏览器中进行（不经过面板与 VPS）；目标固定在这里，测的是各站点的小文件（多为 /favicon.ico），只看能否连上与耗时。
// 地址要求（修订第 194 条）：https、不跳转到 http、响应不带 Cross-Origin-Resource-Policy: same-origin / same-site
// （否则浏览器拒收，显示为“无法连接”）；claude.ai、chatgpt.com、npmjs.com 整站如此，改测同一服务的 API 地址。
// 等级：优 < 150 ms，良 < 400 ms，慢 ≥ 400 ms，超时（5 秒内没有响应或连接失败）。

/** 一个测试目标 */
export interface Site {
  id: string
  name: string
  url: string
  /** 标志键（siteIcons.ts）；没有时用 siteLogos.ts 中的图片标志，都没有时用色块加首字母 */
  icon?: string
  color: string
  letter?: string
}

/** 一组目标 */
export interface SiteGroup {
  key: string
  name: string
  flag: string
  sites: Site[]
}

const fav = (host: string) => `https://${host}/favicon.ico`

/** 测试目标：中国、美国、全球 */
export const siteGroups: SiteGroup[] = [
  {
    key: 'cn', name: '中国', flag: '🇨🇳', sites: [
      { id: 'deepseek', name: 'DeepSeek', url: fav('chat.deepseek.com'), icon: 'deepseek', color: '#4D6BFE' },
      { id: 'douyin', name: '抖音', url: fav('www.douyin.com'), icon: 'tiktok', color: '#000000' },
      { id: 'bilibili', name: '哔哩哔哩', url: fav('www.bilibili.com'), icon: 'bilibili', color: '#00A1D6' },
      { id: 'jd', name: '京东', url: fav('www.jd.com'), color: '#E1251B', letter: 'JD' },
      { id: 'qq', name: '腾讯QQ', url: fav('www.qq.com'), icon: 'qq', color: '#1EBAFC' },
      { id: 'wechat', name: '微信', url: fav('weixin.qq.com'), icon: 'wechat', color: '#07C160' },
      { id: 'xiaohongshu', name: '小红书', url: fav('www.xiaohongshu.com'), icon: 'xiaohongshu', color: '#FF2442' },
      { id: 'weibo', name: '新浪微博', url: fav('weibo.com'), icon: 'sinaweibo', color: '#E6162D' },
      { id: 'baidu', name: '百度', url: fav('www.baidu.com'), icon: 'baidu', color: '#2932E1' },
      { id: 'netease', name: '网易', url: fav('www.163.com'), color: '#D81E06', letter: '易' },
      { id: 'taobao', name: '淘宝', url: fav('www.taobao.com'), icon: 'taobao', color: '#FF5000' },
      { id: 'xiaomi', name: '小米', url: fav('www.mi.com'), icon: 'xiaomi', color: '#FF6900' },
    ],
  },
  {
    key: 'us', name: '美国', flag: '🇺🇸', sites: [
      { id: 'apple', name: 'Apple', url: fav('www.apple.com'), icon: 'apple', color: '#000000' },
      { id: 'google', name: 'Google', url: fav('www.google.com'), icon: 'google', color: '#4285F4' },
      { id: 'youtube', name: 'YouTube', url: fav('www.youtube.com'), icon: 'youtube', color: '#FF0000' },
      { id: 'github', name: 'GitHub', url: fav('github.com'), icon: 'github', color: '#181717' },
      { id: 'cloudflare', name: 'Cloudflare', url: fav('www.cloudflare.com'), icon: 'cloudflare', color: '#F38020' },
      { id: 'claude', name: 'Claude', url: 'https://api.anthropic.com/', icon: 'claude', color: '#D97757' },
      { id: 'chatgpt', name: 'ChatGPT', url: 'https://api.openai.com/', color: '#10A37F', letter: 'G' },
      { id: 'aistudio', name: 'AI Studio', url: fav('aistudio.google.com'), icon: 'googlegemini', color: '#8E75B2' },
      { id: 'amazon', name: 'Amazon', url: fav('www.amazon.com'), color: '#FF9900', letter: 'a' },
      { id: 'bing', name: 'Bing', url: fav('www.bing.com'), color: '#258FFA', letter: 'b' },
      { id: 'steam', name: 'Steam', url: fav('store.steampowered.com'), icon: 'steam', color: '#000000' },
      { id: 'oracle', name: 'Oracle', url: fav('www.oracle.com'), color: '#C74634', letter: 'O' },
      { id: 'zoom', name: 'Zoom', url: fav('zoom.us'), icon: 'zoom', color: '#0B5CFF' },
      { id: 'facebook', name: 'Facebook', url: fav('www.facebook.com'), icon: 'facebook', color: '#0866FF' },
      { id: 'instagram', name: 'Instagram', url: fav('www.instagram.com'), icon: 'instagram', color: '#FF0069' },
      { id: 'x', name: 'X', url: fav('x.com'), icon: 'x', color: '#000000' },
      { id: 'reddit', name: 'Reddit', url: fav('www.reddit.com'), icon: 'reddit', color: '#FF4500' },
      { id: 'linkedin', name: 'LinkedIn', url: fav('www.linkedin.com'), color: '#0A66C2', letter: 'in' },
      { id: 'twitch', name: 'Twitch', url: fav('www.twitch.tv'), icon: 'twitch', color: '#9146FF' },
      { id: 'netflix', name: 'Netflix', url: fav('www.netflix.com'), icon: 'netflix', color: '#E50914' },
    ],
  },
  {
    key: 'global', name: '全球', flag: '🌐', sites: [
      { id: 'tiktok', name: 'TikTok', url: fav('www.tiktok.com'), icon: 'tiktok', color: '#000000' },
      { id: 'spotify', name: 'Spotify', url: fav('open.spotify.com'), icon: 'spotify', color: '#1ED760' },
      { id: 'npm', name: 'npm', url: 'https://registry.npmjs.org/', icon: 'npm', color: '#CB3837' },
      { id: 'docker', name: 'Docker Hub', url: fav('hub.docker.com'), icon: 'docker', color: '#2496ED' },
      { id: 'telegram', name: 'Telegram', url: fav('telegram.org'), icon: 'telegram', color: '#26A5E4' },
      { id: 'line', name: 'LINE', url: fav('line.me'), icon: 'line', color: '#00C300' },
      { id: 'naver', name: 'Naver', url: fav('www.naver.com'), icon: 'naver', color: '#03C75A' },
      { id: 'news24', name: 'News24', url: 'https://www.news24.com/images/tenants/news24/favicon.ico', color: '#E4002B', letter: 'N' },
      { id: 'noon', name: 'Noon', url: fav('www.noon.com'), color: '#FEEE00', letter: 'n' },
      { id: 'wikipedia', name: 'Wikipedia', url: fav('www.wikipedia.org'), icon: 'wikipedia', color: '#000000' },
      { id: 'bbc', name: 'BBC', url: fav('www.bbc.com'), color: '#000000', letter: 'BBC' },
      { id: 'mistral', name: 'Mistral AI', url: fav('mistral.ai'), icon: 'mistralai', color: '#FA520F' },
    ],
  },
]

/** 延迟等级 */
export type Grade = 'best' | 'good' | 'slow' | 'timeout'

/** 按延迟分级；null 为超时或不可达 */
export function grade(ms: number | null): Grade {
  if (ms == null) return 'timeout'
  return ms < 150 ? 'best' : ms < 400 ? 'good' : 'slow'
}

/** 等级名称与颜色（设计令牌） */
export const grades: Record<Grade, { name: string; color: string }> = {
  best: { name: '优', color: 'var(--ok)' },
  good: { name: '良', color: 'color-mix(in srgb, var(--ok) 55%, var(--surface))' },
  slow: { name: '慢', color: 'var(--warn)' },
  timeout: { name: '超时', color: 'var(--muted-state)' },
}

/** 每个目标保留最近的结果数（圆点个数） */
export const HISTORY = 8
