import 'dart:ui';

import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/catalog.gen.dart';
import 'package:vpsmon_app/pages/connectivity_page.dart';
import 'package:vpsmon_app/subscriptions.dart';
import 'package:vpsmon_app/svg_path.dart';

void main() {
  test('SVG 路径：紧凑写法、弧线标志与全部标志都能解析到 24×24 视图框附近（边界含控制点）', () {
    final p = parseSvgPath('M0 0h24v24H0z');
    expect(p.getBounds().width, 24);
    // “-.5.5” 是两个数；弧线标志不带分隔符（a1 1 0 011 1）
    final q = parseSvgPath('M2 2l-.5.5a1 1 0 011 1z');
    expect(q.getBounds().isEmpty, isFalse);
    for (final e in {...brandIcons, ...siteIcons}.entries) {
      final b = parseSvgPath(e.value.path).getBounds();
      expect(b.isEmpty, isFalse, reason: e.key);
      // 边界含曲线控制点，可能略超出视图框；图形的中心应在视图框内、尺寸接近整个视图框
      expect(const Rect.fromLTWH(4, 4, 16, 16).contains(b.center) && b.longestSide > 12 && b.longestSide < 30, isTrue, reason: '${e.key} $b');
    }
  });

  test('目录由 Web 生成：订阅品牌、44 个测试目标与备用地址', () {
    expect(catalog['netflix']?.name, 'Netflix');
    expect(siteGroups.map((g) => g.key), ['cn', 'us', 'global']);
    expect(siteGroups.fold<int>(0, (a, g) => a + g.sites.length), 44);
    final claude = siteGroups.expand((g) => g.sites).firstWhere((s) => s.id == 'claude');
    expect(claude.urls.length, 3);
    expect(claude.urls.every((u) => u.startsWith('https://')), isTrue);
  });

  test('订阅：扣费说明与每月折算与 Web 一致', () {
    Subscription sub(String cycle, {int trial = 0, String start = '2026-01-31', String next = '2026-11-30', bool ended = false}) =>
        Subscription(id: 1, name: 'x', category: 'video', priceCents: 1200, currency: 'USD', cycle: cycle, trialDays: trial,
            startDate: start, nextCharge: next, ended: ended);
    final now = DateTime(2026, 10, 23);
    expect(chargeText(sub('monthly'), now: now), '每月 31 日扣费');
    expect(chargeText(sub('annually'), now: now), '每年 1 月 31 日扣费');
    expect(chargeText(sub('weekly', start: '2026-10-21'), now: now), '每周三扣费');
    expect(chargeText(sub('quarterly'), now: now), '每季度，下次 11 月 30 日');
    expect(chargeText(sub('monthly', trial: 14, start: '2026-10-20'), now: now), '试用中，11 月 3 日开始扣费');
    expect(chargeText(sub('monthly', ended: true), now: now), '已结束订阅');
    expect(sub('annually').monthlyCents, 100);
    expect(sub('weekly').monthlyCents, 5200);
    expect(daysUntilCharge(sub('monthly', next: '2026-10-25'), now: now), 2);
    // 快到期：7 天内扣费或 7 天内结束（修订第 218 条）
    expect(dueSoonDays(sub('monthly', next: '2026-10-25'), now: now), 2);
    expect(dueSoonDays(sub('monthly', next: '2026-11-25'), now: now), isNull);
    expect(dueSoonDays(const Subscription(id: 2, name: 'y', category: 'sim', priceCents: 0, currency: 'CNY', cycle: 'monthly',
        startDate: '2026-01-01', endDate: '2026-10-28', nextCharge: '2026-11-30'), now: now), 5);
    expect(dueSoonDays(sub('monthly', next: '2026-10-25', ended: true), now: now), isNull);
    final totals = monthlyByCurrency([sub('monthly'), sub('annually'), sub('monthly', ended: true)]);
    expect(totals, [('USD', 1300.0)]);
  });

  test('连通性：先测一次再测一次取较小值；连接失败改测备用地址；超时不改测', () async {
    final calls = <String>[];
    Future<Object> Function(String) fake(Map<String, List<Object>> plan) => (url) async {
          calls.add(url);
          return plan[url]!.removeAt(0);
        };
    expect(await measureSite(['a'], fake({'a': [120, 80]})), (ms: 80, fail: null));
    calls.clear();
    expect(await measureSite(['a', 'b'], fake({'a': ['error'], 'b': [300, 'error']})), (ms: 300, fail: null));
    expect(calls, ['a', 'b', 'b']);
    calls.clear();
    expect(await measureSite(['a', 'b'], fake({'a': ['timeout']})), (ms: null, fail: 'timeout'));
    expect(calls, ['a']);
    expect(await measureSite(['a'], fake({'a': ['error']})), (ms: null, fail: 'error'));
    expect([gradeOf(149), gradeOf(150), gradeOf(400), gradeOf(null), gradeOf(probeFail)], ['best', 'good', 'slow', 'timeout', 'fail']);
  });

  test('汇率换算与分析（修订第 223 条）：折算月费、换算人民币、分组、排行、缺汇率与一次性', () {
    Subscription sub(int id, String cycle, int cents, String cur, {String cat = 'video', String country = 'US', String pay = 'credit', bool ended = false}) =>
        Subscription(id: id, name: 's$id', category: cat, priceCents: cents, currency: cur, cycle: cycle, startDate: '2026-01-01',
            country: country, payment: pay, ended: ended);
    const fx = FxRates(rates: {'CNY': 1, 'USD': 7.0, 'HKD': 0.9});
    expect(fx.toCny(1000, 'USD'), 7000);
    expect(fx.toCny(1000, 'CNY'), 1000);
    expect(fx.toCny(1000, 'EUR'), isNull);
    final a = analyze([
      sub(1, 'monthly', 2000, 'USD'), // ¥140
      sub(2, 'annually', 12000, 'CNY', cat: 'cloud', country: 'CN', pay: 'alipay'), // ¥10/月
      sub(3, 'quarterly', 3000, 'HKD', cat: 'cloud', country: 'HK', pay: 'alipay'), // HK$10/月 → ¥9
      sub(4, 'once', 9900, 'CNY'),
      sub(5, 'monthly', 500, 'EUR'),
      sub(6, 'monthly', 100000, 'CNY', ended: true),
    ], fx);
    expect(a.monthly, 14000 + 1000 + 900);
    expect(a.count, 3);
    expect(a.once, 1);
    expect(a.missing, ['EUR']);
    expect(a.byCategory.map((x) => (x.key, x.cents)), [('video', 14000), ('cloud', 1900)]);
    expect(a.byPayment.first.key, 'credit');
    expect(a.byCountry.map((x) => x.key), ['US', 'CN', 'HK']);
    expect(a.top.first.$1.id, 1);
    expect(a.byCategory.fold<double>(0, (t, x) => t + x.pct).round(), 100);
    expect(defaultCountry('HKD'), 'HK');
    expect(defaultCountry('EUR'), '');
    expect(countryLabel('HK'), '香港');
    expect(paymentNames['carrier'], '运营商代扣');
  });
}
