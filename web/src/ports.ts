// 监听端口的展示（设计 4.9.1）：监听范围、常见服务名、对外监听的高风险端口。
// 监听范围只说明端口绑定在哪些地址，不等于能从公网访问（还取决于防火墙与安全组）；是否可达以面板的检测结果为准（修订第 227 条）。
import type { ListenPort } from './api'

export type Exposure = 'public' | 'private' | 'local'

const isLoopback = (a: string) => a.startsWith('127.') || a === '::1'
// RFC 1918、CGNAT（100.64/10，Tailscale 等）、IPv6 ULA 与链路本地
const isPrivate = (a: string) =>
  /^10\./.test(a) || /^192\.168\./.test(a) || /^172\.(1[6-9]|2\d|3[01])\./.test(a) ||
  /^100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\./.test(a) || /^f[cd]/i.test(a) || /^fe80:/i.test(a)

/** 监听范围：任一地址为 0.0.0.0 / :: 或公网地址为对外监听（public）；只在内网地址监听为内网；只在回环地址为仅本机 */
export function exposure(p: ListenPort): Exposure {
  if (p.addrs.some((a) => !isLoopback(a) && !isPrivate(a))) return 'public'
  if (p.addrs.some((a) => !isLoopback(a))) return 'private'
  return 'local'
}

/** 监听范围名称（修订第 227 条：不写“公网”，未经检测不能说可从公网访问） */
export const exposureNames: Record<Exposure, string> = { public: '对外监听', private: '内网', local: '仅本机' }

/** 对外监听的细分：all 为全部地址（0.0.0.0 / ::），addr 为指定的公网地址 */
export const publicKind = (p: ListenPort): 'all' | 'addr' => (p.addrs.some((a) => a === '0.0.0.0' || a === '::' || a === '*') ? 'all' : 'addr')

/** 协议族：按监听地址判断（IPv6 的 :: 是否同时接受 IPv4 取决于系统设置，这里不推断） */
export function family(p: ListenPort): string {
  const v6 = p.addrs.some((a) => a.includes(':'))
  const v4 = p.addrs.some((a) => !a.includes(':'))
  return v4 && v6 ? 'IPv4 + IPv6' : v6 ? 'IPv6' : 'IPv4'
}

/** 面板检测的结果状态名称（修订第 171 条）；没有检测过的端口为“未检测” */
export const reachNames: Record<string, string> = { open: '已验证可达', refused: '已验证不可达（被拒绝）', timeout: '已验证不可达（超时，多为被拦截）', error: '检测失败', skipped: '未检测（UDP）' }

const services: Record<string, string> = {
  'tcp/21': 'FTP', 'tcp/22': 'SSH', 'tcp/25': 'SMTP', 'tcp/53': 'DNS', 'udp/53': 'DNS', 'udp/67': 'DHCP', 'udp/68': 'DHCP',
  'tcp/80': 'HTTP', 'tcp/110': 'POP3', 'udp/123': 'NTP', 'tcp/143': 'IMAP', 'tcp/443': 'HTTPS', 'udp/443': 'HTTP/3',
  'tcp/465': 'SMTPS', 'tcp/587': 'SMTP', 'tcp/993': 'IMAPS', 'tcp/995': 'POP3S', 'tcp/1080': 'SOCKS',
  'udp/1194': 'OpenVPN', 'tcp/1433': 'SQL Server', 'tcp/2375': 'Docker API', 'tcp/2376': 'Docker API',
  'tcp/3000': 'Web', 'tcp/3306': 'MySQL', 'tcp/3389': 'RDP', 'tcp/5432': 'PostgreSQL', 'tcp/5672': 'RabbitMQ',
  'tcp/5900': 'VNC', 'tcp/6379': 'Redis', 'tcp/8080': 'HTTP', 'tcp/8443': 'HTTPS', 'tcp/9000': 'Web',
  'tcp/9090': 'Prometheus', 'tcp/9100': 'Node Exporter', 'tcp/9200': 'Elasticsearch', 'tcp/11211': 'Memcached',
  'udp/11211': 'Memcached', 'tcp/27017': 'MongoDB', 'udp/51820': 'WireGuard',
}

/** 常见端口对应的服务名，如 tcp/22 为 SSH；未知时为空 */
export const serviceName = (p: ListenPort) => services[`${p.proto}/${p.port}`] ?? ''

// 数据库、缓存、远程管理等不应直接对公网开放的端口
const risky = new Set(['tcp/2375', 'tcp/2376', 'tcp/3306', 'tcp/3389', 'tcp/5432', 'tcp/5900', 'tcp/6379', 'tcp/9200',
  'tcp/11211', 'udp/11211', 'tcp/27017', 'tcp/1433', 'tcp/5672'])

/** 对外监听的高风险端口（数据库、缓存、Docker API、远程桌面等） */
export const isRisky = (p: ListenPort) => exposure(p) === 'public' && risky.has(`${p.proto}/${p.port}`)

/** 排序：高风险 → 对外监听 → 内网 → 仅本机，同级按端口 */
export function sortPorts(list: ListenPort[]): ListenPort[] {
  const rank = (p: ListenPort) => (isRisky(p) ? 0 : { public: 1, private: 2, local: 3 }[exposure(p)])
  return [...list].sort((a, b) => rank(a) - rank(b) || a.port - b.port || a.proto.localeCompare(b.proto))
}
