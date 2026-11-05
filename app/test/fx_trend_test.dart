import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/subscriptions.dart';
import 'package:vpsmon_app/theme.dart';
import 'package:vpsmon_app/widgets/fx_trend.dart';

Future<void> pump(WidgetTester tester, Future<List<FxDay>> Function(String) load, {List<String> currencies = const ['USD', 'JPY']}) async {
  tester.view.physicalSize = const Size(320 * 3, 640 * 3);
  tester.view.devicePixelRatio = 3;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(MaterialApp(
    theme: appTheme(Brightness.light),
    home: Scaffold(body: SingleChildScrollView(child: FxTrend(currencies: currencies, load: load))),
  ));
}

List<FxDay> days(int n, double base) => [
      for (var i = 0; i < n; i++) FxDay(DateTime.utc(2026, 9, 1).add(Duration(days: i)).toIso8601String().substring(0, 10), base + i * 0.01),
    ];

void main() {
  test('fxChange30：不满 30 天为 null，取 30 天前当天或更早最近的一条', () {
    expect(fxChange30(const []), isNull);
    expect(fxChange30(days(30, 7)), isNull);
    expect(fxChange30(days(31, 7)), closeTo(0.3 / 7 * 100, 1e-9));
    const gap = [FxDay('2026-09-01', 5), FxDay('2026-09-20', 9), FxDay('2026-10-11', 6)];
    expect(fxChange30(gap), closeTo(20, 1e-9)); // 09-11 之前最近的是 09-01
  });

  testWidgets('展开时才请求，切换币种，320px 不溢出', (tester) async {
    final calls = <String>[];
    await pump(tester, (c) async {
      calls.add(c);
      return days(40, c == 'USD' ? 7 : 0.04);
    });
    expect(calls, isEmpty);
    await tester.tap(find.text('汇率走势'));
    await tester.pumpAndSettle();
    expect(calls, ['USD']);
    expect(find.textContaining('较 30 天前'), findsOneWidget);
    expect(find.textContaining('从 2026-09-01 开始记录'), findsOneWidget);
    await tester.tap(find.text('JPY'));
    await tester.pumpAndSettle();
    expect(calls, ['USD', 'JPY']);
    expect(find.textContaining('¥0.04'), findsWidgets); // 小于 0.1 的汇率多留小数
    expect(tester.takeException(), isNull);
  });

  testWidgets('记录不足时给出说明', (tester) async {
    await pump(tester, (c) async => days(1, 7), currencies: const ['USD']);
    await tester.tap(find.text('汇率走势'));
    await tester.pumpAndSettle();
    expect(find.textContaining('记录还不够'), findsOneWidget);
  });

  testWidgets('旧版面板提示升级', (tester) async {
    await pump(tester, (c) async => throw ApiException(404, 'not_found', ''), currencies: const ['EUR']);
    await tester.tap(find.text('汇率走势'));
    await tester.pumpAndSettle();
    expect(find.textContaining('面板版本较旧'), findsOneWidget);
  });
}
