import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/pages/subscription_calendar_page.dart';
import 'package:vpsmon_app/subscriptions.dart';

Subscription _sub(int id, String name, String start, {String cycle = 'monthly', int price = 1000, String currency = 'CNY'}) =>
    Subscription.fromJson({
      'id': id, 'name': name, 'brand': '', 'category': 'cloud', 'price_cents': price, 'currency': currency, 'cycle': cycle,
      'trial_days': 0, 'start_date': start, 'end_date': '', 'remind_days': 0, 'note': '', 'phones': [],
      'vps': {'ips': [], 'cpu': 0, 'memory_gb': 0, 'disk_gb': 0, 'bandwidth_mbps': 0, 'traffic_gb': 0, 'location': ''},
      'next_charge': '', 'ended': false, 'logo_version': 0, 'created_at': 0, 'updated_at': 0,
    });

void main() {
  testWidgets('续订日历（修订第 313 条）：本月扣费次数与合计、点某天列出当天订阅、翻月；大字号不溢出', (tester) async {
    final now = DateTime.now();
    String ymd(DateTime d) => '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';
    final items = [
      _sub(1, '甲', ymd(DateTime(now.year, now.month, 3)), price: 1000),
      _sub(2, '乙', ymd(DateTime(now.year, now.month, 3)), price: 2000),
      _sub(3, '周付', ymd(DateTime(now.year, now.month, 1)), cycle: 'weekly', price: 100),
      _sub(4, '美元', ymd(DateTime(now.year, now.month, 10)), currency: 'USD'),
    ];
    final weekly = chargesBetween(items[2], DateTime(now.year, now.month, 1), DateTime(now.year, now.month + 1, 1)).length;
    for (final (scale, width) in [(1.0, 375.0), (2.0, 320.0)]) {
      tester.view.physicalSize = Size(width * 3, 1400 * 3);
      tester.view.devicePixelRatio = 3;
      addTearDown(tester.view.reset);
      Subscription? opened;
      await tester.pumpWidget(MaterialApp(
        home: MediaQuery(
          data: MediaQueryData(size: Size(width, 1400), textScaler: TextScaler.linear(scale)),
          child: SubscriptionCalendarPage(
            key: UniqueKey(),
            items: items,
            fx: const FxRates(),
            avatar: (s, {double size = 40}) => SizedBox(width: size, height: size),
            onOpen: (s) => opened = s,
          ),
        ),
      ));
      await tester.pump();
      expect(tester.takeException(), isNull);
      // 美元没有汇率：不计入合计并注明
      expect(find.textContaining('${3 + weekly} 次扣费'), findsOneWidget);
      expect(find.textContaining('不含 USD'), findsOneWidget);
      await tester.tap(find.text('3').first);
      await tester.pump();
      expect(find.text('甲'), findsOneWidget);
      expect(find.text('乙'), findsOneWidget);
      await tester.tap(find.text('甲'));
      expect(opened?.name, '甲');
      await tester.tap(find.byTooltip('下个月'));
      await tester.pump();
      expect(find.text('回到本月'), findsOneWidget);
      expect(tester.takeException(), isNull);
    }
  });
}
