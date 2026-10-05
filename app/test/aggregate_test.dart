import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/cache.dart';
import 'package:vpsmon_app/centers.dart';
import 'package:vpsmon_app/models.dart';
import 'package:vpsmon_app/pages/shell.dart';
import 'package:vpsmon_app/prefs.dart';
import 'package:vpsmon_app/session.dart';

class _Cache implements CacheStore {
  @override
  Future<Cached<List<ServerView>>?> servers() async => null;
  @override
  Future<void> saveServers(List<ServerView> l, DateTime at) async {}
  @override
  Future<Cached<List<MetricPoint>>?> history(int serverId, String range) async => null;
  @override
  Future<void> saveHistory(int serverId, String range, List<dynamic> raw, DateTime at) async {}
  @override
  Future<void> clear() async {}
}

Map<String, dynamic> node(int id, String name, String status) => {
      'id': id, 'name': name, 'status': status,
      'traffic': {'cycle_start': '2026-10-01', 'cycle_end': '2026-11-01', 'used': 0, 'limit': 0, 'unit': 'decimal'},
    };

ApiClient client(String host, List<Map<String, dynamic>> nodes, List<Map<String, dynamic>> alerts) {
  final s = Session(server: Uri.parse('https://$host'), deviceId: 1, accessToken: 'dev_a', refreshToken: 'rt_a',
      accessExpiresAt: DateTime.now().add(const Duration(minutes: 30)), scopeType: 'all', scopeValue: '', allowLowRiskOps: false);
  return ApiClient(s, MemorySessionStore(), client: MockClient((req) async {
    final body = switch (req.url.path) {
      '/api/v1/servers' => {'items': nodes, 'next_cursor': ''},
      '/api/v1/alerts' => {'items': alerts, 'next_cursor': ''},
      _ => null,
    };
    if (body == null) return http.Response('{}', 404);
    return http.Response.bytes(utf8.encode(jsonEncode(body)), 200, headers: {'content-type': 'application/json'});
  }));
}

void main() {
  test('中心汇总与合计（与首页同一口径）', () {
    final a = CenterSummary.of([ServerView.fromJson(node(1, 'a', 'online')), ServerView.fromJson(node(2, 'b', 'offline'))]);
    expect([a.total, a.online, a.offline, a.attention], [2, 1, 1, 1]);
    final t = CenterSummary.sum([a, a.withError('x'), const CenterSummary(total: 3, online: 3, offline: 0, attention: 0)]);
    expect([t.total, t.online, t.offline, t.attention], [7, 5, 2, 2]);
  });

  testWidgets('聚合视图与跨中心事件流（设计 1.5.2）', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 812 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    final a = client('vps.example.com', [node(1, 'hk-1', 'online'), node(2, 'jp-1', 'offline')], [
      {'id': 1, 'server_id': 2, 'server_name': 'jp-1', 'type': 'offline', 'severity': 'critical', 'state': 'firing',
        'message': '超过 120 秒未收到上报', 'fired_at': 1759650000},
    ]);
    final b = client('home.example.com', [node(1, 'nas', 'online'), node(2, 'pi', 'online'), node(3, 'router', 'online')], [
      {'id': 5, 'server_id': 1, 'server_name': 'nas', 'type': 'disk', 'severity': 'warning', 'state': 'resolved',
        'message': '磁盘 86%', 'fired_at': 1759640000, 'resolved_at': 1759660000},
    ]);
    int? switched;
    await tester.pumpWidget(MaterialApp(
      home: AppShell(api: a, cache: _Cache(), prefs: MemoryPrefsStore(), onUnpair: () async {}, onRevoked: () {},
          centers: [CenterHandle('a', a.session.server, a), CenterHandle('b', b.session.server, b)], onSwitch: (i) => switched = i),
    ));
    for (var i = 0; i < 4; i++) {
      await tester.pump();
    }
    expect(find.text('全部监控中心'), findsOneWidget);
    expect(find.text('总计 5 台 · 在线 4 · 离线 1 · 需要关注 1'), findsOneWidget);
    expect(find.text('3 台 · 在线 3'), findsOneWidget);
    await tester.tap(find.text('home.example.com'));
    expect(switched, 1);

    // 事件：合并两个中心，按最近时间（恢复时间 1759660000 晚于另一条的触发时间）排序
    await tester.tap(find.text('事件'));
    await tester.pump();
    await tester.pump();
    await tester.tap(find.text('全部监控中心'));
    for (var i = 0; i < 4; i++) {
      await tester.pump();
    }
    final disk = tester.getTopLeft(find.textContaining('nas · 磁盘 86%'));
    final off = tester.getTopLeft(find.textContaining('jp-1 · 超过 120 秒'));
    expect(disk.dy, lessThan(off.dy));
    expect(find.textContaining('home.example.com'), findsWidgets);
    await tester.pumpWidget(const SizedBox());
  });
}
