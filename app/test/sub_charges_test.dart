import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/subscriptions.dart';
import 'package:vpsmon_app/theme.dart';
import 'package:vpsmon_app/widgets/sub_charges.dart';

const monthly = Subscription(id: 1, name: 'ChatGPT Plus', category: 'tools', priceCents: 2000, currency: 'USD', cycle: 'monthly', startDate: '2026-03-02');

Future<void> pump(WidgetTester tester, Subscription s, {bool privacy = false}) async {
  tester.view.physicalSize = const Size(320 * 3, 640 * 3);
  tester.view.devicePixelRatio = 3;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(MaterialApp(
    theme: appTheme(Brightness.light),
    home: Scaffold(body: SingleChildScrollView(child: SubCharges(sub: s, privacy: privacy, today: DateTime(2026, 11, 4)))),
  ));
  await tester.tap(find.text('历史扣费'));
  await tester.pumpAndSettle();
}

void main() {
  test('pastCharges：含今天、最新在前，未到的不算，试用期与结束日期生效', () {
    final today = DateTime(2026, 11, 4);
    final d = pastCharges(monthly, today: today);
    expect(d.length, 9);
    expect(d.first, DateTime(2026, 11, 2));
    expect(d.last, DateTime(2026, 3, 2));
    const q = Subscription(id: 2, name: '域名', category: 'cloud', priceCents: 8800, currency: 'CNY', cycle: 'quarterly', startDate: '2026-02-10');
    expect(pastCharges(q, today: today).length, 3); // 11 月 10 日还没到
    expect(pastCharges(q, today: DateTime(2026, 11, 10)).length, 4); // 扣费日当天算
    const trial = Subscription(id: 3, name: 'A', category: 'tools', priceCents: 100, currency: 'USD', cycle: 'monthly', startDate: '2026-11-01', trialDays: 30);
    expect(pastCharges(trial, today: today), isEmpty);
    const ended = Subscription(id: 4, name: 'B', category: 'tools', priceCents: 100, currency: 'USD', cycle: 'monthly', startDate: '2026-01-05', endDate: '2026-03-05');
    expect(pastCharges(ended, today: today).length, 2); // 结束日期当天不再扣费
  });

  testWidgets('历史扣费：次数、合计与说明，320px 不溢出', (tester) async {
    await pump(tester, monthly);
    expect(find.textContaining('共 9 次'), findsOneWidget);
    expect(find.textContaining('180.00'), findsOneWidget);
    expect(find.text('2026-11-02'), findsOneWidget);
    expect(find.textContaining('以账单为准'), findsOneWidget);
  });

  testWidgets('隐私模式：金额打码', (tester) async {
    await pump(tester, monthly, privacy: true);
    expect(find.textContaining('180.00'), findsNothing);
    expect(find.textContaining('20.00'), findsNothing);
    expect(find.text('2026-11-02'), findsOneWidget);
  });

  testWidgets('超过 12 次时先显示 12 次，可显示全部', (tester) async {
    const w = Subscription(id: 5, name: 'W', category: 'tools', priceCents: 100, currency: 'USD', cycle: 'weekly', startDate: '2026-01-05');
    await pump(tester, w);
    expect(find.textContaining('显示全部'), findsOneWidget);
    await tester.tap(find.textContaining('显示全部'));
    await tester.pumpAndSettle();
    expect(find.textContaining('显示全部'), findsNothing);
  });

  testWidgets('还没到首次扣费日', (tester) async {
    const s = Subscription(id: 6, name: 'F', category: 'tools', priceCents: 100, currency: 'USD', cycle: 'monthly', startDate: '2026-12-01');
    await pump(tester, s);
    expect(find.text('还没有到首次扣费日。'), findsOneWidget);
  });
}
