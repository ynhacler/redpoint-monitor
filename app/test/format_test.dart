import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/format.dart';
import 'package:vpsmon_app/models.dart';

// 与 web/src/format.ts 同一口径（设计 41.4.1）
void main() {
  test('fmtBytes：十进制单位，< 10 保留 1 位小数', () {
    expect(fmtBytes(0), '0 B');
    expect(fmtBytes(999), '999 B');
    expect(fmtBytes(1500), '1.5 KB');
    expect(fmtBytes(2000000, perSec: true), '2 MB/s');
    expect(fmtBytes(638000000000), '638 GB');
    expect(fmtBytes(null), dash);
  });

  test('fmtTraffic：binary 用 GiB', () {
    expect(fmtTraffic(1073741824, 'binary'), '1 GiB');
    expect(fmtTraffic(1500000000, 'decimal'), '1.5 GB');
  });

  test('fmtPct：取整，<1% ', () {
    expect(fmtPct(0.4), '<1%');
    expect(fmtPct(0), '0%');
    expect(fmtPct(42.6), '43%');
    expect(fmtPct(null), dash);
  });

  test('fmtTime：今天 / 本年 / 更早', () {
    final now = DateTime(2026, 10, 5, 18);
    int unix(DateTime d) => d.millisecondsSinceEpoch ~/ 1000;
    expect(fmtTime(unix(DateTime(2026, 10, 5, 16, 31)), now: now), '16:31');
    expect(fmtTime(unix(DateTime(2026, 10, 2, 16, 31)), now: now), '10-02 16:31');
    expect(fmtTime(unix(DateTime(2025, 10, 2, 16, 31)), now: now), '2025-10-02');
    expect(fmtTime(0), dash);
  });

  test('fmtDuration 与国旗', () {
    expect(fmtDuration(30), '30 秒');
    expect(fmtDuration(240), '4 分钟');
    expect(fmtDuration(7200), '2 小时');
    expect(fmtDuration(3 * 86400), '3 天');
    expect(flagEmoji('hk'), '🇭🇰');
    expect(flagEmoji(''), '');
    expect(flagEmoji('X1'), '');
  });

  test('fmtLatency：与 Web 一致', () {
    expect(fmtLatency(null), '—');
    expect(fmtLatency(0.4), '0.4 ms');
    expect(fmtLatency(5), '5 ms');
    expect(fmtLatency(182.6), '183 ms');
    expect(fmtLatency(1500), '1.5 s');
  });

  test('LatencyView：解析目标、曲线（全部失败的点为 null），旧版面板没有 mode 时按 default', () {
    final v = LatencyView.fromJson({
      'status': 'on',
      'targets': [
        {'id': 1, 'isp': 'ct', 'province': '北京', 'address': '220.181.173.35', 'proto': 'tcp', 'port': 80, 'enabled': true,
          'builtin': true, 'note': '', 'latency_ms': 182.5, 'loss': 0, 'last_at': 100, 'last_error': ''},
        {'id': 95, 'isp': 'intl', 'province': '东京', 'address': '108.61.201.151', 'proto': 'tcp', 'port': 80, 'enabled': false,
          'builtin': true, 'note': 'Vultr', 'latency_ms': null, 'loss': null, 'last_at': 0, 'last_error': ''},
      ],
      'series': {
        '1': [
          {'ts': 100, 'latency_ms': 182.5, 'loss': 0},
          {'ts': 400, 'latency_ms': null, 'loss': 100},
        ],
      },
    });
    expect(v.mode, 'default');
    expect(v.intervalS, 300);
    expect(v.targets[0].latency, 182.5);
    expect(v.targets[1].isp, 'intl');
    expect(v.targets[1].enabled, false);
    expect(v.series[1]![1].latency, isNull);
  });

  test('LatencyOverview：目标名称、按延迟排名（不可达排最后）、按分组筛选', () {
    final d = LatencyOverview.fromJson({
      'servers': [
        {'id': 1, 'name': 'jp', 'group': 'A'},
        {'id': 2, 'name': 'hk', 'group': 'B'},
        {'id': 3, 'name': 'us', 'group': 'A'},
        {'id': 4, 'name': 'sg', 'group': ''},
      ],
      'targets': [
        {'id': 10, 'isp': 'ct', 'province': '上海', 'address': '1.2.3.4', 'proto': 'tcp', 'port': 80, 'enabled': true, 'builtin': false, 'note': ''},
        {'id': 11, 'isp': 'intl', 'province': '东京', 'address': 'speed.example.com', 'proto': 'icmp', 'port': 0, 'enabled': true, 'builtin': false, 'note': 'Vultr'},
      ],
      'cells': [
        {'server_id': 1, 'target_id': 10, 'latency_ms': 80.0, 'loss': 0},
        {'server_id': 2, 'target_id': 10, 'latency_ms': 40.0, 'loss': 2},
        {'server_id': 3, 'target_id': 10, 'latency_ms': null, 'loss': 100},
        {'server_id': 4, 'target_id': 10, 'latency_ms': 80.0, 'loss': 10},
      ],
    });
    expect(d.targets[0].name, '上海电信');
    expect(d.targets[1].name, '东京 Vultr');
    expect([for (final c in d.ranking(10)) c.serverId], [2, 1, 4, 3]); // 同延迟按丢包，不可达最后
    expect([for (final c in d.ranking(10, group: 'A')) c.serverId], [1, 3]);
    expect(d.ranking(11), isEmpty);
    expect(d.groups, ['A', 'B']);
  });

  test('fmtExpiry：与 Web 一致', () {
    final now = DateTime(2026, 10, 12, 20);
    expect(fmtExpiry('', 'monthly', now: now), isNull);
    expect(fmtExpiry('', 'one_time', now: now)!.text, '永久');
    expect(fmtExpiry('2026-10-10', 'monthly', now: now)!, (text: '已过期', level: 'bad', days: -2));
    expect(fmtExpiry('2026-10-12', 'monthly', now: now)!.text, '今天到期');
    expect(fmtExpiry('2026-10-19', 'monthly', now: now)!, (text: '剩7天', level: 'bad', days: 7));
    expect(fmtExpiry('2026-11-01', 'monthly', now: now)!.level, 'warn');
    expect(fmtExpiry('2027-06-01', 'yearly', now: now)!, (text: '2027-06-01', level: '', days: 232));
    expect(fmtExpiry('坏日期', 'monthly', now: now), isNull);
  });

  test('fmtProbeError 与 fmtProbeWhere：与 Web 一致', () {
    expect(fmtProbeError('timeout'), '超时');
    expect(fmtProbeError('dns'), '域名解析失败');
    expect(fmtProbeError('blocked'), '域名只解析到内网地址，已拒绝');
    expect(fmtProbeError('xyz'), '失败');
    expect(fmtProbeError(''), '');
    expect(fmtProbeWhere('tcp', 80, '1.2.3.4'), 'TCP 80 → 1.2.3.4');
    expect(fmtProbeWhere('icmp', 0, 'speed.example.cn'), 'ICMP → speed.example.cn');
  });
}
