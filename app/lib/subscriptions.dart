// 订阅（设计 47，修订第 213 条）：App 只读展示。下次扣费日由面板计算（next_charge）；扣费说明、每月折算与
// 首字母、品牌色与 Web 的 web/src/subscriptions.ts 一致。
import 'catalog.gen.dart';

/// SIM 电话卡的号码
class SubPhone {
  /// 创建号码
  const SubPhone(this.cc, this.number);

  /// 区号，如 +86
  final String cc;

  /// 号码
  final String number;
}

/// VPS 的地址与配置（数值为 0 表示没有填写）
class SubVPS {
  /// 创建 VPS 信息
  const SubVPS({this.ips = const [], this.cpu = 0, this.memoryGB = 0, this.diskGB = 0, this.bandwidthMbps = 0, this.trafficGB = 0, this.location = ''});

  /// 地址
  final List<String> ips;

  /// 核数
  final int cpu;

  /// 内存（GB）
  final double memoryGB;

  /// 硬盘（GB）
  final int diskGB;

  /// 带宽（Mbps）
  final int bandwidthMbps;

  /// 每月流量（GB）
  final int trafficGB;

  /// 机房 / 地区
  final String location;

  /// 配置摘要：“2 核 · 4 GB · 80 GB · 1 TB/月 · 东京”
  String get summary {
    String n(double v) => v == v.roundToDouble() ? v.round().toString() : v.toString();
    return [
      if (cpu > 0) '$cpu 核',
      if (memoryGB > 0) '${n(memoryGB)} GB 内存',
      if (diskGB > 0) '$diskGB GB 硬盘',
      if (bandwidthMbps > 0) bandwidthMbps >= 1000 && bandwidthMbps % 1000 == 0 ? '${bandwidthMbps ~/ 1000} Gbps' : '$bandwidthMbps Mbps',
      if (trafficGB > 0) '${trafficGB >= 1000 && trafficGB % 1000 == 0 ? '${trafficGB ~/ 1000} TB' : '$trafficGB GB'}/月',
      if (location.isNotEmpty) location,
    ].join(' · ');
  }
}

/// 一条订阅
class Subscription {
  /// 创建订阅
  const Subscription({
    required this.id,
    required this.name,
    this.brand = '',
    required this.category,
    required this.priceCents,
    required this.currency,
    required this.cycle,
    this.trialDays = 0,
    required this.startDate,
    this.endDate = '',
    this.remindDays = 0,
    this.note = '',
    this.phones = const [],
    this.vps = const SubVPS(),
    this.nextCharge = '',
    this.ended = false,
    this.logoVersion = 0,
  });

  /// 编号
  final int id;

  /// 名称
  final String name;

  /// 目录键
  final String brand;

  /// 类别
  final String category;

  /// 每期价格（分）
  final int priceCents;

  /// 币种
  final String currency;

  /// 周期
  final String cycle;

  /// 试用天数
  final int trialDays;

  /// 开始日期 YYYY-MM-DD
  final String startDate;

  /// 结束日期，空表示仍在订阅
  final String endDate;

  /// 提前几天提醒扣费（0 表示不提醒）
  final int remindDays;

  /// 备注
  final String note;

  /// SIM 卡号码
  final List<SubPhone> phones;

  /// VPS 信息
  final SubVPS vps;

  /// 下次扣费日 YYYY-MM-DD；空表示没有
  final String nextCharge;

  /// 已结束
  final bool ended;

  /// 上传图标的版本，0 表示没有
  final int logoVersion;

  /// 从接口 JSON 创建
  factory Subscription.fromJson(Map<String, dynamic> j) {
    final v = (j['vps'] as Map<String, dynamic>?) ?? const {};
    return Subscription(
      id: (j['id'] as num).toInt(),
      name: j['name'] as String? ?? '',
      brand: j['brand'] as String? ?? '',
      category: j['category'] as String? ?? 'other',
      priceCents: (j['price_cents'] as num?)?.toInt() ?? 0,
      currency: j['currency'] as String? ?? 'CNY',
      cycle: j['cycle'] as String? ?? 'monthly',
      trialDays: (j['trial_days'] as num?)?.toInt() ?? 0,
      startDate: j['start_date'] as String? ?? '',
      endDate: j['end_date'] as String? ?? '',
      remindDays: (j['remind_days'] as num?)?.toInt() ?? 0,
      note: j['note'] as String? ?? '',
      phones: [
        for (final p in (j['phones'] as List<dynamic>? ?? const []))
          SubPhone((p as Map<String, dynamic>)['cc'] as String? ?? '', p['number'] as String? ?? ''),
      ],
      vps: SubVPS(
        ips: [for (final ip in (v['ips'] as List<dynamic>? ?? const [])) ip as String],
        cpu: (v['cpu'] as num?)?.toInt() ?? 0,
        memoryGB: (v['memory_gb'] as num?)?.toDouble() ?? 0,
        diskGB: (v['disk_gb'] as num?)?.toInt() ?? 0,
        bandwidthMbps: (v['bandwidth_mbps'] as num?)?.toInt() ?? 0,
        trafficGB: (v['traffic_gb'] as num?)?.toInt() ?? 0,
        location: v['location'] as String? ?? '',
      ),
      nextCharge: j['next_charge'] as String? ?? '',
      ended: j['ended'] as bool? ?? false,
      logoVersion: (j['logo_version'] as num?)?.toInt() ?? 0,
    );
  }

