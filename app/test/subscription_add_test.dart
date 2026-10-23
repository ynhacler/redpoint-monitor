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

  testWidgets('长按修改 / 删除（修订第 218 条）：修改沿用表单中没有的 VPS 配置；快到期的排最前并标红点', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 812 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    String day(int d) {
      final t = DateTime.now().add(Duration(days: d));
      return '${t.year}-${t.month.toString().padLeft(2, '0')}-${t.day.toString().padLeft(2, '0')}';
    }

    Map<String, dynamic> sub(int id, String name, String next, {String category = 'video', Map<String, dynamic>? vps}) => {
          'id': id, 'name': name, 'brand': '', 'category': category, 'price_cents': 1000, 'currency': 'USD', 'cycle': 'monthly',
          'trial_days': 0, 'start_date': '2026-01-01', 'end_date': '', 'remind_days': 1, 'note': '', 'phones': [],
          'vps': vps ?? {'ips': [], 'cpu': 0, 'memory_gb': 0, 'disk_gb': 0, 'bandwidth_mbps': 0, 'traffic_gb': 0, 'location': ''},
          'next_charge': next, 'ended': false, 'logo_version': 0, 'created_at': 0, 'updated_at': 0,
        };
    // 面板按下次扣费日排序；“马上续费”3 天后扣费，排在 20 天后的前面并标红点
    var items = [
      sub(1, '远期订阅', day(20)),
      sub(2, '我的 VPS', day(30), category: 'vps',
          vps: {'ips': ['203.0.113.8'], 'cpu': 4, 'memory_gb': 8, 'disk_gb': 80, 'bandwidth_mbps': 1000, 'traffic_gb': 2000, 'location': '东京'}),
      sub(3, '马上续费', day(3)),
    ];
    final calls = <String>[];
    Map<String, dynamic>? putBody;
    final client = MockClient((req) async {
      calls.add('${req.method} ${req.url.path}');
      if (req.method == 'PUT') {
        putBody = jsonDecode(req.body) as Map<String, dynamic>;
        return http.Response.bytes(utf8.encode(jsonEncode({...items[1], ...putBody!})), 200);
      }
      if (req.method == 'DELETE') {
        items = items.where((x) => x['id'] != 1).toList();
        return http.Response('', 204);
      }
      return http.Response.bytes(utf8.encode(jsonEncode({'limited': false, 'items': items})), 200);
    });
    await tester.pumpWidget(MaterialApp(home: SubscriptionsPage(api: ApiClient(_session(subs: true), MemorySessionStore(), client: client), onRevoked: () {})));
    await tester.pump();
    await tester.pump();
    expect(tester.getTopLeft(find.text('马上续费')).dy, lessThan(tester.getTopLeft(find.text('远期订阅')).dy));
    expect(find.textContaining('快到期 1 个'), findsOneWidget);

    // 修改：只改价格，VPS 的 CPU、内存等沿用原值
    await tester.longPress(find.text('我的 VPS'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('修改'));
    await tester.pumpAndSettle();
    expect(find.text('修改订阅'), findsOneWidget);
    await tester.enterText(find.widgetWithText(TextField, '价格'), '12.5');
    await tester.tap(find.text('保存'));
    await tester.pumpAndSettle();
    expect(calls, contains('PUT /api/v1/app/subscriptions/2'));
    expect(putBody!['price_cents'], 1250);
    expect((putBody!['vps'] as Map)['cpu'], 4);
    expect((putBody!['vps'] as Map)['memory_gb'], 8);
    expect((putBody!['vps'] as Map)['ips'], ['203.0.113.8']);
    expect((putBody!['vps'] as Map)['location'], '东京');

    // 删除：确认后调用接口并刷新
    await tester.longPress(find.text('远期订阅'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('删除'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(TextButton, '删除'));
    await tester.pumpAndSettle();
    expect(calls, contains('DELETE /api/v1/app/subscriptions/1'));
    expect(find.text('远期订阅'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('没有权限时长按不弹出操作', (tester) async {
    final client = MockClient((_) async => http.Response.bytes(utf8.encode(jsonEncode({'limited': false, 'items': [
          {'id': 1, 'name': '测试订阅', 'brand': '', 'category': 'video', 'price_cents': 100, 'currency': 'USD', 'cycle': 'monthly', 'trial_days': 0,
            'start_date': '2026-01-01', 'end_date': '', 'remind_days': 0, 'note': '', 'phones': [], 'vps': {}, 'next_charge': '2099-01-01',
            'ended': false, 'logo_version': 0, 'created_at': 0, 'updated_at': 0},
        ]})), 200));
    await tester.pumpWidget(MaterialApp(home: SubscriptionsPage(api: ApiClient(_session(subs: false), MemorySessionStore(), client: client), onRevoked: () {})));
    await tester.pump();
    await tester.pump();
    await tester.longPress(find.text('测试订阅'));
    await tester.pumpAndSettle();
    expect(find.text('修改'), findsNothing);
  });
}
