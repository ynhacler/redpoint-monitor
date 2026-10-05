import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/centers.dart';
import 'package:vpsmon_app/home_widget.dart';
import 'package:vpsmon_app/models.dart';

ServerView node(int id, String name, String status, {double? cpu, Map<String, dynamic>? extra}) => ServerView.fromJson({
      'id': id, 'name': name, 'status': status, 'last_seen_at': 1, 'ipv4': '203.0.113.9', 'provider': 'DMIT', 'price_cents': 999,
      'traffic': {'cycle_start': '2026-10-01', 'cycle_end': '2026-11-01', 'used': 0, 'limit': 0, 'unit': 'decimal'},
      if (cpu != null)
        'latest': {
          'cpu': {'usage': cpu, 'cores': 2},
          'memory': {'usage': 43.2, 'used': 1, 'total': 2},
          'network': [
            {'rx_speed': 12000000, 'tx_speed': 2000000}
          ],
        },
      ...?extra,
    });

void main() {
  test('快照：计数为全部中心之和，行异常优先，离线不显示旧数值（设计 1.5.4）', () {
    final cur = [
      node(1, 'DMIT-HK', 'online', cpu: 18),
      node(2, 'Oracle-SG', 'offline', cpu: 50),
      node(3, 'Zoro-JP', 'online', cpu: 9),
    ];
    final snap = widgetSnapshot(cur, [const CenterSummary(total: 5, online: 4, offline: 1, attention: 1)], DateTime.utc(2026, 10, 5));
    expect([snap['total'], snap['online'], snap['offline'], snap['attention']], [8, 6, 2, 2]);
    expect(snap['updated_at'], 1791158400);
    final rows = snap['servers'] as List;
    expect(rows.map((r) => r['name']), ['Oracle-SG', 'DMIT-HK', 'Zoro-JP']);
    expect(rows[0], containsPair('status', 'offline'));
    expect(rows[0]['cpu'], '—');
    expect(rows[1], {'id': 1, 'name': 'DMIT-HK', 'status': 'ok', 'cpu': '18%', 'mem': '43%', 'rx': '12 MB/s', 'tx': '2 MB/s'});
  });

  test('【安全】快照不含 IP、供应商、价格等资产信息与任何凭证', () {
    final raw = jsonEncode(widgetSnapshot([node(1, 'a', 'online', cpu: 1)], const [], DateTime(2026)));
    for (final s in ['203.0.113', 'DMIT', '999', 'dev_', 'rt_', 'MNT-']) {
      expect(raw.contains(s), isFalse, reason: s);
    }
  });

  test('状态：维护中与待安装单独显示，最多 8 行', () {
    final m = node(1, 'm', 'offline', extra: {'maintenance': {'id': 1, 'kind': 'maintenance'}});
    expect(widgetStatus(m), 'maintenance');
    expect(widgetStatus(node(2, 'p', 'pending')), 'pending');
    final many = [for (var i = 0; i < 20; i++) node(i, 'n$i', 'online', cpu: 1)];
    expect((widgetSnapshot(many, const [], DateTime(2026))['servers'] as List).length, widgetRows);
  });

  test('内容没变时节流，变了立即发送', () async {
    final p = WidgetPublisher();
    final t = DateTime(2026, 10, 5, 12);
    final a = widgetSnapshot([node(1, 'a', 'online', cpu: 1)], const [], t);
    expect(await p.publish(a, now: t), isTrue);
    final a2 = widgetSnapshot([node(1, 'a', 'online', cpu: 1)], const [], t.add(const Duration(seconds: 10)));
    expect(await p.publish(a2, now: t.add(const Duration(seconds: 10))), isFalse);
    final b = widgetSnapshot([node(1, 'a', 'offline', cpu: 1)], const [], t);
    expect(await p.publish(b, now: t.add(const Duration(seconds: 20))), isTrue);
    expect(await p.publish(b, now: t.add(const Duration(minutes: 16))), isTrue);
  });
}
