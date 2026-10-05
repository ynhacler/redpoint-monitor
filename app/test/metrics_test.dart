import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/metrics.dart';
import 'package:vpsmon_app/models.dart';

Map<String, dynamic> server({
  String name = 'a',
  String status = 'online',
  List<Map<String, dynamic>> disks = const [],
  List<Map<String, dynamic>> alerts = const [],
  Map<String, dynamic>? maintenance,
  int used = 0,
  int limit = 0,
  String cycleEnd = '2026-11-01',
}) =>
    {
      'id': 1, 'name': name, 'status': status, 'group': '', 'country': 'JP', 'last_seen_at': 100,
      'latest': {
        'cpu': {'usage': 50, 'cores': 2, 'load1': 0.5},
        'memory': {'usage': 40, 'used': 400, 'total': 1000},
        'disk': disks,
        'network': [{'rx_speed': 10, 'tx_speed': 20}, {'rx_speed': 1, 'tx_speed': 2}],
        'system': {'uptime': 3600},
      },
      'traffic': {'cycle_start': '2026-10-01', 'cycle_end': cycleEnd, 'used': used, 'limit': limit, 'unit': 'decimal'},
      'alerts': alerts,
      'maintenance': maintenance,
    };

ServerView sv(Map<String, dynamic> j) => ServerView.fromJson(j);

void main() {
  test('解析节点：网速合计、未知字段忽略', () {
    final s = sv({...server(), 'future_field': 1});
    expect(s.latest!.rx, 11);
    expect(s.latest!.tx, 22);
    expect(s.latest!.uptime, 3600);
    expect(s.country, 'JP');
  });

  test('磁盘合计：同一设备只算一次；快满的分区不被合计掩盖（设计 1.5.6）', () {
    final s = sv(server(disks: [
      {'mount': '/', 'device': '/dev/vda1', 'total': 100, 'used': 20, 'usage': 20, 'available': 80},
      {'mount': '/srv', 'device': '/dev/vda1', 'total': 100, 'used': 20, 'usage': 20, 'available': 80},
      {'mount': '/data', 'device': '/dev/vdb', 'total': 100, 'used': 96, 'usage': 96, 'available': 4},
    ]));
    final d = diskSummary(s)!;
    expect(d.count, 2);
    expect(d.total, 200);
    expect(d.usage, closeTo(58, 0.01));
    expect(d.fullest.mount, '/data');
    expect(d.level, Level.bad);
  });

  test('需要关注：离线、上报延迟、告警；已静音与离线告警不重复；维护中不算', () {
    expect(issues(sv(server())), isEmpty);
    expect(issues(sv(server(status: 'offline'))).single.text, '离线');
    final alerts = [
      {'type': 'offline', 'severity': 'critical', 'message': 'x', 'value': 200, 'fired_at': 1, 'silenced': false},
      {'type': 'cpu', 'severity': 'warning', 'message': 'x', 'value': 95.4, 'fired_at': 1, 'silenced': false},
      {'type': 'disk', 'severity': 'critical', 'message': 'x', 'value': 96, 'fired_at': 1, 'silenced': false},
      {'type': 'memory', 'severity': 'warning', 'message': 'x', 'value': 91, 'fired_at': 1, 'silenced': true},
    ];
    final i = issues(sv(server(alerts: alerts)));
    expect(i.map((e) => e.text), ['磁盘 96%', 'CPU 95%']);
    final m = {'id': 3, 'kind': 'maintenance', 'ends_at': null, 'created_by': 'app:phone'};
    expect(issues(sv(server(status: 'offline', maintenance: m))), isEmpty);
  });

  test('异常优先排序：严重 → 警告 → 正常 → 待安装', () {
    final list = [
      sv(server(name: 'c-ok')),
      sv(server(name: 'd-pending', status: 'pending')),
      sv(server(name: 'b-late', status: 'unknown')),
      sv(server(name: 'a-off', status: 'offline')),
    ]..sort(compareServers);
    expect(list.map((s) => s.name), ['a-off', 'b-late', 'c-ok', 'd-pending']);
  });

  test('流量使用率与距离重置', () {
    expect(trafficPct(sv(server())), isNull);
    expect(trafficPct(sv(server(used: 638, limit: 1000))), closeTo(63.8, 0.001));
    expect(daysToReset(sv(server(cycleEnd: '2026-10-17')), now: DateTime(2026, 10, 5, 12)), 12);
    expect(daysToReset(sv(server(cycleEnd: '2026-10-01')), now: DateTime(2026, 10, 5)), 0);
  });

  test('离线时不显示实时指标（设计 43.6）', () {
    expect(isLive(sv(server())), isTrue);
    expect(isLive(sv(server(status: 'offline'))), isFalse);
  });

  test('历史点：内存与磁盘换算为百分比', () {
    final p = MetricPoint.fromJson({'ts': 1, 'cpu': 12.5, 'mem_used': 250, 'mem_total': 1000, 'disk_used': 30, 'disk_total': 0,
      'rx_speed': 5, 'tx_speed': 6});
    expect(p.mem, 25);
    expect(p.disk, 0);
  });
}
