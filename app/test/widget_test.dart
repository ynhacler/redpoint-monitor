import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/cache.dart';
import 'package:vpsmon_app/models.dart';
import 'package:vpsmon_app/pages/cloud_page.dart';
import 'package:vpsmon_app/pages/latency_compare_page.dart';
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

const cloudJson = {
  'limited': false,
  'accounts': [
    {'id': 1, 'provider': 'aws', 'name': 'AWS 主账号', 'enabled': true, 'syncing': false, 'auth_failed': false, 'last_error': '',
      'last_warning': '区域 ap-east-1 的 Lightsail 正在为账户初始化，本轮跳过，下一轮再试', 'next_try_at': 0, 'synced_at': 1759650000,
      'instance_count': 2, 'budget_cents': 1000,
      'current_cost': {'period': '2026-10', 'amount_cents': 321, 'forecast_cents': 1279, 'balance_cents': null, 'currency': 'USD'}},
    {'id': 2, 'provider': 'aliyun_cn', 'name': '阿里云', 'enabled': true, 'syncing': false, 'auth_failed': true,
      'last_error': 'InvalidAccessKeyId.NotFound（HTTP 404）', 'last_warning': '', 'next_try_at': 0, 'synced_at': 0,
      'instance_count': 0, 'budget_cents': 0, 'current_cost': null},
  ],
  'instances': [
    {'id': 10, 'account_id': 1, 'name': 'tokyo-ls-with-a-very-long-instance-name', 'region': 'ap-northeast-1', 'kind': 'lightsail',
      'state': 'running', 'plan': 'micro_3_0', 'expire_at': 0, 'traffic_used_bytes': 12000000000, 'traffic_limit_bytes': 2048000000000, 'server_id': 1},
    {'id': 11, 'account_id': 1, 'name': 'ec2', 'region': 'us-west-2', 'kind': 'ec2', 'state': 'stopped', 'plan': 't3.micro',
      'expire_at': 0, 'traffic_used_bytes': 0, 'traffic_limit_bytes': 0, 'server_id': null},
  ],
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
      } else if (p == '/api/v1/app/cloud') {
        body = cloudJson;
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
    // 实时概况；云账户一行；正常节点以紧凑行列在“全部节点”中
    expect(find.text('实时概况'), findsOneWidget);
    expect(find.textContaining('2 个账户 · 2 台实例 · 本月 \$3.21'), findsOneWidget);
    expect(find.text('需要关注 2'), findsOneWidget);
    await tester.scrollUntilVisible(find.text('SG-calm'), 200, scrollable: find.byType(Scrollable).first);
    expect(find.text('全部节点'), findsOneWidget);

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
    await tester.scrollUntilVisible(find.textContaining('103.***.***.3'), 200, scrollable: find.byType(Scrollable).first);
    expect(find.textContaining('103.***.***.3'), findsOneWidget);
    expect(find.text('其他节点'), findsNothing); // 唯一的正常节点已收藏

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

  testWidgets('测速对比：选目标后按延迟排名，第一名标“最快”，窄屏不溢出', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 812 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    final client = MockClient((req) async {
      expect(req.url.path, '/api/v1/latency-overview');
      final body = {
        'servers': [
          {'id': 1, 'name': 'Tokyo ARM with a very long name for overflow', 'group': 'jp'},
          {'id': 2, 'name': 'HK-1', 'group': 'hk'},
        ],
        'targets': [
          {'id': 10, 'isp': 'ct', 'province': '上海', 'address': '1.2.3.4', 'proto': 'tcp', 'port': 80, 'enabled': true, 'builtin': false, 'note': ''},
          {'id': 11, 'isp': 'cm', 'province': '广东', 'address': '5.6.7.8', 'proto': 'icmp', 'port': 0, 'enabled': true, 'builtin': false, 'note': ''},
        ],
        'cells': [
          {'server_id': 1, 'target_id': 10, 'latency_ms': 180.0, 'loss': 0},
          {'server_id': 2, 'target_id': 10, 'latency_ms': 35.0, 'loss': 0},
          {'server_id': 1, 'target_id': 11, 'latency_ms': 90.0, 'loss': 0},
          {'server_id': 2, 'target_id': 11, 'latency_ms': null, 'loss': 100},
        ],
      };
      return http.Response.bytes(utf8.encode(jsonEncode(body)), 200, headers: {'content-type': 'application/json'});
    });
    final session = Session(server: Uri.parse('https://monitor.example.com'), deviceId: 1, accessToken: 'dev_a',
        refreshToken: 'rt_a', accessExpiresAt: DateTime.now().add(const Duration(minutes: 30)), scopeType: 'all',
        scopeValue: '', allowLowRiskOps: true);
    int? opened;
    await tester.pumpWidget(MaterialApp(
      home: LatencyComparePage(api: ApiClient(session, MemorySessionStore(), client: client), onRevoked: () {}, onOpenServer: (id) => opened = id),
    ));
    await tester.pump();
    await tester.pump();

    // 默认第一个目标（上海电信）：HK-1 最快，排第一
    expect(tester.getTopLeft(find.text('HK-1')).dy, lessThan(tester.getTopLeft(find.textContaining('Tokyo ARM')).dy));
    expect(find.text('最快'), findsOneWidget);
    expect(find.text('35 ms'), findsOneWidget);
    // 切到广东移动：HK-1 不可达排最后
    await tester.tap(find.text('广东移动'));
    await tester.pump();
    expect(find.text('不可达'), findsOneWidget);
    expect(tester.getTopLeft(find.textContaining('Tokyo ARM')).dy, lessThan(tester.getTopLeft(find.text('HK-1')).dy));
    await tester.tap(find.text('HK-1'));
    expect(opened, 2);
    expect(tester.takeException(), isNull);
  });

  testWidgets('云账户：状态、费用与预算、展开实例、关联节点；窄屏不溢出', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 812 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    final client = MockClient((req) async {
      expect(req.url.path, '/api/v1/app/cloud');
      return http.Response.bytes(utf8.encode(jsonEncode(cloudJson)), 200, headers: {'content-type': 'application/json'});
    });
    final session = Session(server: Uri.parse('https://monitor.example.com'), deviceId: 1, accessToken: 'dev_a',
        refreshToken: 'rt_a', accessExpiresAt: DateTime.now().add(const Duration(minutes: 30)), scopeType: 'all',
        scopeValue: '', allowLowRiskOps: true);
    int? opened;
    await tester.pumpWidget(MaterialApp(
      home: CloudPage(api: ApiClient(session, MemorySessionStore(), client: client), onRevoked: () {}, serverNames: const {1: 'Tokyo ARM'},
          onOpenServer: (id) => opened = id),
    ));
    await tester.pump();
    await tester.pump();
    expect(find.text('AWS 主账号'), findsOneWidget);
    expect(find.textContaining('部分区域已跳过'), findsOneWidget);
    expect(find.text('\$3.21'), findsOneWidget);
    expect(find.text('\$12.79'), findsOneWidget);
    expect(find.text('128%'), findsOneWidget); // 预估超预算
    expect(find.textContaining('凭证失效或权限不足'), findsOneWidget);
    await tester.tap(find.text('实例（2）'));
    await tester.pumpAndSettle();
    expect(find.text('Tokyo ARM'), findsOneWidget);
    expect(find.textContaining('流量 12 GB / 2'), findsOneWidget);
    await tester.tap(find.text('Tokyo ARM'));
    expect(opened, 1);
    expect(tester.takeException(), isNull);
  });

  testWidgets('云账户：非“全部节点”授权的设备显示说明', (tester) async {
    final client = MockClient((_) async => http.Response('{"limited":true,"accounts":[],"instances":[]}', 200));
    final session = Session(server: Uri.parse('https://monitor.example.com'), deviceId: 1, accessToken: 'dev_a',
        refreshToken: 'rt_a', accessExpiresAt: DateTime.now().add(const Duration(minutes: 30)), scopeType: 'group',
        scopeValue: 'jp', allowLowRiskOps: false);
    await tester.pumpWidget(MaterialApp(home: CloudPage(api: ApiClient(session, MemorySessionStore(), client: client), onRevoked: () {})));
    await tester.pump();
    await tester.pump();
    expect(find.textContaining('授权范围不是“全部节点”'), findsOneWidget);
  });
}
