// 从节点数据派生的指标与健康判断：与 web/src/metrics.ts 同一规则，同一节点在 Web 与 App 上结论一致。
import 'models.dart';

enum Level { ok, warn, bad }

/// 配色阈值，与告警默认值一致（设计 16.1）
const _gauge = {
  'cpu': (warn: 70.0, bad: 90.0),
  'mem': (warn: 75.0, bad: 90.0),
  'disk': (warn: 85.0, bad: 95.0),
};

Level gaugeLevel(double? v, String kind) {
  final t = _gauge[kind]!;
  if (v == null) return Level.ok;
  return v >= t.bad ? Level.bad : (v >= t.warn ? Level.warn : Level.ok);
}

class DiskSummary {
  DiskSummary(this.total, this.used, this.usage, this.count, this.fullest, this.level);
  final int total;
  final int used;
  final double usage;
  final int count;
  final DiskInfo fullest;

  /// 合计与最满挂载点中较严重的级别：快满的单个分区不会被合计掩盖（设计 1.5.6）
  final Level level;
}

/// 合计所有挂载点；同一设备只算一次；使用率口径与 df 一致。
DiskSummary? diskSummary(ServerView s) {
  final list = s.latest?.disks ?? const <DiskInfo>[];
  if (list.isEmpty) return null;
  final seen = <String>{};
  var total = 0, used = 0, avail = 0;
  var hasAvail = true;
  for (final d in list) {
    final key = d.device.isNotEmpty ? d.device : d.mount;
    if (!seen.add(key)) continue;
    total += d.total;
    used += d.used;
    if (d.available == null) {
      hasAvail = false;
    } else {
      avail += d.available!;
    }
  }
  final denom = hasAvail ? used + avail : total;
  final usage = denom > 0 ? used / denom * 100 : 0.0;
  final fullest = list.reduce((a, b) => b.usage > a.usage ? b : a);
  final a = gaugeLevel(usage, 'disk');
  final b = gaugeLevel(fullest.usage, 'disk');
  return DiskSummary(total, used, usage, seen.length, fullest, b.index > a.index ? b : a);
}

/// 本周期流量使用率；不限流量时为 null
double? trafficPct(ServerView s) => s.traffic.limit > 0 ? s.traffic.used / s.traffic.limit * 100 : null;

/// 离线或未知时不展示实时指标：旧数值看起来像实时数据，会误导（设计 43.6）
bool isLive(ServerView s) => s.latest != null && (s.status == 'online' || s.status == 'unknown');

String _alertLabel(AlertBrief a) {
  final pct = '${a.value.round()}%';
  return switch (a.type) {
    'cpu' => 'CPU $pct',
    'memory' => '内存 $pct',
    'disk' => '磁盘 $pct',
    'swap' => 'Swap $pct',
    'load' => '负载 ${a.value.toStringAsFixed(1)}×',
    'traffic' => '流量 $pct',
    'traffic_forecast' => '流量预计超额',
    'agent_clock' => '时钟偏差 ${a.value.round()} 秒',
    _ => a.message,
  };
}

class Issue {
  const Issue(this.level, this.text);
  final Level level;
  final String text;
}

/// 需要关注的原因，严重在前；空表示正常（设计 9）。资源与流量问题来自面板的告警引擎。
List<Issue> issues(ServerView s) {
  final out = <Issue>[];
  if (s.maintenance != null) return out; // 维护中：不算需要关注（设计 1.5.15）
  if (s.status == 'offline') out.add(Issue(Level.bad, s.lastSeenAt > 0 ? '离线' : '尚未上报'));
  if (s.status == 'unknown') out.add(const Issue(Level.warn, '上报延迟'));
  for (final a in s.alerts) {
    if (a.type == 'offline' || a.silenced) continue;
    out.add(Issue(a.severity == 'critical' ? Level.bad : Level.warn, _alertLabel(a)));
  }
  out.sort((a, b) => b.level.index - a.level.index);
  return out;
}

/// 本周期还剩几天重置：按面板返回的下一周期开始日计算（设计 1.5.8）
int daysToReset(ServerView s, {DateTime? now}) {
  final end = DateTime.parse('${s.traffic.cycleEnd}T00:00:00');
  final ms = end.difference(now ?? DateTime.now()).inMilliseconds;
  return ms <= 0 ? 0 : (ms / 86400000).ceil();
}

/// 列表排序：异常优先（设计 1.5.3）——有严重问题 → 有警告 → 正常 → 待安装，同级按名称。
int compareServers(ServerView a, ServerView b) {
  int rank(ServerView s) {
    if (s.status == 'pending') return 3;
    final i = issues(s);
    if (i.isEmpty) return 2;
    return i.first.level == Level.bad ? 0 : 1;
  }

  final r = rank(a) - rank(b);
  return r != 0 ? r : a.name.compareTo(b.name);
}
