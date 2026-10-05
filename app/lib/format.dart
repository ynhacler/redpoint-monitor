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
