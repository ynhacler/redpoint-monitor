// 监听端口的展示（设计 4.9.1）：暴露范围、常见服务名、对公网开放的高风险端口。
import type { ListenPort } from './api'

export type Exposure = 'public' | 'private' | 'local'

const isLoopback = (a: string) => a.startsWith('127.') || a === '::1'
// RFC 1918、CGNAT（100.64/10，Tailscale 等）、IPv6 ULA 与链路本地
const isPrivate = (a: string) =>
  /^10\./.test(a) || /^192\.168\./.test(a) || /^172\.(1[6-9]|2\d|3[01])\./.test(a) ||
  /^100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\./.test(a) || /^f[cd]/i.test(a) || /^fe80:/i.test(a)

/** 暴露范围：任一地址为 0.0.0.0 / :: 或公网地址即为公网；只在内网地址监听为内网；只在回环地址为本机 */
export function exposure(p: ListenPort): Exposure {
  if (p.addrs.some((a) => !isLoopback(a) && !isPrivate(a))) return 'public'
  if (p.addrs.some((a) => !isLoopback(a))) return 'private'
  return 'local'
}

export const exposureNames: Record<Exposure, string> = { public: '公网', private: '内网', local: '本机' }

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

export const serviceName = (p: ListenPort) => services[`${p.proto}/${p.port}`] ?? ''

// 数据库、缓存、远程管理等不应直接对公网开放的端口
const risky = new Set(['tcp/2375', 'tcp/2376', 'tcp/3306', 'tcp/3389', 'tcp/5432', 'tcp/5900', 'tcp/6379', 'tcp/9200',
  'tcp/11211', 'udp/11211', 'tcp/27017', 'tcp/1433', 'tcp/5672'])

/** 对公网开放的高风险端口（数据库、缓存、Docker API、远程桌面等） */
export const isRisky = (p: ListenPort) => exposure(p) === 'public' && risky.has(`${p.proto}/${p.port}`)

/** 排序：高风险 → 公网 → 内网 → 本机，同级按端口 */
export function sortPorts(list: ListenPort[]): ListenPort[] {
  const rank = (p: ListenPort) => (isRisky(p) ? 0 : { public: 1, private: 2, local: 3 }[exposure(p)])
  return [...list].sort((a, b) => rank(a) - rank(b) || a.port - b.port || a.proto.localeCompare(b.proto))
}
