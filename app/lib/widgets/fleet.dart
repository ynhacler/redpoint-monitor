// 首页的概况部件（设计 1.5.18）：实时概况卡片、即将到期列表、紧凑的节点行。
// 首页原来在“全部正常”时几乎是空的；这里补上全局数字与其余节点的一行摘要，点一行进入详情。
import 'package:flutter/material.dart';

import '../format.dart';
import '../metrics.dart';
import '../models.dart';
import '../theme.dart';

/// 实时概况：总上下行速率、平均 CPU / 内存、本周期流量合计；全部正常时标题旁显示“全部正常”。
class FleetSummaryCard extends StatelessWidget {
  const FleetSummaryCard({super.key, required this.items, required this.allOk});
  final List<ServerView> items;
  final bool allOk;

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final f = fleetStats(items);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    const num = TextStyle(fontSize: 17, fontWeight: FontWeight.w600, fontFeatures: [FontFeature.tabularFigures()]);
    Widget cell(String label, Widget value) => Expanded(
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text(label, style: muted),
            const SizedBox(height: 2),
            value,
          ]),
        );
    Widget pct(double? v, String kind) =>
        Text(fmtPct(v), style: num.copyWith(color: v == null ? null : StatusColors.of(context).level(gaugeLevel(v, kind))));
    return Card(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 12, 14, 14),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            const Text('实时概况', style: TextStyle(fontWeight: FontWeight.w600)),
            const SizedBox(width: 8),
            if (allOk)
              Row(mainAxisSize: MainAxisSize.min, children: [
                Icon(Icons.check_circle, size: 14, color: c.ok),
                const SizedBox(width: 3),
                Text('全部正常', style: TextStyle(fontSize: 12, color: c.ok)),
              ]),
            const Spacer(),
            Text('${f.live} 台在线', style: muted),
          ]),
          const SizedBox(height: 10),
          Row(children: [
            cell('上行', Text(fmtBytesShort(f.tx, perSec: true), style: num)),
            cell('下行', Text(fmtBytesShort(f.rx, perSec: true), style: num)),
            cell('平均 CPU', pct(f.cpu, 'cpu')),
            cell('平均内存', pct(f.mem, 'mem')),
          ]),
          const SizedBox(height: 10),
          Text('本周期流量合计 ${fmtBytes(f.trafficUsed)}', style: muted),
        ]),
      ),
    );
  }
}

/// 30 天内到期（含已过期）的节点，按剩余天数排序。
List<(ServerView, ({String text, String level, int? days}))> expiringSoon(List<ServerView> items, {DateTime? now}) {
  final out = [
    for (final s in items)
      if (fmtExpiry(s.expireDate, s.billingPeriod, now: now) case final e? when e.days != null && e.days! <= 30) (s, e),
  ];
  out.sort((a, b) => a.$2.days!.compareTo(b.$2.days!));
  return out;
}

/// 紧凑的节点行：状态点、国旗、名称，右侧 CPU / 内存与上下行速率（离线时为离线说明）；trailing 可替换右侧内容。
class MiniServerRow extends StatelessWidget {
  const MiniServerRow(this.s, {super.key, required this.onTap, this.trailing});
  final ServerView s;
  final VoidCallback onTap;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final colors = StatusColors.of(context);
    final r = s.latest;
    final small = TextStyle(fontSize: 12, color: c.textMuted, fontFeatures: const [FontFeature.tabularFigures()]);
    Widget right;
    if (trailing != null) {
      right = trailing!;
    } else if (isLive(s) && r != null) {
      right = Column(crossAxisAlignment: CrossAxisAlignment.end, mainAxisSize: MainAxisSize.min, children: [
        Text.rich(TextSpan(style: small, children: [
          const TextSpan(text: 'CPU '),
          TextSpan(text: fmtPct(r.cpu), style: TextStyle(color: colors.level(gaugeLevel(r.cpu, 'cpu')))),
          const TextSpan(text: '  内存 '),
          TextSpan(text: fmtPct(r.mem), style: TextStyle(color: colors.level(gaugeLevel(r.mem, 'mem')))),
        ])),
        Text('↑${fmtBytesShort(r.tx, perSec: true)}  ↓${fmtBytesShort(r.rx, perSec: true)}', style: small),
      ]);
    } else {
      right = Text(statusText[s.status] ?? s.status, style: small.copyWith(color: colors.status(s.status)));
    }
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
        child: Row(children: [
          Icon(Icons.circle, size: 8, color: colors.status(s.status)),
          const SizedBox(width: 8),
          if (flagEmoji(s.country).isNotEmpty) Padding(padding: const EdgeInsets.only(right: 6), child: Text(flagEmoji(s.country))),
          Expanded(child: Text(s.name, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w500))),
          const SizedBox(width: 8),
          right,
        ]),
      ),
    );
  }
}

/// 一组紧凑行放在同一张卡片中，行之间用分隔线。
class MiniList extends StatelessWidget {
  const MiniList({super.key, required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => Card(
        clipBehavior: Clip.antiAlias,
        child: Column(children: [
          for (final (i, w) in children.indexed) ...[if (i > 0) const Divider(height: 1, indent: 14, endIndent: 14), w],
        ]),
      );
}
