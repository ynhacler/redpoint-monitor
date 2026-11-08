// 订阅（设计 47，修订第 213 条）：App 只读展示。下次扣费日由面板计算（next_charge）；扣费说明、每月折算与
// 首字母、品牌色与 Web 的 web/src/subscriptions.ts 一致。
import 'package:flutter/painting.dart';

import 'catalog.gen.dart';
import 'tokens.dart';

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
    this.country = '',
    this.payment = '',
    this.url = '',
    this.payer = '',
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

  /// 国家 / 地区（两位代码，修订第 223 条）；旧记录可能为空
  final String country;

  /// 支付方式（paymentNames 的键）；旧记录可能为空
  final String payment;

  /// 管理或退订页面的网址（修订第 311 条）；空为未填写
  final String url;

  /// 付款人（修订第 311 条）；空为未填写
  final String payer;

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
        for (final p in (j['phones'] as List<dynamic>? ?? const [])) SubPhone((p as Map<String, dynamic>)['cc'] as String? ?? '', p['number'] as String? ?? ''),
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
      country: j['country'] as String? ?? '',
      payment: j['payment'] as String? ?? '',
      url: j['url'] as String? ?? '',
      payer: j['payer'] as String? ?? '',
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

const _cycleMonths = {'monthly': 1, 'quarterly': 3, 'semiannually': 6, 'annually': 12};

DateTime _addMonthsClamped(DateTime a, int n) {
  final first = DateTime(a.year, a.month + n, 1);
  final last = DateTime(first.year, first.month + 1, 0).day;
  return DateTime(first.year, first.month, a.day < last ? a.day : last);
}

/// 区间 [from, to)（本地日期）内的全部扣费日（与 Web 的 chargesBetween 相同）
List<DateTime> chargesBetween(Subscription s, DateTime from, DateTime to) {
  final start = _date(s.startDate);
  if (start == null) return const [];
  final first = DateTime(start.year, start.month, start.day + s.trialDays);
  final end = _date(s.endDate);
  final out = <DateTime>[];
  for (var k = 0; k < 10000; k++) {
    final DateTime t;
    if (s.cycle == 'once') {
      if (k > 0) break;
      t = first;
    } else if (s.cycle == 'weekly') {
      t = DateTime(first.year, first.month, first.day + 7 * k);
    } else {
      t = _addMonthsClamped(first, k * (_cycleMonths[s.cycle] ?? 1));
    }
    if (!t.isBefore(to) || (end != null && !t.isBefore(end))) break;
    if (!t.isBefore(from)) out.add(t);
  }
  return out;
}

/// 到今天（含）为止的扣费日，最新的在前（修订第 294 条，与 Web 的“历史扣费”相同）：按周期推算，不是实际付款记录
List<DateTime> pastCharges(Subscription s, {DateTime? today}) {
  final now = today ?? DateTime.now();
  return chargesBetween(s, DateTime(1970), DateTime(now.year, now.month, now.day + 1)).reversed.toList();
}

/// 未来 n 个月每月的预计扣款（修订第 240 条，与 Web 的 cashflow 相同）：按扣费日排期，换算人民币的舍入点为每个订阅的当月扣费
({List<(DateTime, int, int)> months, int total, List<String> missing}) cashflow(Iterable<Subscription> list, FxRates fx, {DateTime? today, int n = 12}) {
  final now = today ?? DateTime.now();
  final months = <(DateTime, int, int)>[];
  final missing = <String>{};
  for (var i = 0; i < n; i++) {
    final from = DateTime(now.year, now.month + i, 1);
    final to = DateTime(now.year, now.month + i + 1, 1);
    var cents = 0, count = 0;
    for (final s in list) {
      final k = chargesBetween(s, from, to).length;
      if (k == 0) continue;
      final v = fx.toCny(k * s.priceCents, s.currency);
      if (v == null) {
        missing.add(s.currency);
        continue;
      }
      cents += v;
      count++;
    }
    months.add((from, cents, count));
  }
  return (months: months, total: months.fold(0, (a, m) => a + m.$2), missing: missing.toList()..sort());
}

