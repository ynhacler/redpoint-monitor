// 节点卡片（设计 13）：状态、告警标签、CPU / 内存 / 磁盘、网速、本周期流量；收藏星标（1.5.5）；隐私模式隐藏 IP（1.5.12）。
import 'package:flutter/material.dart';

import '../format.dart';
import '../metrics.dart';
import '../models.dart';
import '../privacy.dart';
import '../theme.dart';

class ServerCard extends StatelessWidget {
  const ServerCard(this.s, {super.key, required this.onTap, required this.favorite, required this.onFavorite, this.privacy = false});
  final ServerView s;
  final VoidCallback onTap;
  final bool favorite;
  final VoidCallback onFavorite;
  final bool privacy;

  @override
  Widget build(BuildContext context) {
    final colors = StatusColors.of(context);
    final muted = TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant);
    final live = isLive(s);
    final disk = diskSummary(s);
    final pct = trafficPct(s);
    final probs = issues(s);
    final ip = s.ipv4.isNotEmpty ? s.ipv4 : s.ipv6;
    return Card(
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 8, 6, 14),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Row(children: [
              if (flagEmoji(s.country).isNotEmpty) Text('${flagEmoji(s.country)} '),
              Expanded(
                child: Text(s.name, style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16), overflow: TextOverflow.ellipsis),
              ),
              Icon(Icons.circle, size: 9, color: colors.status(s.status)),
              const SizedBox(width: 4),
              Text(statusText[s.status] ?? s.status, style: TextStyle(fontSize: 12, color: colors.status(s.status))),
              IconButton(
                icon: Icon(favorite ? Icons.star : Icons.star_border, color: favorite ? colors.warn : null),
                tooltip: favorite ? '取消收藏' : '收藏',
                visualDensity: VisualDensity.compact,
                onPressed: onFavorite,
              ),
            ]),
            Padding(
              padding: const EdgeInsets.only(right: 8),
              child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                if (ip.isNotEmpty || s.provider.isNotEmpty)
                  Text(
                    [if (ip.isNotEmpty) privacy ? maskIP(ip) : ip, if (s.provider.isNotEmpty) maskText(s.provider, privacy), if (s.group.isNotEmpty) s.group]
                        .join(' · '),
                    style: muted,
                  ),
                if (s.maintenance != null || s.muted != null || probs.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: Wrap(spacing: 6, runSpacing: 4, children: [
                      if (s.maintenance != null) Tag('维护中', colors.muted),
                      if (s.muted != null) Tag('已静音', colors.muted),
                      for (final i in probs) Tag(i.text, colors.level(i.level)),
                    ]),
                  ),
                const SizedBox(height: 10),
                if (s.status == 'pending')
                  Text('待安装：在 Web 中复制安装命令到主机执行', style: muted)
                else if (live) ...[
                  Row(children: [
                    _Stat('CPU', s.latest!.cpu, gaugeLevel(s.latest!.cpu, 'cpu')),
                    _Stat('内存', s.latest!.mem, gaugeLevel(s.latest!.mem, 'mem')),
                    _Stat('磁盘', disk?.usage, disk?.level ?? Level.ok),
                  ]),
                  const SizedBox(height: 8),
                  Text('↓ ${fmtBytes(s.latest!.rx, perSec: true)}    ↑ ${fmtBytes(s.latest!.tx, perSec: true)}', style: muted),
                ] else
                  Text(s.lastSeenAt > 0 ? '最后上报 ${fmtTime(s.lastSeenAt)}' : '尚未上报', style: muted),
                const SizedBox(height: 8),
                Text(
                  s.traffic.limit > 0
                      ? '本周期 ${fmtTraffic(s.traffic.used, s.traffic.unit)} / ${fmtTraffic(s.traffic.limit, s.traffic.unit)}'
                      : '本周期 ${fmtTraffic(s.traffic.used, s.traffic.unit)} · 不限',
                  style: muted,
                ),
                if (pct != null)
                  Padding(
                    padding: const EdgeInsets.only(top: 4),
                    child: LinearProgressIndicator(
                      value: (pct / 100).clamp(0.0, 1.0),
                      color: pct >= 95 ? colors.bad : (pct >= 80 ? colors.warn : null),
                    ),
                  ),
              ]),
            ),
          ]),
        ),
      ),
    );
  }
}

class _Stat extends StatelessWidget {
  const _Stat(this.label, this.value, this.level);
  final String label;
  final double? value;
  final Level level;

  @override
  Widget build(BuildContext context) {
    final c = StatusColors.of(context);
    return Expanded(
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Text(label, style: TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant)),
        Text(fmtPct(value), style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600, color: level == Level.ok ? null : c.level(level))),
      ]),
    );
  }
}

/// 小标签：告警、维护、静音
class Tag extends StatelessWidget {
  const Tag(this.text, this.color, {super.key});
  final String text;
  final Color color;

  @override
  Widget build(BuildContext context) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
        decoration: BoxDecoration(border: Border.all(color: color), borderRadius: BorderRadius.circular(6)),
        child: Text(text, style: TextStyle(fontSize: 11, color: color)),
      );
}
