import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/pages/subscription_analysis_page.dart';
import 'package:vpsmon_app/subscriptions.dart';

void main() {
  testWidgets('订阅分析页：人民币月费合计、分组切换、排行与汇率说明，窄屏不溢出', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 812 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    const items = [
      Subscription(id: 1, name: 'ChatGPT Plus', category: 'tools', priceCents: 2000, currency: 'USD', cycle: 'monthly', startDate: '2026-01-01',
          country: 'US', payment: 'credit'),
      Subscription(id: 2, name: '域名', category: 'cloud', priceCents: 12000, currency: 'CNY', cycle: 'annually', startDate: '2026-01-01',
          country: 'CN', payment: 'alipay'),
    ];
    const fx = FxRates(rates: {'CNY': 1, 'USD': 7.0}, asOf: 1761177601, source: 'ExchangeRate-API');
    await tester.pumpWidget(MaterialApp(
      home: SubscriptionAnalysisPage(items: items, fx: fx, avatar: (s, {double size = 40}) => SizedBox(width: size, height: size)),
    ));
    expect(find.text('¥150.00'), findsOneWidget); // ¥140 + ¥10
    expect(find.text('¥140.00/月'), findsOneWidget);
    expect(find.textContaining('¥120.00/年 · 折合 ¥10.00/月'), findsOneWidget); // 年付的显示折合月费
    await tester.tap(find.text('支付方式'));
    await tester.pumpAndSettle();
    expect(find.textContaining('信用卡'), findsOneWidget);
    expect(find.textContaining('支付宝'), findsOneWidget);
    await tester.tap(find.text('国家地区'));
    await tester.pumpAndSettle();
    expect(find.textContaining('美国'), findsOneWidget);
    await tester.scrollUntilVisible(find.textContaining('1 USD = ¥7.00'), 200);
    expect(tester.takeException(), isNull);
  });

  test('未来 12 个月预计扣款（修订第 240 条）：年付只在扣费月，月付每月，结束后不再计入，外币换算', () {
    const fx = FxRates(rates: {'CNY': 1, 'USD': 7.0});
    const items = [
      Subscription(id: 1, name: '年付', category: 'cloud', priceCents: 12000, currency: 'CNY', cycle: 'annually', startDate: '2025-03-15'),
      Subscription(id: 2, name: '月付', category: 'tools', priceCents: 1000, currency: 'USD', cycle: 'monthly', startDate: '2026-01-31'),
      Subscription(id: 3, name: '已结束', category: 'video', priceCents: 500, currency: 'CNY', cycle: 'monthly', startDate: '2026-01-05',
          endDate: '2026-12-01'),
    ];
    final cf = cashflow(items, fx, today: DateTime(2026, 10, 25));
    expect(cf.months.length, 12);
    expect(cf.months.first.$1, DateTime(2026, 10, 1));
    // 10、11 月：月付 70 元 + 未结束的 5 元；12 月起只有月付；次年 3 月加上年付 120 元
    expect([for (final m in cf.months.take(6)) m.$2], [7500, 7500, 7000, 7000, 7000, 19000]);
    expect(cf.total, 7500 * 2 + 7000 * 10 + 12000);
    // 1 月 31 日起的月付在 2 月按月末扣费
    expect(chargesBetween(items[1], DateTime(2027, 2, 1), DateTime(2027, 3, 1)), [DateTime(2027, 2, 28)]);
  });
}
