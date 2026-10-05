import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/format.dart';

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
}
