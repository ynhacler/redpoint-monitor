import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/cache.dart';
import 'package:vpsmon_app/models.dart';
import 'package:vpsmon_app/pages/server_detail_page.dart';
import 'package:vpsmon_app/pages/shell.dart';
import 'package:vpsmon_app/prefs.dart';
import 'package:vpsmon_app/session.dart';

/// 内存中的缓存（页面测试不碰文件系统）
class MemCache implements CacheStore {
  Cached<List<ServerView>>? list;
  @override
  Future<Cached<List<ServerView>>?> servers() async => list;
  @override
  Future<void> saveServers(List<ServerView> l, DateTime at) async => list = Cached(l, at);
  @override
  Future<Cached<List<MetricPoint>>?> history(int serverId, String range) async => null;
  @override
  Future<void> saveHistory(int serverId, String range, List<dynamic> raw, DateTime at) async {}
  @override
  Future<void> clear() async => list = null;
}

Map<String, dynamic> node(int id, String name, String status, {String group = 'jp', List<dynamic> alerts = const []}) => {
      'id': id, 'name': name, 'status': status, 'group': group, 'country': 'JP', 'last_seen_at': 1759650000,
      'ipv4': '103.1.2.$id', 'provider': 'DMIT', 'price_cents': 1500, 'currency': 'USD', 'billing_period': 'monthly',
      'latest': {
        'cpu': {'usage': 96, 'cores': 2, 'load1': 1.2},
        'memory': {'usage': 42, 'used': 420000000, 'total': 1000000000},
        'disk': [{'mount': '/', 'device': '/dev/vda1', 'total': 40000000000, 'used': 12000000000, 'usage': 30, 'available': 28000000000}],
        'network': [{'rx_speed': 1575000, 'tx_speed': 225000}],
        'system': {'uptime': 864000},
      },
      'traffic': {'cycle_start': '2026-10-01', 'cycle_end': '2026-11-01', 'used': 638000000000, 'limit': 1000000000000,
        'unit': 'decimal', 'forecast': {'daily': 1, 'total': 891000000000, 'over': false}},
      'alerts': alerts,
    };