  /// 目录项（自定义的为 null）
  CatalogEntry? get entry => brand.isEmpty ? null : catalog[brand];

  /// 单色标志
  GlyphIcon? get glyph => brand.isEmpty ? null : brandIcons[entry?.icon ?? brand];

  /// 首字母
  String get letter {
    final e = entry;
    if (e?.letter != null) return e!.letter!;
    final m = RegExp(r'[A-Za-z0-9一-鿿]').firstMatch((e?.name ?? name).trim());
    return (m?.group(0) ?? '?').toUpperCase();
  }

  /// 按扣费周期折算为每月花费（分）：每周按 52 / 12，一次性为 0
  double get monthlyCents => switch (cycle) {
        'once' => 0,
        'weekly' => priceCents * 52 / 12,
        'quarterly' => priceCents / 3,
        'semiannually' => priceCents / 6,
        'annually' => priceCents / 12,
        _ => priceCents.toDouble(),
      };
}

DateTime? _date(String s) {
  final p = s.split('-');
  if (p.length != 3) return null;
  return DateTime(int.parse(p[0]), int.parse(p[1]), int.parse(p[2]));
}

/// 距下次扣费的天数（今天为 0）；没有下次扣费时为 null
int? daysUntilCharge(Subscription s, {DateTime? now}) {
  final d = _date(s.nextCharge);
  if (d == null) return null;
  final n = now ?? DateTime.now();
  return d.difference(DateTime(n.year, n.month, n.day)).inDays;
}

/// 距结束订阅的天数（今天为 0）；没有结束日期或已结束时为 null
int? daysUntilEnd(Subscription s, {DateTime? now}) {
  final d = _date(s.endDate);
  if (d == null || s.ended) return null;
  final n = now ?? DateTime.now();
  return d.difference(DateTime(n.year, n.month, n.day)).inDays;
}

/// 快到期（修订第 218 条）：7 天内扣费或 7 天内结束；返回最近的天数，不是时为 null
int? dueSoonDays(Subscription s, {DateTime? now}) {
  if (s.ended) return null;
  final xs = [daysUntilCharge(s, now: now), daysUntilEnd(s, now: now)].whereType<int>().where((d) => d >= 0 && d <= 7);
  return xs.isEmpty ? null : xs.reduce((a, b) => a < b ? a : b);
}

const _weekday = ['每周一扣费', '每周二扣费', '每周三扣费', '每周四扣费', '每周五扣费', '每周六扣费', '每周日扣费'];

/// 扣费说明（与 Web 的 chargeText 相同）：“每月 5 日扣费”“每年 10 月 26 日扣费”“每周三扣费”“已结束订阅”“试用中，11 月 8 日开始扣费”
String chargeText(Subscription s, {DateTime? now}) {
  if (s.ended) return '已结束订阅';
  final start = _date(s.startDate);
  if (start == null) return cycleNames[s.cycle] ?? s.cycle;
  final first = start.add(Duration(days: s.trialDays));
  final today = now ?? DateTime.now();
  if (s.trialDays > 0 && first.isAfter(today)) return '试用中，${first.month} 月 ${first.day} 日开始扣费';
  final next = _date(s.nextCharge);
  switch (s.cycle) {
    case 'once':
      return next != null ? '${next.month} 月 ${next.day} 日扣费' : '一次性，已扣费';
    case 'weekly':
      return _weekday[first.weekday - 1];
    case 'monthly':
      return '每月 ${first.day} 日扣费';
    case 'annually':
      return '每年 ${first.month} 月 ${first.day} 日扣费';
  }
  final c = cycleNames[s.cycle] ?? s.cycle;
  return next != null ? '$c，下次 ${next.month} 月 ${next.day} 日' : c;
}

/// 按币种合计每月折算花费（分），金额大的在前；只算未结束的
List<(String, double)> monthlyByCurrency(Iterable<Subscription> list) {
  final m = <String, double>{};
  for (final s in list) {
    if (s.ended) continue;
    m[s.currency] = (m[s.currency] ?? 0) + s.monthlyCents;
  }
  return m.entries.map((e) => (e.key, e.value)).toList()..sort((a, b) => b.$2.compareTo(a.$2));
}
