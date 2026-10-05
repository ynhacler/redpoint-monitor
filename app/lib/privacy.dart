// 隐私模式（设计 1.5.12）：截图或给他人看 App 时隐藏公网 IP、价格、供应商等。
/// 103.1.2.12 → 103.***.***.12；IPv6 只保留第一段：2001:…
String maskIP(String ip) {
  if (ip.isEmpty) return ip;
  if (ip.contains(':')) {
    final first = ip.split(':').first;
    return '$first:…';
  }
  final p = ip.split('.');
  if (p.length != 4) return '***';
  return '${p[0]}.***.***.${p[3]}';
}

/// 隐私模式下替换为 ***
String maskText(String s, bool private) => private && s.isNotEmpty ? '***' : s;