void main() {
  testWidgets('首页、服务器（搜索 / 排序）、事件、我的（隐私模式）与详情页在窄屏上正常渲染', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 812 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);

    final nodes = [
      node(1, 'Tokyo ARM with a very long name for overflow', 'online', alerts: [
        {'type': 'cpu', 'severity': 'warning', 'message': 'CPU 96%', 'value': 96, 'fired_at': 1759650000, 'silenced': false},
      ]),
      node(2, 'HK-1', 'offline', group: 'hk'),
      node(3, 'SG-calm', 'online', group: 'sg'),
    ];
    // 正常节点 SG-calm 没有告警，CPU 改成 10
    (nodes[2]['latest'] as Map)['cpu'] = {'usage': 10, 'cores': 2, 'load1': 0.1};
    final client = MockClient((req) async {
      final p = req.url.path;
      Object body;
      if (p == '/api/v1/servers') {
        body = {'items': nodes, 'next_cursor': ''};
      } else if (p == '/api/v1/servers/1') {
        body = nodes[0];
      } else if (p == '/api/v1/alerts') {
        body = {'next_cursor': '', 'items': [
          {'id': 9, 'server_id': 2, 'server_name': 'HK-1', 'type': 'offline', 'severity': 'critical', 'state': 'firing',
            'message': '超过 120 秒未收到上报', 'fired_at': 1759650000},
          {'id': 8, 'server_id': 1, 'server_name': 'Tokyo', 'type': 'cpu', 'severity': 'warning', 'state': 'resolved',
            'message': 'CPU 96%', 'fired_at': 1759640000, 'resolved_at': 1759640600},
        ]};
      } else if (p.endsWith('/health')) {
        body = {'level': 'warn', 'status': '需要关注：CPU 96%', 'items': [
          {'key': 'cpu', 'title': 'CPU', 'text': '过去 24 小时平均 12%，峰值 68%', 'level': 'ok'},
        ]};
      } else if (p.endsWith('/metrics/history')) {
        body = {'range': '1h', 'resolution': 10, 'next_cursor': '', 'items': [
          for (var i = 0; i < 30; i++)
            {'ts': 1759650000 + i * 10, 'cpu': 50 + i, 'mem_used': 400, 'mem_total': 1000, 'disk_used': 3, 'disk_total': 10,
              'rx_speed': 1000 * i, 'tx_speed': 500 * i},
        ]};
      } else {
        return http.Response('{}', 404);
      }
      return http.Response.bytes(utf8.encode(jsonEncode(body)), 200, headers: {'content-type': 'application/json'});
    });
    final session = Session(server: Uri.parse('https://monitor.example.com'), deviceId: 1, accessToken: 'dev_a',
        refreshToken: 'rt_a', accessExpiresAt: DateTime.now().add(const Duration(minutes: 30)), scopeType: 'all',
        scopeValue: '', allowLowRiskOps: true);
    final api = ApiClient(session, MemorySessionStore(), client: client);
    final prefs = MemoryPrefsStore();

    await tester.pumpWidget(MaterialApp(
      home: AppShell(api: api, cache: MemCache(), prefs: prefs, onUnpair: () async {}, onRevoked: () {}),
    ));
    await tester.pump(); // 读偏好与缓存
    await tester.pump(); // 请求返回
    await tester.pump();

    // 首页：需要关注（离线在前），正常节点不在首页
    final hk = tester.getTopLeft(find.text('HK-1'));
    final tokyo = tester.getTopLeft(find.textContaining('Tokyo ARM'));
    expect(hk.dy, lessThan(tokyo.dy));
    expect(find.text('CPU 96%'), findsOneWidget);
    expect(find.text('SG-calm'), findsNothing);

    // 服务器：搜索按 IP 过滤，收藏后出现在首页
    await tester.tap(find.text('服务器'));
    await tester.pump();
    expect(find.text('SG-calm'), findsOneWidget);
    await tester.enterText(find.byType(TextField), '103.1.2.3');
    await tester.pump();
    expect(find.text('SG-calm'), findsOneWidget);
    expect(find.text('HK-1'), findsNothing);
    await tester.tap(find.byIcon(Icons.star_border).first);
    await tester.pump();
    expect(prefs.value.favorites, {3});

    // 事件：时间线
    await tester.tap(find.text('事件'));
    await tester.pump();
    await tester.pump();
    expect(find.textContaining('超过 120 秒未收到上报'), findsOneWidget);
    expect(find.textContaining('恢复'), findsWidgets);

    // 我的：隐私模式
    await tester.tap(find.text('我的'));
    await tester.pump();
    await tester.tap(find.text('隐私模式'));
    await tester.pump();
    expect(prefs.value.privacy, isTrue);

    // 首页的收藏卡片：IP 打码
    await tester.tap(find.text('首页'));
    await tester.pump();
    expect(find.textContaining('103.***.***.3'), findsOneWidget);

    // 详情：趋势、流量、资产（隐私模式下价格与供应商打码）
    await tester.tap(find.textContaining('Tokyo ARM'));
    await tester.pumpAndSettle(const Duration(milliseconds: 100), EnginePhase.sendSemanticsUpdate, const Duration(seconds: 2));
    expect(find.text('趋势'), findsOneWidget);
    expect(find.text('需要关注：CPU 96%'), findsOneWidget);
    // 底部导航的各页都保留在 IndexedStack 中：指定滚动详情页自己的列表
    final detail = find.descendant(of: find.byType(ServerDetailPage), matching: find.byType(Scrollable)).first;
    await tester.scrollUntilVisible(find.text('638 GB / 1 TB'), 200, scrollable: detail);
    expect(find.textContaining('已使用 63.8%'), findsOneWidget);
    await tester.scrollUntilVisible(find.text('资产'), 200, scrollable: detail);
    expect(find.text('103.***.***.1'), findsOneWidget);
    expect(find.text('DMIT'), findsNothing);
    await tester.scrollUntilVisible(find.text('静音告警'), 200, scrollable: detail);
    expect(find.text('开启维护'), findsOneWidget);

    await tester.pumpWidget(const SizedBox());
  });
}
