// 节点标志（修订第 279 条，与 Web 的 lines.ts 同一规则）：IP 协议栈 v4 / v6（Agent 上报）与用户标注的网络线路。
import 'models.dart';

/// 精品线路：标签着强调色，其余为中性色
const _premium = {'CN2 GIA', '9929', 'CUII', 'CMIN2', 'IPLC', 'IEPL'};

/// 是否为精品线路
bool isPremiumLine(String tag) => _premium.contains(tag.toUpperCase());

/// 节点的标志：v4 / v6 在前，线路在后；(文字, 是否着强调色)
List<(String, bool)> nodeBadges(ServerView s) => [
      if (s.latest?.ipv4 == true) ('v4', false),
      if (s.latest?.ipv6 == true) ('v6', false),
      for (final t in s.lineTags) (t, isPremiumLine(t)),
    ];
