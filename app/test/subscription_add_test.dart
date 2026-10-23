import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/pages/subscriptions_page.dart';
import 'package:vpsmon_app/session.dart';

Session _session({required bool subs}) => Session(server: Uri.parse('https://monitor.example.com'), deviceId: 1, accessToken: 'dev_a',
    refreshToken: 'rt_a', accessExpiresAt: DateTime.now().add(const Duration(minutes: 30)), scopeType: 'all', scopeValue: '',
    allowLowRiskOps: true, allowSubscriptions: subs);

void main() {
  testWidgets('没有“允许记录订阅”时不显示新增入口', (tester) async {
    final client = MockClient((_) async => http.Response('{"limited":false,"items":[]}', 200));
    await tester.pumpWidget(MaterialApp(home: SubscriptionsPage(api: ApiClient(_session(subs: false), MemorySessionStore(), client: client), onRevoked: () {})));
    await tester.pump();
    await tester.pump();
    expect(find.text('新增订阅'), findsNothing);
  });

  testWidgets('新增订阅（修订第 217 条）：选服务 → 填写 → 字段错误显示在输入框下 → 保存后刷新列表', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 812 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    final posted = <Map<String, dynamic>>[];
    var items = <Map<String, dynamic>>[];
    final client = MockClient((req) async {
      if (req.method == 'POST' && req.url.path == '/api/v1/app/subscriptions') {
        final b = jsonDecode(req.body) as Map<String, dynamic>;
        posted.add(b);
        if (b['price_cents'] as int < 0) {
          return http.Response.bytes(utf8.encode(jsonEncode({'error': {'code': 'validation_failed', 'message': '请检查', 'request_id': 'r',
            'details': [{'field': 'price_cents', 'message': '价格应在 0 ～ 1,000,000 之间'}]}})), 422);
        }
        final x = {...b, 'id': 1, 'next_charge': '2099-01-05', 'ended': false, 'logo_version': 0, 'created_at': 0, 'updated_at': 0};
        items = [x];
        return http.Response.bytes(utf8.encode(jsonEncode(x)), 201);
      }
      return http.Response.bytes(utf8.encode(jsonEncode({'limited': false, 'items': items})), 200);
    });
    await tester.pumpWidget(MaterialApp(home: SubscriptionsPage(api: ApiClient(_session(subs: true), MemorySessionStore(), client: client), onRevoked: () {})));
    await tester.pump();
    await tester.pump();
    await tester.tap(find.text('新增订阅'));
    await tester.pumpAndSettle();
    // 常用中有 Netflix：选中后名称、类别、币种按目录预填
    await tester.tap(find.text('Netflix'));
    await tester.pumpAndSettle();
    expect(find.text('新增订阅'), findsOneWidget);
    // 价格没填：面板返回字段错误，显示在价格输入框下
    await tester.tap(find.text('保存'));
    await tester.pumpAndSettle();
    expect(posted.last['brand'], 'netflix');
    expect(posted.last['category'], 'video');
    expect(posted.last['currency'], 'USD');
    expect(find.text('价格应在 0 ～ 1,000,000 之间'), findsOneWidget);
    await tester.enterText(find.widgetWithText(TextField, '价格'), '15.49');
    await tester.tap(find.text('保存'));
    await tester.pumpAndSettle();
    expect(posted.last['price_cents'], 1549);
    expect(find.textContaining('已新增“Netflix”'), findsOneWidget);
    expect(find.text('Netflix'), findsOneWidget); // 列表已刷新
    expect(tester.takeException(), isNull);
  });
}
