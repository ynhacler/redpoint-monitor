import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/cache.dart';
import 'package:vpsmon_app/models.dart';
import 'package:vpsmon_app/pages/cloud_page.dart';
import 'package:vpsmon_app/pages/connectivity_page.dart';
import 'package:vpsmon_app/pages/ip_page.dart';
import 'package:vpsmon_app/pages/latency_compare_page.dart';
import 'package:vpsmon_app/pages/reorder_page.dart';
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
  testWidgets('服务器（概况 / 搜索 / 收藏）、云平台、工具（订阅）、事件、我的（隐私模式）与详情页在窄屏上正常渲染', (tester) async {
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
    // Tokyo 上有容器（设计 33.4）：一个已命名、长名称测试窄屏不溢出，一个首次采样没有 CPU
    (nodes[0]['latest'] as Map)['containers'] = [
      {'id': '3f9a1c2b7d4e', 'runtime': 'docker', 'cpu': 18.4, 'memory': 480000000, 'mem_limit': 536870912, 'pids': 23},
      {'id': 'c04be1f7a993', 'runtime': 'podman', 'memory': 18000000, 'command': 'redis-server'},
    ];
    nodes[0]['container_names'] = {'3f9a1c2b7d4e': 'nginx-reverse-proxy-with-a-very-long-container-name'};
    nodes[0]['container_restarts'] = {'c04be1f7a993': 4};
    nodes[0]['ip_info'] = {'ip': '103.1.2.1', 'asn': 'AS13335', 'org': 'Cloudflare, Inc.', 'hostname': '', 'city': 'Tokyo', 'region': 'Tokyo',
      'country': 'JP', 'timezone': 'Asia/Tokyo', 'fetched_at': 1759650000, 'error': ''};
    nodes[0]['unlock'] = {'supported': true, 'pending': false, 'requested_at': 1759650000, 'checked_at': 1759650060, 'items': [
      {'service': 'netflix', 'status': 'yes', 'region': 'JP'},
      {'service': 'chatgpt', 'status': 'no'},
      {'service': 'abema', 'status': 'yes', 'region': 'JP', 'local': 'JP'},
    ]};
    nodes[0]['containers_missing'] = [
      {'label': 'postgres', 'missing': 1, 'since': 1759650000},
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
      } else if (p == '/api/v1/system-events') {
        body = {'next_cursor': '', 'items': [
          {'id': 3, 'kind': 'panel', 'severity': 'critical', 'server_id': 0, 'server_name': '', 'title': '⚠️ 面板异常：磁盘空间不足',
            'message': '数据目录所在磁盘剩余 5.0%（1.0 GB）', 'created_at': 1759650000},
          {'id': 2, 'kind': 'upgrade', 'severity': 'info', 'server_id': 1, 'server_name': 'Tokyo', 'title': '✅ Tokyo Agent 已升级到 v0.5.8',
            'message': '', 'created_at': 1759640000},
        ]};
      } else if (p == '/api/v1/app/cloud') {
        body = cloudJson;
      } else if (p == '/api/v1/app/subscriptions') {
        body = {'limited': false, 'items': [
          {'id': 1, 'name': 'Netflix', 'brand': 'netflix', 'category': 'video', 'price_cents': 6800, 'currency': 'HKD', 'cycle': 'monthly',
            'trial_days': 0, 'start_date': '2026-01-05', 'end_date': '', 'remind_days': 1, 'note': '', 'phones': [],
            'vps': {'ips': [], 'cpu': 0, 'memory_gb': 0, 'disk_gb': 0, 'bandwidth_mbps': 0, 'traffic_gb': 0, 'location': ''},
            'next_charge': '2099-01-05', 'ended': false, 'logo_version': 0, 'created_at': 0, 'updated_at': 0},
        ]};
      } else if (p.endsWith('/reach')) {
        body = {'configured': true, 'status': 'partial', 'text': '移动不可达', 'probes': [
          {'probe_id': 7, 'name': 'gz-cm', 'isp': 'cm', 'status': 'blocked', 'since': 1759650000, 'bad_ports': [22, 443], 'last_at': 1759650600,
            'checks': [{'family': 4, 'port': 0, 'latency_ms': null, 'loss': 100, 'error': 'timeout'},
              {'family': 4, 'port': 443, 'latency_ms': null, 'loss': 100, 'error': 'timeout'}]},
          {'probe_id': 8, 'name': 'sh-ct', 'isp': 'ct', 'status': 'ok', 'since': 1759640000, 'bad_ports': [], 'last_at': 1759650600,
            'checks': [{'family': 4, 'port': 443, 'latency_ms': 45.0, 'loss': 0, 'error': ''}]},
        ], 'series': {
          '7': [for (var i = 0; i < 5; i++) {'ts': 1759650000 + i * 300, 'latency_ms': i < 3 ? 80.0 : null, 'loss': i < 3 ? 0 : 100}],
          '8': [for (var i = 0; i < 5; i++) {'ts': 1759650000 + i * 300, 'latency_ms': 45.0, 'loss': 0}],
        }};
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

    // 服务器：概况在顶部，异常优先（离线在前）
    final hk = tester.getTopLeft(find.text('HK-1'));
    final tokyo = tester.getTopLeft(find.textContaining('Tokyo ARM'));
    expect(hk.dy, lessThan(tokyo.dy));
    expect(find.text('实时概况'), findsOneWidget);
    // 搜索按 IP 过滤；收藏
    await tester.enterText(find.byType(TextField), '103.1.2.3');
    await tester.pump();
    expect(find.text('SG-calm'), findsOneWidget);
    expect(find.text('HK-1'), findsNothing);
    await tester.tap(find.byIcon(Icons.star_border).first);
    await tester.pump();
    expect(prefs.value.favorites, {3});
    await tester.enterText(find.byType(TextField), '');
    await tester.pump();

    // 底部：服务器 / 网络 / 工具 / 设置；工具中分页“订阅”“云平台”（修订第 218 条）
    expect(find.text('网络'), findsOneWidget);
    expect(find.byType(ConnectivityPage), findsNothing); // 没切到这个标签前不创建，不会自动查询或测试
    expect(find.byType(IpPage), findsNothing);
    await tester.tap(find.text('工具'));
    await tester.pump();
    await tester.pump();
    expect(find.text('Netflix'), findsOneWidget);
    expect(find.text('月均折算'), findsOneWidget);
    expect(find.textContaining('每月 5 日扣费'), findsOneWidget);
    await tester.tap(find.text('云平台'));
    await tester.pumpAndSettle(const Duration(milliseconds: 100), EnginePhase.sendSemanticsUpdate, const Duration(seconds: 2));
    expect(find.text('AWS 主账号'), findsOneWidget);

    // 事件：从“服务器”右上角的铃铛进入
    await tester.tap(find.text('服务器'));
    await tester.pump();
    await tester.tap(find.byTooltip('事件'));
    await tester.pumpAndSettle(const Duration(milliseconds: 100), EnginePhase.sendSemanticsUpdate, const Duration(seconds: 2));
    expect(find.textContaining('超过 120 秒未收到上报'), findsOneWidget);
    expect(find.textContaining('恢复'), findsWidgets);
    // 系统事件（修订第 163 条）
    await tester.tap(find.text('系统'));
    await tester.pump();
    await tester.pump();
    expect(find.textContaining('磁盘空间不足'), findsOneWidget);
    expect(find.textContaining('已升级到 v0.5.8'), findsOneWidget);
    await tester.pageBack();
    await tester.pumpAndSettle(const Duration(milliseconds: 100), EnginePhase.sendSemanticsUpdate, const Duration(seconds: 2));

    // 设置：隐私模式
    await tester.tap(find.text('设置'));
    await tester.pump();
    await tester.tap(find.text('隐私模式'));
    await tester.pump();
    expect(prefs.value.privacy, isTrue);

    // 服务器：卡片上的 IP 打码
    await tester.tap(find.text('服务器'));
    await tester.pump();
    final list = find.descendant(of: find.byType(IndexedStack), matching: find.byType(Scrollable)).first;
    await tester.scrollUntilVisible(find.textContaining('103.***.***.3'), 200, scrollable: list);
    expect(find.textContaining('103.***.***.3'), findsOneWidget);
    await tester.scrollUntilVisible(find.textContaining('Tokyo ARM'), -200, scrollable: list);

    // 详情（修订第 214 条）：概况（隐私模式下 IP 与供应商打码、ASN）→ 健康摘要 → 分页
    await tester.tap(find.textContaining('Tokyo ARM'));
    await tester.pumpAndSettle(const Duration(milliseconds: 100), EnginePhase.sendSemanticsUpdate, const Duration(seconds: 2));
    expect(find.text('103.***.***.1'), findsOneWidget);
    expect(find.text('DMIT'), findsNothing);
    expect(find.textContaining('AS13335'), findsOneWidget);
    expect(find.text('需要关注：CPU 96%'), findsOneWidget);
    // 底部导航的各页都保留在 IndexedStack 中：指定滚动详情页自己的列表
    final detail = find.descendant(of: find.byType(ServerDetailPage), matching: find.byType(Scrollable)).first;
    // 监控：实时数值与历史
    await tester.scrollUntilVisible(find.text('历史'), 200, scrollable: detail);
    expect(find.text('历史'), findsOneWidget);
    // 网络：流量、解锁、国内可达性、云实例
    await tester.scrollUntilVisible(find.byKey(const ValueKey('detail-tab-net')), -200, scrollable: detail);
    await tester.tap(find.byKey(const ValueKey('detail-tab-net')));
    await tester.pump();
    await tester.scrollUntilVisible(find.text('638 GB / 1 TB'), 200, scrollable: detail);
    expect(find.textContaining('已使用 64%'), findsOneWidget);
    await tester.scrollUntilVisible(find.text('流媒体与网站解锁'), 200, scrollable: detail);
    expect(find.text('Netflix'), findsOneWidget);
    expect(find.text('当地服务 · 🇯🇵 JP'), findsOneWidget);
    await tester.scrollUntilVisible(find.text('国内可达性'), 200, scrollable: detail);
    expect(find.text('移动不可达'), findsOneWidget);
    expect(find.text('疑似被墙'), findsOneWidget);
    expect(find.text('可达'), findsOneWidget);
    expect(find.textContaining('TCP 443'), findsWidgets);
    // 云实例：关联到这台节点的 Lightsail（隐私模式下账户名打码）
    await tester.scrollUntilVisible(find.text('云实例'), 200, scrollable: detail);
    expect(find.textContaining('tokyo-ls-with-a-very-long-instance-name · Lightsail · ap-northeast-1'), findsOneWidget);
    expect(find.textContaining('AWS · '), findsOneWidget);
    expect(find.text('AWS · AWS 主账号'), findsNothing);
    expect(find.text('账户已产生 \$3.21'), findsOneWidget);
    // 应用：容器名称或 ID、CPU、内存与上限
    await tester.scrollUntilVisible(find.byKey(const ValueKey('detail-tab-apps')), -200, scrollable: detail);
    await tester.tap(find.byKey(const ValueKey('detail-tab-apps')));
    await tester.pump();
    await tester.scrollUntilVisible(find.text('容器'), 200, scrollable: detail);
    expect(find.text('nginx-reverse-proxy-with-a-very-long-container-name'), findsOneWidget);
    expect(find.text('redis-server'), findsOneWidget); // 没有名字时显示主进程名（修订第 165 条）
    expect(find.text('CPU 18%'), findsOneWidget);
    expect(find.textContaining('Podman'), findsOneWidget);
    expect(find.text('已停止：postgres'), findsOneWidget); // 修订第 166 条
    expect(find.textContaining('24 小时内重启 4 次'), findsOneWidget); // 修订第 167 条
    // 告警：活动告警与静音 / 维护
    await tester.scrollUntilVisible(find.byKey(const ValueKey('detail-tab-alerts')), -200, scrollable: detail);
    await tester.tap(find.byKey(const ValueKey('detail-tab-alerts')));
    await tester.pump();
    await tester.scrollUntilVisible(find.text('静音告警'), 200, scrollable: detail);
    expect(find.text('开启维护'), findsOneWidget);
    expect(find.text('活动告警'), findsOneWidget);

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
    // 状态为固定的标签，原因点“查看详情”展开（修订第 245 条）
    expect(find.text('部分同步'), findsOneWidget);
    expect(find.text('凭证失效'), findsOneWidget);
    expect(find.textContaining('部分区域已跳过'), findsNothing);
    expect(find.text('\$3.21'), findsOneWidget);
    expect(find.text('\$12.79'), findsOneWidget);
    expect(find.text('128%'), findsOneWidget); // 预估超预算
    await tester.tap(find.text('查看详情').first);
    await tester.pump();
    expect(find.textContaining('部分区域已跳过'), findsOneWidget);
    await tester.tap(find.text('查看详情').first);
    await tester.pump();
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

  testWidgets('调整顺序：拖动把手改变顺序，按名称重排，完成时保存', (tester) async {
    final items = [for (final (i, n) in ['c-node', 'a-node', 'b-node'].indexed) ServerView.fromJson(node(i + 1, n, 'online'))];
    List<int>? saved;
    await tester.pumpWidget(MaterialApp(home: Builder(builder: (context) => Scaffold(
          body: TextButton(
            onPressed: () => Navigator.of(context).push(MaterialPageRoute(builder: (_) => ReorderPage(items: items, onSave: (ids) => saved = ids))),
            child: const Text('open'),
          ),
        ))));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    // 把最后一个（b-node）拖到最前
    final handle = find.byIcon(Icons.drag_handle).last;
    final g = await tester.startGesture(tester.getCenter(handle));
    await tester.pump(const Duration(milliseconds: 100));
    await g.moveBy(const Offset(0, -300));
    await tester.pump(const Duration(milliseconds: 100));
    await g.up();
    await tester.pumpAndSettle();
    await tester.tap(find.text('完成'));
    await tester.pumpAndSettle();
    expect(saved, [3, 1, 2]);

    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('按名称'));
    await tester.pump();
    await tester.tap(find.text('完成'));
    await tester.pumpAndSettle();
    expect(saved, [2, 3, 1]); // a-node、b-node、c-node
  });

  testWidgets('节点“已续费”（修订第 224 条）：允许记录订阅的设备在到期日旁显示，确认后顺延；普通设备不显示', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 812 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    var expire = '2026-10-28';
    final calls = <String>[];
    final n = node(1, 'Tokyo', 'online');
    final client = MockClient((req) async {
      calls.add('${req.method} ${req.url.path}');
      if (req.method == 'POST' && req.url.path == '/api/v1/app/servers/1/renew') {
        expire = '2026-11-28';
        return http.Response.bytes(utf8.encode(jsonEncode({'expire_date': expire, 'previous': '2026-10-28'})), 200);
      }
      if (req.url.path == '/api/v1/servers/1') {
        return http.Response.bytes(utf8.encode(jsonEncode({...n, 'expire_date': expire})), 200);
      }
      return http.Response('{}', 404);
    });
    Session sess(bool subs) => Session(server: Uri.parse('https://monitor.example.com'), deviceId: 1, accessToken: 'dev_a',
        refreshToken: 'rt_a', accessExpiresAt: DateTime.now().add(const Duration(minutes: 30)), scopeType: 'all',
        scopeValue: '', allowLowRiskOps: true, allowSubscriptions: subs);
    final initial = ServerView.fromJson({...n, 'expire_date': expire});

    await tester.pumpWidget(MaterialApp(home: ServerDetailPage(api: ApiClient(sess(false), MemorySessionStore(), client: client),
        cache: MemCache(), initial: initial, onRevoked: () {})));
    await tester.pump();
    expect(find.text('已续费'), findsNothing);

    await tester.pumpWidget(MaterialApp(home: ServerDetailPage(api: ApiClient(sess(true), MemorySessionStore(), client: client),
        cache: MemCache(), initial: initial, onRevoked: () {})));
    await tester.pump();
    await tester.tap(find.text('已续费'));
    await tester.pumpAndSettle();
    expect(find.textContaining('顺延一个计费周期（月付）'), findsOneWidget);
    await tester.tap(find.text('顺延'));
    await tester.pumpAndSettle();
    expect(calls, contains('POST /api/v1/app/servers/1/renew'));
    expect(find.textContaining('2026-11-28'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });
}
