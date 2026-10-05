// 节点列表的搜索与排序（设计 1.5.6、1.5.16）：纯函数，有单元测试。
import 'metrics.dart';
import 'models.dart';

/// 排序方式；默认“异常优先”：异常 > 离线 > 收藏 > 普通在线（设计 1.5.6）
enum SortBy { smart, name, cpu, memory, disk, traffic, rx, tx, region, provider }

const sortNames = {
  SortBy.smart: '异常优先',
  SortBy.name: '名称',
  SortBy.cpu: 'CPU 使用率',
  SortBy.memory: '内存使用率',
  SortBy.disk: '磁盘使用率',
  SortBy.traffic: '流量使用率',
  SortBy.rx: '实时下载',
  SortBy.tx: '实时上传',
  SortBy.region: '地区',
  SortBy.provider: '供应商',
};

/// 搜索：名称、IP、供应商、地区、分组、备注、国家代码（不区分大小写，多个词需同时匹配）
bool matchesQuery(ServerView s, String query) {
  final q = query.trim().toLowerCase();
  if (q.isEmpty) return true;
  final hay = [s.name, s.ipv4, s.ipv6, s.provider, s.plan, s.region, s.group, s.note, s.country].join(' ').toLowerCase();
  return q.split(RegExp(r'\s+')).every(hay.contains);
}

/// 排序：数值类降序（高的在前），离线 / 无数据的放最后；同值按名称。
List<ServerView> sortServers(List<ServerView> list, SortBy by, Set<int> favorites) {
  double? metric(ServerView s) {
    if (by == SortBy.traffic) return trafficPct(s);
    if (!isLive(s)) return null;
    final l = s.latest!;
    return switch (by) {
      SortBy.cpu => l.cpu,
      SortBy.memory => l.mem,
      SortBy.disk => diskSummary(s)?.usage,
      SortBy.rx => l.rx.toDouble(),
      SortBy.tx => l.tx.toDouble(),
      _ => null,
    };
  }

  int smartRank(ServerView s) {
    if (s.status == 'pending') return 4;
    final i = issues(s);
    if (i.isNotEmpty) return i.first.level == Level.bad ? 0 : 1;
    return favorites.contains(s.id) ? 2 : 3;
  }

  final out = [...list];
  out.sort((a, b) {
    int r;
    switch (by) {
      case SortBy.smart:
        r = smartRank(a) - smartRank(b);
      case SortBy.name:
        r = 0;
      case SortBy.region:
        r = _blankLast(a.region, b.region);
      case SortBy.provider:
        r = _blankLast(a.provider, b.provider);
      default:
        final x = metric(a), y = metric(b);
        r = x == null && y == null ? 0 : (x == null ? 1 : (y == null ? -1 : y.compareTo(x)));
    }
    return r != 0 ? r : a.name.compareTo(b.name);
  });
  return out;
}

int _blankLast(String a, String b) {
  if (a.isEmpty != b.isEmpty) return a.isEmpty ? 1 : -1;
  return a.compareTo(b);
}
