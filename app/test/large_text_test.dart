import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/pages/shell.dart';
import 'package:vpsmon_app/prefs.dart';
import 'package:vpsmon_app/session.dart';

import 'widget_test.dart' show MemCache, cloudJson, node;

/// 模拟面板：长名称、告警、流量与订阅都带上，尽量撑满每一行
http.Client panel() {
  final nodes = [
    node(1, 'Tokyo ARM with a very long name for overflow', 'online', alerts: [
      {'type': 'cpu', 'severity': 'warning', 'message': 'CPU 96%', 'value': 96, 'fired_at': 1759650000, 'silenced': false},
    ]),
    node(2, 'HK-1', 'offline', group: 'hk'),
  ];
  nodes[0]['expire_date'] = '2099-10-28';
  return MockClient((req) async {
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
      ]};
    } else if (p == '/api/v1/system-events') {
      body = {'next_cursor': '', 'items': []};
    } else if (p == '/api/v1/app/cloud') {
      body = cloudJson;
    } else if (p == '/api/v1/app/subscriptions') {
      body = {'limited': false, 'items': [
        {'id': 1, 'name': 'Netflix Premium Family Plan', 'brand': 'netflix', 'category': 'video', 'price_cents': 6800, 'currency': 'HKD',
          'cycle': 'monthly', 'trial_days': 0, 'start_date': '2026-01-05', 'end_date': '', 'remind_days': 1, 'note': '', 'phones': [],
          'vps': {'ips': [], 'cpu': 0, 'memory_gb': 0, 'disk_gb': 0, 'bandwidth_mbps': 0, 'traffic_gb': 0, 'location': ''},
          'next_charge': '2099-01-05', 'ended': false, 'logo_version': 0, 'created_at': 0, 'updated_at': 0},
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
}

void main() {
  // 系统字号调大时各页不能溢出（手机 UI 规范 P2）。测试字体比真实字体宽，结果偏保守
  for (final width in [375.0, 320.0]) {
    for (final scale in [1.3, 1.5, 2.0]) {
      testWidgets('系统字号 $scale 倍、宽 ${width.toInt()}：各页不溢出', (tester) async {
        tester.view.physicalSize = Size(width * 3, 812 * 3);
        tester.view.devicePixelRatio = 3;
        tester.platformDispatcher.textScaleFactorTestValue = scale;
        addTearDown(tester.view.reset);
        addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);

        // 收集全部溢出再一起报告，不停在第一处
        final overflows = <String>{};
        final original = FlutterError.onError;
        FlutterError.onError = (d) {
          final text = d.toString();
          if (!text.contains('overflowed by')) return original?.call(d);
          overflows.add(RegExp(r'lib/[\w/]+\.dart:\d+').firstMatch(text)?.group(0) ?? text.split('\n').first);
        };

        // 页面里还有横向滑动的筛选条，只滑纵向的
        Finder vertical() => find.byWidgetPredicate((w) => w is Scrollable && w.axisDirection == AxisDirection.down).hitTestable();

        Future<void> settle() async {
          for (var i = 0; i < 4; i++) {
            await tester.pump(const Duration(milliseconds: 50));
          }
        }

        // 往下滑几屏，让列表里靠后的内容也布局一次
        Future<void> sweep() async {
          for (var i = 0; i < 4; i++) {
            final list = vertical();
            if (list.evaluate().isEmpty) return;
            await tester.drag(list.first, const Offset(0, -500), warnIfMissed: false);
            await settle();
          }
        }

        Future<void> top() async {
          for (var i = 0; i < 6; i++) {
            final list = vertical();
            if (list.evaluate().isEmpty) return;
            await tester.drag(list.first, const Offset(0, 800), warnIfMissed: false);
            await settle();
          }
        }

        Future<void> open(String label) async {
          final f = find.text(label);
          if (f.evaluate().isEmpty) return;
          await tester.tap(f.first, warnIfMissed: false);
          await settle();
          await sweep();
        }

        final session = Session(server: Uri.parse('https://monitor.example.com'), deviceId: 1, accessToken: 'dev_a',
            refreshToken: 'rt_a', accessExpiresAt: DateTime.now().add(const Duration(minutes: 30)), scopeType: 'all',
            scopeValue: '', allowLowRiskOps: true, allowSubscriptions: true);
        await tester.pumpWidget(MaterialApp(
          home: AppShell(api: ApiClient(session, MemorySessionStore(), client: panel()), cache: MemCache(), prefs: MemoryPrefsStore(),
              onUnpair: () async {}, onRevoked: () {}),
        ));
        await settle();
        // 字大时概况占满一屏，节点卡片要滑下去才看得到
        final card = find.textContaining('Tokyo ARM').hitTestable();
        for (var i = 0; i < 12 && card.evaluate().isEmpty; i++) {
          await tester.drag(vertical().first, const Offset(0, -200), warnIfMissed: false);
          await settle();
        }
        await tester.tap(card.first, warnIfMissed: false);
        await settle();
        await sweep();
        final tabs = find
            .byWidgetPredicate((w) => w.key is ValueKey && '${(w.key as ValueKey).value}'.startsWith('detail-tab-'), skipOffstage: false)
            .evaluate()
            .map((e) => e.widget.key!)
            .toList();
        for (final key in tabs) {
          await top();
          final tab = find.byKey(key);
          if (tab.evaluate().isEmpty) continue;
          await tester.tap(tab, warnIfMissed: false);
          await settle();
          await sweep();
        }
        await tester.pageBack();
        await settle();
        await sweep();
        await open('工具');
        await open('云平台');
        await open('设置');

        await tester.pumpWidget(const SizedBox());
        FlutterError.onError = original;
        expect(overflows, isEmpty);
      });
    }
  }
}