// 两个日期相差的日历天数：按 UTC 的年月日计算，跨夏令时切换（某天只有 23 或 25 小时）也不差一天（修订第 272 条）
int _dayDiff(DateTime d, DateTime n) => DateTime.utc(d.year, d.month, d.day).difference(DateTime.utc(n.year, n.month, n.day)).inDays;

/// 距下次扣费的天数（今天为 0）；没有下次扣费时为 null
int? daysUntilCharge(Subscription s, {DateTime? now}) {
  final d = _date(s.nextCharge);
  if (d == null) return null;
  final n = now ?? DateTime.now();
  return _dayDiff(d, n);
}

/// 距结束订阅的天数（今天为 0）；没有结束日期或已结束时为 null
int? daysUntilEnd(Subscription s, {DateTime? now}) {
  final d = _date(s.endDate);
  if (d == null || s.ended) return null;
  final n = now ?? DateTime.now();
  return _dayDiff(d, n);
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
  // 按日历日相加：add(Duration(days:)) 加的是 24 小时的整数倍，跨夏令时切换时会落到前一天（修订第 272 条）
  final first = DateTime(start.year, start.month, start.day + s.trialDays);
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

// ---- 国家 / 地区、支付方式与人民币换算（修订第 223 条，与 Web 的 subscriptions.ts 一致） ----

/// 订阅类别的固定颜色（修订第 233 条，与 Web 的 categoryColors 相同，来自 design/tokens.json 的 cat-*）
/// 自定义类别（修订第 314 条）：键 c<编号> → （名称、取颜色的内置类别键）；由订阅列表的响应填入
/// 新增订阅时“提醒”的默认天数（修订第 316 条，面板“推送 → 订阅”设置）；旧面板没有时为 1
int defaultRemindDays = 1;

final customCategories = <String, (String, String)>{};

/// 用面板返回的自定义类别替换 [customCategories]
void setCustomCategories(List<dynamic>? list) {
  customCategories
    ..clear()
    ..addAll({
      for (final e in list ?? const [])
        if (e is Map<String, dynamic> && e['key'] is String) e['key'] as String: ((e['name'] as String?) ?? '', (e['color'] as String?) ?? 'other'),
    });
}

/// 类别名称：内置或自定义；不认识的（如类别已删除）显示为“其他”
String catName(String key) => categoryNames[key] ?? customCategories[key]?.$1 ?? '其他';

/// 全部类别键：内置（“其他”除外）、自定义、其他
List<String> allCategoryKeys() => [...categoryNames.keys.where((k) => k != 'other'), ...customCategories.keys, 'other'];

/// 类别颜色；自定义类别取所选内置类别的颜色
Color categoryColor(AppColors c, String key) => switch (customCategories[key]?.$2 ?? key) {
      'video' => c.catVideo,
      'tools' => c.catTools,
      'reading' => c.catReading,
      'social' => c.catSocial,
      'health' => c.catHealth,
      'game' => c.catGame,
      'cloud' => c.catCloud,
      'sim' => c.catSim,
      'vps' => c.catVps,
      _ => c.catOther,
    };

/// 支付方式（键与面板一致）
const paymentNames = <String, String>{
  'alipay': '支付宝',
  'wechat': '微信支付',
  'bank': '银行卡',
  'credit': '信用卡',
  'paypal': 'PayPal',
  'apple': 'Apple 账户',
  'google': 'Google Play',
  'carrier': '运营商代扣',
  'crypto': '加密货币',
  'other': '其他',
};

const _currencyCountry = <String, String>{
  'CNY': 'CN',
  'USD': 'US',
  'HKD': 'HK',
  'JPY': 'JP',
  'GBP': 'GB',
  'TWD': 'TW',
  'SGD': 'SG',
  'MOP': 'MO',
  'AUD': 'AU',
  'MYR': 'MY',
  'THB': 'TH',
  'PHP': 'PH',
  'ZAR': 'ZA',
  'NGN': 'NG',
  'GHS': 'GH',
};

/// 币种对应的默认国家 / 地区；EUR 等多国通用的币种为空
String defaultCountry(String currency) => _currencyCountry[currency] ?? '';

/// 国家 / 地区名称
String countryLabel(String code) => code.isEmpty ? '' : (countryNames[code] ?? code);

/// 某币种某天的汇率（修订第 297 条）
class FxDay {
  /// 创建
  const FxDay(this.day, this.cny);

  /// 日期（UTC，YYYY-MM-DD）
  final String day;

  /// 1 单位折合多少人民币
  final double cny;

  /// 该日 UTC 零点（Unix 秒）
  int get unix => DateTime.parse('${day}T00:00:00Z').millisecondsSinceEpoch ~/ 1000;

  /// 从接口 JSON 创建
  factory FxDay.fromJson(Map<String, dynamic> j) => FxDay(j['day'] as String, (j['cny'] as num).toDouble());
}

/// 最新汇率较 30 天前的变化（%）：取 30 天前当天或更早最近的一条；记录不满 30 天时为 null（与 Web 相同）
double? fxChange30(List<FxDay> rows) {
  if (rows.isEmpty) return null;
  final last = rows.last;
  final edge = last.unix - 30 * 86400;
  final base = rows.where((r) => r.unix <= edge).lastOrNull;
  return base == null ? null : (last.cny - base.cny) / base.cny * 100;
}

/// 汇率（GET /fx-rates）：1 单位外币折合多少人民币
class FxRates {
  /// 创建
  const FxRates({this.rates = const {'CNY': 1}, this.asOf = 0, this.source = '', this.error = ''});

  /// 币种 → 人民币
  final Map<String, double> rates;

  /// 来源数据时间（Unix 秒），0 表示还没有
  final int asOf;

  /// 来源说明
  final String source;

  /// 最近一次更新失败的原因
  final String error;

  /// 从接口 JSON 创建
  factory FxRates.fromJson(Map<String, dynamic> j) => FxRates(
        rates: {'CNY': 1, for (final e in (j['rates'] as Map<String, dynamic>? ?? const {}).entries) e.key: (e.value as num).toDouble()},
        asOf: (j['as_of'] as num?)?.toInt() ?? 0,
        source: j['source'] as String? ?? '',
        error: j['error'] as String? ?? '',
      );

  /// 换算为人民币（分）；没有汇率时为 null
  int? toCny(num cents, String currency) {
    if (currency == 'CNY') return cents.round();
    final r = rates[currency];
    return r == null || r <= 0 ? null : (cents * r).round();
  }
}

/// 一组的折算月费
class Share {
  /// 创建
  Share(this.key);

  /// 分组键（类别 / 国家代码 / 支付方式）
  final String key;

  /// 每月合计（人民币，分）
  int cents = 0;

  /// 订阅数
  int count = 0;

  /// 占比（0～100）
  double pct = 0;
}

/// 分析结果：进行中的订阅折算为每月花费（人民币）
class Analysis {
  /// 每月合计（分）
  int monthly = 0;

  /// 参与折算的订阅数
  int count = 0;

  /// 一次性付费的订阅数（不计入）
  int once = 0;

  /// 缺少汇率、没有计入的币种
  final missing = <String>[];

  /// 按类别
  List<Share> byCategory = [];

  /// 按国家 / 地区
  List<Share> byCountry = [];

  /// 按支付方式
  List<Share> byPayment = [];

  /// 按付款人（修订第 311 条），未填写的键为空
  List<Share> byPayer = [];

  /// 按月费从高到低
  final top = <(Subscription, int)>[];
}

/// 分析（修订第 223 条）：进行中的订阅按扣费周期折算为月费（每周 × 52 ÷ 12、每季 ÷ 3、每半年 ÷ 6、每年 ÷ 12），换算为人民币后
/// 按类别、国家 / 地区、支付方式汇总并排行；一次性付费的不计入。与 Web 的 analyze 一致。
Analysis analyze(Iterable<Subscription> list, FxRates fx) {
  final out = Analysis();
  final cat = <String, Share>{}, country = <String, Share>{}, pay = <String, Share>{}, payer = <String, Share>{};
  void add(Map<String, Share> m, String key, int cents) {
    final x = m.putIfAbsent(key, () => Share(key));
    x.cents += cents;
    x.count++;
  }

  for (final s in list) {
    if (s.ended) continue;
    if (s.cycle == 'once') {
      out.once++;
      continue;
    }
    final cents = fx.toCny(s.monthlyCents, s.currency);
    if (cents == null) {
      if (!out.missing.contains(s.currency)) out.missing.add(s.currency);
      continue;
    }
    out.monthly += cents;
    out.count++;
    out.top.add((s, cents));
    add(cat, s.category, cents);
    add(country, s.country, cents);
    add(pay, s.payment, cents);
    add(payer, s.payer, cents);
  }
  List<Share> sorted(Map<String, Share> m) {
    final xs = m.values.toList()..sort((a, b) => b.cents.compareTo(a.cents));
    for (final x in xs) {
      x.pct = out.monthly == 0 ? 0 : x.cents / out.monthly * 100;
    }
    return xs;
  }

  out.byCategory = sorted(cat);
  out.byCountry = sorted(country);
  out.byPayment = sorted(pay);
  out.byPayer = sorted(payer);
  out.top.sort((a, b) => b.$2.compareTo(a.$2));
  return out;
}

// ---- 概览与列表（修订第 309 条，与 Web 相同） ----

/// 当前账期已过去的比例（0～100）：上次扣费（或开始日期）到下次扣费；一次性、已结束、没有下次扣费时为 null
double? billingProgress(Subscription s, {DateTime? now}) {
  final n = now ?? DateTime.now();
  final today = DateTime(n.year, n.month, n.day);
  final to = _date(s.nextCharge);
  if (s.ended || s.cycle == 'once' || to == null) return null;
  final past = chargesBetween(s, DateTime(today.year - 2, today.month, today.day), DateTime(today.year, today.month, today.day + 1));
  final from = past.isNotEmpty ? past.last : _date(s.startDate);
  if (from == null) return null;
  final span = _dayDiff(to, from);
  if (span <= 0) return null;
  return (_dayDiff(today, from) / span * 100).clamp(0, 100).toDouble();
}

/// 列表第二行的续订说明（与 Web 的 renewText 相同）：今天续订 / N 天后续订（7 天内）/ M 月 D 日续订 / 试用中 / 已结束
String renewText(Subscription s, {DateTime? now}) {
  final n = now ?? DateTime.now();
  if (s.ended) return '已结束';
  final start = _date(s.startDate);
  if (start != null && s.trialDays > 0) {
    final first = DateTime(start.year, start.month, start.day + s.trialDays);
    if (first.isAfter(DateTime(n.year, n.month, n.day))) return '试用中，${first.month} 月 ${first.day} 日开始扣费';
  }
  final d = _date(s.nextCharge);
  if (d == null) return s.cycle == 'once' ? '一次性，已扣费' : '没有下次扣费';
  final days = _dayDiff(d, n);
  final verb = s.cycle == 'once' ? '扣费' : '续订';
  if (days == 0) return '今天$verb';
  if (days <= 7) return '$days 天后$verb';
  return d.year != n.year ? '${d.year} 年 ${d.month} 月 ${d.day} 日$verb' : '${d.month} 月 ${d.day} 日$verb';
}

/// 付款周期的短名
const cyclePay = {'weekly': '周付', 'monthly': '月付', 'quarterly': '季付', 'semiannually': '半年付', 'annually': '年付', 'once': '一次性'};

/// 订阅列表的筛选与排序（本机偏好，修订第 309 条）
class SubListView {
  /// 创建
  const SubListView({this.filter = 'all', this.sort = 'date', this.desc = false});

  /// 筛选：all / active / soon / ended / 类别键
  final String filter;

  /// 排序：date / price / name / category
  final String sort;

  /// 倒序
  final bool desc;

  /// 改其中几项
  SubListView copyWith({String? filter, String? sort, bool? desc}) =>
      SubListView(filter: filter ?? this.filter, sort: sort ?? this.sort, desc: desc ?? this.desc);

  /// 保存到偏好
  Map<String, dynamic> toJson() => {'filter': filter, 'sort': sort, 'desc': desc};

  /// 从偏好读取；legacyGrouped 为旧版“按类别”的选择（修订第 237 条），沿用为按类别排序
  factory SubListView.fromJson(Map<String, dynamic>? j, {bool legacyGrouped = false}) => SubListView(
        filter: (j?['filter'] as String?) ?? 'all',
        sort: (j?['sort'] as String?) ?? (legacyGrouped ? 'category' : 'date'),
        desc: (j?['desc'] as bool?) ?? false,
      );
}

/// 排序名称
const subSortNames = {'date': '日期', 'price': '价格', 'name': '名称', 'category': '类别'};

/// 7 天内续订（下次扣费在 0～7 天内）
bool renewsSoon(Subscription s, {DateTime? now}) {
  final d = daysUntilCharge(s, now: now);
  return !s.ended && d != null && d >= 0 && d <= 7;
}

/// 是否符合筛选
bool matchesFilter(Subscription s, String f, {DateTime? now}) => switch (f) {
      'all' => true,
      'active' => !s.ended,
      'ended' => s.ended,
      'soon' => renewsSoon(s, now: now),
      _ when f.startsWith('payer:') => s.payer == f.substring(6),
      _ => s.category == f,
    };

/// 排序（与 Web 的 sortSubs 相同）：已结束的总在最后；price 按月均折算人民币（没有汇率按原币）；date 没有下次扣费的在后
List<Subscription> sortSubs(Iterable<Subscription> list, String key, bool desc, FxRates fx) {
  final order = allCategoryKeys();
  num monthlyCny(Subscription s) {
    final m = s.cycle == 'once' ? s.priceCents : s.monthlyCents;
    return fx.toCny(m, s.currency) ?? m;
  }

  int cmp(Subscription a, Subscription b) => switch (key) {
        'price' => monthlyCny(a).compareTo(monthlyCny(b)),
        'name' => a.name.toLowerCase().compareTo(b.name.toLowerCase()),
        'category' => order.indexOf(a.category).compareTo(order.indexOf(b.category)) != 0
            ? order.indexOf(a.category).compareTo(order.indexOf(b.category))
            : (a.nextCharge.isEmpty ? '9' : a.nextCharge).compareTo(b.nextCharge.isEmpty ? '9' : b.nextCharge),
        _ => (a.nextCharge.isEmpty ? '9999' : a.nextCharge).compareTo(b.nextCharge.isEmpty ? '9999' : b.nextCharge),
      };
  final xs = list.toList();
  xs.sort((a, b) {
    if (a.ended != b.ended) return a.ended ? 1 : -1;
    return desc ? -cmp(a, b) : cmp(a, b);
  });
  return xs;
}

/// 本月待扣（人民币分）：今天到月底还要扣的；缺少汇率的币种列在 missing
({int cents, List<String> missing}) dueRestOfMonth(Iterable<Subscription> list, FxRates fx, {DateTime? now}) {
  final n = now ?? DateTime.now();
  final from = DateTime(n.year, n.month, n.day), to = DateTime(n.year, n.month + 1, 1);
  var cents = 0;
  final missing = <String>{};
  for (final s in list) {
    final k = chargesBetween(s, from, to).length;
    if (k == 0) continue;
    final v = fx.toCny(k * s.priceCents, s.currency);
    if (v == null) {
      missing.add(s.currency);
    } else {
      cents += v;
    }
  }
  return (cents: cents, missing: missing.toList()..sort());
}
