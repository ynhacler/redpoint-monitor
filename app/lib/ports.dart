// 监听端口（修订第 268 条，与 Web 的 web/src/ports.ts 同一规则，设计 4.9.1）：监听范围、常见服务名、对外监听的高风险端口。
// 监听范围只说明端口绑定在哪些地址，不等于能从公网访问（还取决于防火墙与安全组）；App 只读，端口备注与基线只在 Web。
import 'models.dart';

/// 一个监听端口（节点详情 latest.ports）
class ListenPort {
  /// 创建
  const ListenPort(this.port, this.proto, this.addrs);

  /// 端口号
  final int port;

  /// tcp / udp
  final String proto;

  /// 监听地址
  final List<String> addrs;

  /// 节点详情中的监听端口；旧版 Agent 没有该字段时为 null
  static List<ListenPort>? of(ServerView s) {
    final latest = s.raw['latest'] as Map<String, dynamic>?;
    final list = latest?['ports'] as List<dynamic>?;
    if (list == null) return null;
    return [
      for (final e in list)
        ListenPort((e as Map<String, dynamic>)['port'] as int, (e['proto'] as String?) ?? 'tcp',
            [for (final a in (e['addrs'] as List<dynamic>? ?? const [])) a as String]),
    ];
  }
}

bool _loopback(String a) => a.startsWith('127.') || a == '::1';
// RFC 1918、CGNAT（100.64/10）、IPv6 ULA 与链路本地
final _private = RegExp(r'^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.|f[cd]|fe80:)', caseSensitive: false);

/// 监听范围：public 对外监听（全部地址或公网地址）、private 内网、local 仅本机
String exposure(ListenPort p) {
  if (p.addrs.any((a) => !_loopback(a) && !_private.hasMatch(a))) return 'public';
  if (p.addrs.any((a) => !_loopback(a))) return 'private';
  return 'local';
}

/// 监听范围的文字：全部地址 / 公网地址 / 内网 / 仅本机（不写“公网”，未经检测不能说可从公网访问）
String scopeText(ListenPort p) => switch (exposure(p)) {
      'public' => p.addrs.any((a) => a == '0.0.0.0' || a == '::' || a == '*') ? '全部地址' : '公网地址',
      'private' => '内网',
      _ => '仅本机',
    };

const _services = {
  'tcp/21': 'FTP', 'tcp/22': 'SSH', 'tcp/25': 'SMTP', 'tcp/53': 'DNS', 'udp/53': 'DNS', 'tcp/80': 'HTTP', 'tcp/443': 'HTTPS',
  'udp/443': 'HTTP/3', 'udp/123': 'NTP', 'tcp/1080': 'SOCKS', 'udp/1194': 'OpenVPN', 'tcp/2375': 'Docker API', 'tcp/2376': 'Docker API',
  'tcp/3000': 'Web', 'tcp/3306': 'MySQL', 'tcp/3389': 'RDP', 'tcp/5432': 'PostgreSQL', 'tcp/5900': 'VNC', 'tcp/6379': 'Redis',
  'tcp/8080': 'HTTP', 'tcp/8443': 'HTTPS', 'tcp/9090': 'Prometheus', 'tcp/9100': 'Node Exporter', 'tcp/9200': 'Elasticsearch',
  'tcp/11211': 'Memcached', 'udp/11211': 'Memcached', 'tcp/27017': 'MongoDB', 'udp/51820': 'WireGuard',
};

/// 常见端口对应的服务名；未知时为空
String serviceName(ListenPort p) => _services['${p.proto}/${p.port}'] ?? '';

const _risky = {
  'tcp/2375', 'tcp/2376', 'tcp/3306', 'tcp/3389', 'tcp/5432', 'tcp/5900', 'tcp/6379', 'tcp/9200', 'tcp/11211', 'udp/11211', 'tcp/27017',
  'tcp/1433', 'tcp/5672',
};

/// 对外监听的高风险端口（数据库、缓存、Docker API、远程桌面等）
bool isRisky(ListenPort p) => exposure(p) == 'public' && _risky.contains('${p.proto}/${p.port}');

/// 排序：高风险 → 对外监听 → 内网 → 仅本机，同级按端口
List<ListenPort> sortPorts(List<ListenPort> list) {
  int rank(ListenPort p) => isRisky(p) ? 0 : switch (exposure(p)) { 'public' => 1, 'private' => 2, _ => 3 };
  return [...list]..sort((a, b) {
      final r = rank(a) - rank(b);
      if (r != 0) return r;
      return a.port != b.port ? a.port - b.port : a.proto.compareTo(b.proto);
    });
}
