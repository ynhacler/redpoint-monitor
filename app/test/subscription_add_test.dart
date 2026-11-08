import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/pages/subscriptions_page.dart';
import 'package:vpsmon_app/session.dart';
import 'package:vpsmon_app/subscriptions.dart';

Session _session({required bool subs}) => Session(
    server: Uri.parse('https://monitor.example.com'),
    deviceId: 1,
    accessToken: 'dev_a',
    refreshToken: 'rt_a',
    accessExpiresAt: DateTime.now().add(const Duration(minutes: 30)),
    scopeType: 'all',
    scopeValue: '',
    allowLowRiskOps: true,
    allowSubscriptions: subs);

void main() {
  testWidgets('没有“允许记录订阅”时不显示新增入口', (tester) async {
    final client = MockClient((_) async => http.Response('{"limited":false,"items":[]}', 200));
    await tester
        .pumpWidget(MaterialApp(home: SubscriptionsPage(api: ApiClient(_session(subs: false), MemorySessionStore(), client: client), onRevoked: () {})));
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
          return http.Response.bytes(
              utf8.encode(jsonEncode({
                'error': {
                  'code': 'validation_failed',
                  'message': '请检查',
                  'request_id': 'r',
                  'details': [
                    {'field': 'price_cents', 'message': '价格应在 0 ～ 1,000,000 之间'}
                  ]
                }
              })),
              422);
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
    await tester.ensureVisible(find.text('保存'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存'));
    await tester.pumpAndSettle();
    expect(posted.last['brand'], 'netflix');
    expect(posted.last['category'], 'video');
    expect(posted.last['currency'], 'USD');
    expect(posted.last['country'], 'US'); // 按币种预填（修订第 223 条）
    expect(posted.last['payment'], ''); // 支付方式须自己选择，由面板校验
    expect(find.text('价格应在 0 ～ 1,000,000 之间'), findsOneWidget);
    await tester.enterText(find.widgetWithText(TextField, '价格'), '15.49');
    await tester.ensureVisible(find.text('保存'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存'));
    await tester.pumpAndSettle();
    expect(posted.last['price_cents'], 1549);
    expect(find.textContaining('已新增“Netflix”'), findsOneWidget);
    expect(_row('Netflix'), findsOneWidget); // 列表已刷新
    expect(tester.takeException(), isNull);
  });

  testWidgets('长按修改 / 删除（修订第 218 条）：修改沿用表单中没有的 VPS 配置；按日期排序时近的在前', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 812 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    String day(int d) {
      final t = DateTime.now().add(Duration(days: d));
      return '${t.year}-${t.month.toString().padLeft(2, '0')}-${t.day.toString().padLeft(2, '0')}';
    }

    Map<String, dynamic> sub(int id, String name, String next, {String category = 'video', Map<String, dynamic>? vps}) => {
          'id': id,
          'name': name,
          'brand': '',
          'category': category,
          'price_cents': 1000,
          'currency': 'USD',
          'cycle': 'monthly',
          'trial_days': 0,
          'start_date': '2026-01-01',
          'end_date': '',
          'remind_days': 1,
          'note': '',
          'phones': [],
          'vps': vps ?? {'ips': [], 'cpu': 0, 'memory_gb': 0, 'disk_gb': 0, 'bandwidth_mbps': 0, 'traffic_gb': 0, 'location': ''},
          'next_charge': next,
          'ended': false,
          'logo_version': 0,
          'created_at': 0,
          'updated_at': 0,
        };
    // 面板按下次扣费日排序；“马上续费”3 天后扣费，排在 20 天后的前面并标红点
    var items = [
      sub(1, '远期订阅', day(20)),
      sub(2, '我的 VPS', day(30), category: 'vps', vps: {
        'ips': ['203.0.113.8'],
        'cpu': 4,
        'memory_gb': 8,
        'disk_gb': 80,
        'bandwidth_mbps': 1000,
        'traffic_gb': 2000,
        'location': '东京'
      }),
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
    expect(tester.getTopLeft(_row('马上续费')).dy, lessThan(tester.getTopLeft(_row('远期订阅')).dy));
    expect(find.text('3 天后续订'), findsOneWidget); // 7 天内：续订说明为“N 天后续订”（修订第 309 条）

    // 修改：只改价格，VPS 的 CPU、内存等沿用原值
    await tester.longPress(_row('我的 VPS'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('修改'));
    await tester.pumpAndSettle();
    expect(find.text('修改订阅'), findsOneWidget);
    await tester.enterText(find.widgetWithText(TextField, '价格'), '12.5');
    // 表单变长（修订第 311 条加了付款人与链接），保存按钮要先滚动到可见
    await tester.dragUntilVisible(find.text('保存'), find.byWidgetPredicate((w) => w is ListView && '${w.key}'.contains('form-')), const Offset(0, -300));
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存'));
    await tester.pumpAndSettle();
    expect(calls, contains('PUT /api/v1/app/subscriptions/2'));
    expect(putBody!['price_cents'], 1250);
    expect((putBody!['vps'] as Map)['cpu'], 4);
    expect((putBody!['vps'] as Map)['memory_gb'], 8);
    expect((putBody!['vps'] as Map)['ips'], ['203.0.113.8']);
    expect((putBody!['vps'] as Map)['location'], '东京');
    // 付款人与链接随表单一起提交（修订第 311 条），面板据此保存或清空
    expect(putBody!.containsKey('payer') && putBody!.containsKey('url'), isTrue);

    // 删除：确认后调用接口并刷新
    await tester.longPress(_row('远期订阅'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('删除'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(TextButton, '删除'));
    await tester.pumpAndSettle();
    expect(calls, contains('DELETE /api/v1/app/subscriptions/1'));
    expect(_row('远期订阅'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('没有权限时长按不弹出操作', (tester) async {
    final client = MockClient((_) async => http.Response.bytes(
        utf8.encode(jsonEncode({
          'limited': false,
          'items': [
            {
              'id': 1,
              'name': '测试订阅',
              'brand': '',
              'category': 'video',
              'price_cents': 100,
              'currency': 'USD',
              'cycle': 'monthly',
              'trial_days': 0,
              'start_date': '2026-01-01',
              'end_date': '',
              'remind_days': 0,
              'note': '',
              'phones': [],
              'vps': {},
              'next_charge': '2099-01-01',
              'ended': false,
              'logo_version': 0,
              'created_at': 0,
              'updated_at': 0
            },
          ]
        })),
        200));
    await tester
        .pumpWidget(MaterialApp(home: SubscriptionsPage(api: ApiClient(_session(subs: false), MemorySessionStore(), client: client), onRevoked: () {})));
    await tester.pump();
    await tester.pump();
    await tester.longPress(_row('测试订阅'));
    await tester.pumpAndSettle();
    expect(find.text('修改'), findsNothing);
  });

  testWidgets('订阅“已续费”（修订第 224 条）：有结束日期的长按显示，顺延后刷新列表', (tester) async {
    var end = '2026-11-02';
    final calls = <String>[];
    Map<String, dynamic> sub() => {
          'id': 7,
          'name': '域名 example.dev',
          'brand': '',
          'category': 'cloud',
          'price_cents': 8900,
          'currency': 'CNY',
          'cycle': 'annually',
          'trial_days': 0,
          'start_date': '2025-11-02',
          'end_date': end,
          'remind_days': 1,
          'note': '',
          'phones': [],
          'vps': {},
          'next_charge': '',
          'ended': false,
          'logo_version': 0,
          'created_at': 0,
          'updated_at': 0,
          'country': 'CN',
          'payment': 'alipay',
        };
    final client = MockClient((req) async {
      calls.add('${req.method} ${req.url.path}');
      if (req.method == 'POST' && req.url.path == '/api/v1/app/subscriptions/7/renew') {
        end = '2027-11-02';
        return http.Response.bytes(utf8.encode(jsonEncode(sub())), 200);
      }
      return http.Response.bytes(
          utf8.encode(jsonEncode({
            'limited': false,
            'items': [sub()]
          })),
          200);
    });
    await tester.pumpWidget(MaterialApp(home: SubscriptionsPage(api: ApiClient(_session(subs: true), MemorySessionStore(), client: client), onRevoked: () {})));
    await tester.pump();
    await tester.pump();
    await tester.longPress(find.text('域名 example.dev'));
    await tester.pumpAndSettle();
    expect(find.textContaining('结束日期 2026-11-02 顺延一个周期（每年）'), findsOneWidget);
    await tester.tap(find.text('已续费'));
    await tester.pumpAndSettle();
    expect(calls, contains('POST /api/v1/app/subscriptions/7/renew'));
    expect(find.textContaining('已顺延到 2027-11-02'), findsOneWidget);
  });

  testWidgets('订阅列表筛选与排序（修订第 309 条）：按类别排序时有组头；筛选、正序 / 倒序回调新的值', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 1400 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    Map<String, dynamic> sub(int id, String name, String category, {int price = 1000}) => {
          'id': id,
          'name': name,
          'brand': '',
          'category': category,
          'price_cents': price,
          'currency': 'CNY',
          'cycle': 'monthly',
          'trial_days': 0,
          'start_date': '2026-01-01',
          'end_date': '',
          'remind_days': 0,
          'note': '',
          'phones': [],
          'vps': {'ips': [], 'cpu': 0, 'memory_gb': 0, 'disk_gb': 0, 'bandwidth_mbps': 0, 'traffic_gb': 0, 'location': ''},
          'next_charge': '2099-01-0$id',
          'ended': false,
          'logo_version': 0,
          'created_at': 0,
          'updated_at': 0,
        };
    final client = MockClient((req) async => http.Response.bytes(
        utf8.encode(jsonEncode({
          'limited': false,
          'items': [sub(1, '甲影音', 'video'), sub(2, '乙主机', 'vps', price: 5000), sub(3, '丙影音', 'video', price: 3000)]
        })),
        200));
    final api = ApiClient(_session(subs: false), MemorySessionStore(), client: client);
    SubListView? changed;
    Future<void> show(SubListView v) async {
      await tester.pumpWidget(MaterialApp(home: SubscriptionsPage(key: UniqueKey(), api: api, onRevoked: () {}, view: v, onView: (x) => changed = x)));
      await tester.pump();
      await tester.pump();
    }

    double y(String t) => tester.getTopLeft(_row(t)).dy;

    // 按日期：甲、乙、丙
    await show(const SubListView());
    expect(find.text('2 个'), findsNothing);
    expect(y('甲影音'), lessThan(y('乙主机')));
    expect(y('乙主机'), lessThan(y('丙影音')));
    await tester.tap(find.byKey(const ValueKey('sub-desc')));
    expect(changed?.desc, true);

    // 按价格倒序：乙（50）、丙（30）、甲（10）
    await show(const SubListView(sort: 'price', desc: true));
    expect(y('乙主机'), lessThan(y('丙影音')));
    expect(y('丙影音'), lessThan(y('甲影音')));

    // 按类别：组头为数量与月均；影音（2 个）在 VPS（1 个）之前（目录顺序）
    await show(const SubListView(sort: 'category'));
    expect(find.text('2 个'), findsOneWidget);
    expect(find.text('1 个'), findsOneWidget);
    expect(find.text('月均 ¥40.00'), findsOneWidget);
    expect(y('丙影音'), lessThan(y('乙主机')));

    // 筛选：只看 VPS
    await show(const SubListView(filter: 'vps'));
    expect(_row('甲影音'), findsNothing);
    expect(_row('乙主机'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('sub-filter')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('全部（3）'));
    await tester.pumpAndSettle();
    expect(changed?.filter, 'all');
    expect(_row('甲影音'), findsOneWidget);
  });

  testWidgets('每月预算（修订第 312 条）：面板设了预算时显示本月预计与预算，没设时不显示', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 1400 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    final now = DateTime.now();
    final start = '${now.year}-${now.month.toString().padLeft(2, '0')}-01';
    Map<String, dynamic> body(int budget) => {
          'limited': false,
          'budget_cents': budget,
          'items': [
            {
              'id': 1,
              'name': '云主机',
              'brand': '',
              'category': 'cloud',
              'price_cents': 10000,
              'currency': 'CNY',
              'cycle': 'monthly',
              'trial_days': 0,
              'start_date': start,
              'end_date': '',
              'remind_days': 0,
              'note': '',
              'phones': [],
              'vps': {'ips': [], 'cpu': 0, 'memory_gb': 0, 'disk_gb': 0, 'bandwidth_mbps': 0, 'traffic_gb': 0, 'location': ''},
              'next_charge': '2099-01-01',
              'ended': false,
              'logo_version': 0,
              'created_at': 0,
              'updated_at': 0
            },
          ],
        };
    for (final (budget, shown) in [(5000, true), (0, false)]) {
      final client = MockClient((req) async => http.Response.bytes(utf8.encode(jsonEncode(body(budget))), 200));
      await tester.pumpWidget(MaterialApp(
          home: SubscriptionsPage(key: UniqueKey(), api: ApiClient(_session(subs: false), MemorySessionStore(), client: client), onRevoked: () {})));
      await tester.pump();
      await tester.pump();
      expect(find.text('本月预计 ¥100.00 / 预算 ¥50.00'), shown ? findsOneWidget : findsNothing);
    }
  });

  testWidgets('自定义类别（修订第 314 条）：名称来自面板，按类别分组与筛选可用，修改表单可选', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 1400 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    Map<String, dynamic> sub(int id, String name, String category) => {
          'id': id,
          'name': name,
          'brand': '',
          'category': category,
          'price_cents': 1000,
          'currency': 'CNY',
          'cycle': 'monthly',
          'trial_days': 0,
          'start_date': '2026-01-01',
          'end_date': '',
          'remind_days': 0,
          'note': '',
          'phones': [],
          'vps': {'ips': [], 'cpu': 0, 'memory_gb': 0, 'disk_gb': 0, 'bandwidth_mbps': 0, 'traffic_gb': 0, 'location': ''},
          'country': 'CN',
          'payment': 'bank',
          'next_charge': '2099-01-0$id',
          'ended': false,
          'logo_version': 0,
          'created_at': 0,
          'updated_at': 0,
        };
    final client = MockClient((req) async => http.Response.bytes(
        utf8.encode(jsonEncode({
          'limited': false,
          'categories': [
            {'id': 3, 'key': 'c3', 'name': '保险', 'color': 'health'}
          ],
          'items': [sub(1, '车险', 'c3'), sub(2, '视频', 'video')]
        })),
        200));
    await tester.pumpWidget(MaterialApp(
        home: SubscriptionsPage(
            api: ApiClient(_session(subs: true), MemorySessionStore(), client: client), onRevoked: () {}, view: const SubListView(sort: 'category'))));
    await tester.pump();
    await tester.pump();
    expect(find.text('保险'), findsOneWidget); // 组头
    expect(y(tester, '视频'), lessThan(y(tester, '车险'))); // 内置类别在自定义类别之前
    await tester.tap(find.byKey(const ValueKey('sub-filter')));
    await tester.pumpAndSettle();
    expect(find.text('保险（1）'), findsOneWidget);
    await tester.tapAt(const Offset(5, 5));
    await tester.pumpAndSettle();
    // 修改表单：类别下拉中有自定义类别，当前值保持
    await tester.longPress(_row('车险'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('修改'));
    await tester.pumpAndSettle();
    expect(find.text('保险'), findsWidgets);
  });
}

/// 列表中的行（概览卡片的“下一项续订”也会显示名称，修订第 309 条）
Finder _row(String name) => find.descendant(of: find.byKey(const ValueKey('sub-list')), matching: find.text(name));

double y(WidgetTester tester, String name) => tester.getTopLeft(_row(name)).dy;
