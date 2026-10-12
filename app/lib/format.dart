// 数字、单位与时间的展示格式（设计 41.4.1）：与 web/src/format.ts 同一口径，Web 与 App 显示一致。

/// 未知数据显示 “—”，不显示 0
const dash = '—';

String _num(double n) => n < 10 && n % 1 != 0 ? n.toStringAsFixed(1) : n.round().toString();

/// 字节数，十进制单位（1 GB = 10⁹ 字节，设计 5.8）；perSec 时追加 “/s”。
String fmtBytes(num? n, {bool perSec = false}) {
  if (n == null || n.isNaN) return dash;
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  var v = n.toDouble();
  var i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return '${i == 0 ? v.round() : _num(v)} ${units[i]}${perSec ? '/s' : ''}';
}

/// 流量按节点的单位口径（设计 5.8）：decimal 用 GB，binary 用 GiB。
String fmtTraffic(num? n, String unit) {
  if (unit != 'binary') return fmtBytes(n);
  if (n == null || n.isNaN) return dash;
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  var v = n.toDouble();
  var i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return '${i == 0 ? v.round() : _num(v)} ${units[i]}';
}

/// 百分比取整，小于 1% 显示 “<1%”。
String fmtPct(num? p) {
  if (p == null || p.isNaN) return dash;
  if (p > 0 && p < 1) return '<1%';
  return '${p.round()}%';
}

String _p(int n) => n.toString().padLeft(2, '0');

/// 时间：今天 16:31；本年 10-02 16:31；更早 2025-10-02。
String fmtTime(int? unix, {DateTime? now}) {
  if (unix == null || unix == 0) return dash;
  final d = DateTime.fromMillisecondsSinceEpoch(unix * 1000);
  final n = now ?? DateTime.now();
  final hm = '${_p(d.hour)}:${_p(d.minute)}';
  if (d.year == n.year && d.month == n.month && d.day == n.day) return hm;
  if (d.year == n.year) return '${_p(d.month)}-${_p(d.day)} $hm';
  return '${d.year}-${_p(d.month)}-${_p(d.day)}';
}

/// 时钟时间到秒，用于“最后更新 16:32:18”（设计 1.5.10）。
String fmtClock(DateTime d) => '${_p(d.hour)}:${_p(d.minute)}:${_p(d.second)}';

/// 时长：“4 分钟”“2 小时”“3 天”。
String fmtDuration(num? seconds) {
  if (seconds == null || seconds < 0) return dash;
  if (seconds < 60) return '${seconds.floor()} 秒';
  if (seconds < 3600) return '${(seconds / 60).floor()} 分钟';
  if (seconds < 86400) return '${(seconds / 3600).floor()} 小时';
  return '${(seconds / 86400).floor()} 天';
}

/// 国家代码 → 国旗 emoji（区域指示符号）；无效代码返回空串。
String flagEmoji(String country) {
  final c = country.toUpperCase();
  if (!RegExp(r'^[A-Z]{2}$').hasMatch(c)) return '';
  return String.fromCharCodes(c.codeUnits.map((u) => 0x1F1E6 + u - 0x41));
}

/// 卡片中的简短字节数：单位只写一个字母（1.2 G、350 M），与 Web 的 fmtBytesShort 一致（设计 41.4.1）。
String fmtBytesShort(num? n, {bool perSec = false}) {
  if (n == null || n.isNaN) return dash;
  const units = ['B', 'K', 'M', 'G', 'T', 'P'];
  var v = n.toDouble();
  var i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  final s = i == 0 || v >= 100 ? v.round().toString() : ((v * 10).round() / 10).toString().replaceAll(RegExp(r'\.0$'), '');
  return '$s ${units[i]}${perSec ? '/s' : ''}';
}

/// 开机时间（卡片）：不足 1 天按小时，1～999 天按天，更长按年；与 Web 的 fmtUptimeShort 一致。
String fmtUptimeShort(num? seconds) {
  if (seconds == null || seconds < 0) return dash;
  final days = seconds / 86400;
  if (days < 1) {
    final h = (seconds / 3600).floor();
    return h < 1 ? '<1小时' : '$h小时';
  }
  if (days <= 999) return '${days.floor()}天';
  return '${(days / 365).toStringAsFixed(1)}年';
}

/// 延迟：≥ 1 秒显示秒，< 10 ms 保留 1 位小数（与 Web 的 fmtLatency 一致）。
String fmtLatency(num? ms) {
  if (ms == null || ms.isNaN) return '—';
  if (ms >= 1000) return '${(ms / 1000).toStringAsFixed(1)} s';
  if (ms < 10 && ms != ms.roundToDouble()) return '${(ms * 10).round() / 10} ms';
  return '${ms.round()} ms';
}

/// 测速全部失败时的原因（与 Web 节点详情的说明一致，修订第 132 条起含 dns / blocked）。
String fmtProbeError(String code) => switch (code) {
      'timeout' => '超时',
      'refused' => '连接被拒绝',
      'unreachable' => '网络不可达',
      'icmp_unavailable' => '本机不允许 ICMP',
      'dns' => '域名解析失败',
      'blocked' => '域名只解析到内网地址，已拒绝',
      '' => '',
      _ => '失败',
    };

/// 测速目标的地址说明，如 “TCP 80 → 1.2.3.4”、“ICMP → speed.example.cn”。
String fmtProbeWhere(String proto, int port, String address) => proto == 'icmp' ? 'ICMP → $address' : 'TCP $port → $address';

/// 到期标签（与 Web 的 fmtExpiry 一致，修订第 138 条）：已过期 / 今天到期 / 剩 N 天（100 天内）/ 到期日；
/// 一次性付款且没有到期日为“永久”；没有到期日的周期付款返回 null。level：bad（7 天内或已过期）、warn（30 天内）、空。
({String text, String level, int? days})? fmtExpiry(String expireDate, String billingPeriod, {DateTime? now}) {
  if (expireDate.isEmpty) return billingPeriod == 'one_time' ? (text: '永久', level: '', days: null) : null;
  final d = DateTime.tryParse(expireDate);
  if (d == null) return null;
  final n = now ?? DateTime.now();
  // 用 UTC 日期相减，避免夏令时那天只有 23 小时
  final days = DateTime.utc(d.year, d.month, d.day).difference(DateTime.utc(n.year, n.month, n.day)).inDays;
  if (days < 0) return (text: '已过期', level: 'bad', days: days);
  final level = days <= 7 ? 'bad' : (days <= 30 ? 'warn' : '');
  if (days == 0) return (text: '今天到期', level: level, days: 0);
  return (text: days < 100 ? '剩$days天' : expireDate, level: level, days: days);
}

/// 金额（分）：USD 显示 $、CNY 显示 ¥，其他币种写代码，如 “$3.21”“¥120.00”“EUR 9.90”。
String fmtMoney(int? cents, String currency) {
  if (cents == null) return dash;
  final v = (cents / 100).toStringAsFixed(2);
  return switch (currency) { 'USD' => '\$$v', 'CNY' => '¥$v', '' => v, _ => '$currency $v' };
}
