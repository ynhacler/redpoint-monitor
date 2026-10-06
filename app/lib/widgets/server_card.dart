// 节点卡片（设计 13、41.3 ServerCard）：布局与 Web 的节点卡片一致，便于两端对照——
//   头部：国旗、名称、状态点（维护 / 静音标签）；右侧温度、开机时间、1 分钟负载（离线时为离线时长）
//   主体：CPU / 内存 / 磁盘三个环（下方为核数、总量），网络与 IO 两列（速率 + 开机以来累计）
//   告警：需要关注的原因（标签）
//   底部：流量（本计费周期）与进度条
// 收藏星标（1.5.5）；隐私模式隐藏 IP 与供应商（1.5.12）。离线时不显示旧指标（设计 43.6）。
import 'package:flutter/material.dart';

import '../format.dart';
import '../metrics.dart';
import '../models.dart';
import '../privacy.dart';
import '../theme.dart';
import '../tokens.dart';
import 'ring.dart';

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
    final c = appColors(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final live = isLive(s);
    final r = s.latest;
    // 离线 / 尚未上报已写在头部右侧，标签中不再重复
    final probs = issues(s).where((i) => i.text != '离线' && i.text != '尚未上报').toList();
    final pct = trafficPct(s);
    // 边框：离线红、上报延迟橙（与 Web 一致）
    final border = switch (s.status) {
      'offline' => Color.alphaBlend(c.bad.withValues(alpha: 0.45), c.border),
      'unknown' => Color.alphaBlend(c.warn.withValues(alpha: 0.45), c.border),
      _ => c.border,
    };
    final ip = s.ipv4.isNotEmpty ? s.ipv4 : s.ipv6;
    final sub = [if (ip.isNotEmpty) privacy ? maskIP(ip) : ip, if (s.provider.isNotEmpty) maskText(s.provider, privacy), if (s.group.isNotEmpty) s.group];

    return Card(
      clipBehavior: Clip.antiAlias,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12), side: BorderSide(color: border)),
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 10, 14, 10),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            // ---- 头部 ----
            Row(children: [
              if (flagEmoji(s.country).isNotEmpty) Padding(padding: const EdgeInsets.only(right: 6), child: Text(flagEmoji(s.country))),
              Flexible(
                child: Text(s.name, style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16), overflow: TextOverflow.ellipsis),
              ),
              const SizedBox(width: 6),
              Tooltip(message: statusText[s.status] ?? s.status, child: Icon(Icons.circle, size: 9, color: colors.status(s.status))),
              if (s.maintenance != null) ...[const SizedBox(width: 6), _Pill('维护中', c.textMuted, c.border)]
              else if (s.muted != null) ...[const SizedBox(width: 6), _Pill('已静音', c.textMuted, c.border)],
              const Spacer(),
              if (live && r != null)
                DefaultTextStyle.merge(
                  style: TextStyle(fontSize: 12, color: c.textMuted, fontFeatures: const [FontFeature.tabularFigures()]),
                  child: Row(mainAxisSize: MainAxisSize.min, children: [
                    if (r.tempC != null && r.tempC! > 0) ...[
                      _meta(Icons.thermostat, '${r.tempC!.round()}℃', r.tempC! >= 90 ? c.bad : r.tempC! >= 75 ? c.warn : c.ok),
                      const SizedBox(width: 8),
                    ],
                    _meta(Icons.power_settings_new, fmtUptimeShort(r.uptime), null),
                    const SizedBox(width: 8),
                    _meta(Icons.show_chart, r.load1.toStringAsFixed(r.load1 < 10 ? 2 : 1),
                        r.cores > 0 && r.load1 > r.cores ? c.warn : null),
                  ]),
                )
              else
                Text(s.status == 'pending' ? '待安装' : (s.lastSeenAt > 0 ? '离线 ${fmtDuration(DateTime.now().millisecondsSinceEpoch / 1000 - s.lastSeenAt)}' : '尚未上报'),
                    style: TextStyle(fontSize: 12, color: s.status == 'offline' ? c.bad : c.textMuted)),
              SizedBox(
                width: 32,
                height: 28,
                child: IconButton(
                  padding: EdgeInsets.zero,
                  iconSize: 18,
                  icon: Icon(favorite ? Icons.star : Icons.star_border, color: favorite ? colors.warn : c.textMuted),
                  tooltip: favorite ? '取消收藏' : '收藏',
                  onPressed: onFavorite,
                ),
              ),
            ]),
            if (sub.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 2), child: Text(sub.join(' · '), style: muted, overflow: TextOverflow.ellipsis)),
            const SizedBox(height: 8),
            Divider(height: 1, color: c.border),

            // ---- 主体：三个环 + 网络 + IO ----
            if (s.status == 'pending')
              Padding(padding: const EdgeInsets.symmetric(vertical: 12), child: Text('待安装：在 Web 中复制安装命令到主机执行', style: muted))
            else if (live && r != null)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 10),
                child: LayoutBuilder(builder: (context, box) {
                  final ring = (box.maxWidth / 5 - 10).clamp(40.0, 52.0);
                  final disk = diskSummary(s);
                  return Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
                    _ringCol('CPU', r.cpu, colors.level(gaugeLevel(r.cpu, 'cpu')), '${r.cores} 核', ring),
                    _ringCol('Mem', r.mem, colors.level(gaugeLevel(r.mem, 'mem')), fmtBytesShort(r.memTotal), ring),
                    _ringCol('Disk', disk?.usage, colors.level(disk?.level ?? Level.ok), fmtBytesShort(disk?.total), ring),
                    _flowCol('Net', r.tx, r.txTotal, r.rx, r.rxTotal, ring, c),
                    r.io == null
                        ? _flowCol('I/O', null, null, null, null, ring, c)
                        : _flowCol('I/O', r.io!.write, r.io!.writeTotal, r.io!.read, r.io!.readTotal, ring, c),
                  ]);
                }),
              )
            else if (s.lastSeenAt > 0)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 10),
                child: Text('最后上报 ${fmtTime(s.lastSeenAt)}', style: muted),
              )
            else
              const SizedBox.shrink(),

            // ---- 需要关注的原因 ----
            if (probs.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Wrap(spacing: 6, runSpacing: 4, children: [for (final i in probs) Tag(i.text, colors.level(i.level))]),
              ),

            // ---- 底部：流量（主体为空时与头部共用一条分隔线） ----
            if (s.status == 'pending' || (live && r != null) || s.lastSeenAt > 0 || probs.isNotEmpty) Divider(height: 1, color: c.border),
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: DefaultTextStyle.merge(
                style: const TextStyle(fontSize: 12, fontFeatures: [FontFeature.tabularFigures()]),
                child: Row(children: [
                  Text('流量', style: muted),
                  const SizedBox(width: 8),
                  Text(fmtTraffic(s.traffic.used, s.traffic.unit)),
                  Text(' / ${s.traffic.limit > 0 ? fmtTraffic(s.traffic.limit, s.traffic.unit) : '不限'}', style: muted),
                  if (pct != null) ...[
                    const SizedBox(width: 8),
                    Expanded(
                      child: ClipRRect(
                        borderRadius: BorderRadius.circular(2),
                        child: LinearProgressIndicator(
                          value: (pct / 100).clamp(0.0, 1.0),
                          minHeight: 4,
                          backgroundColor: c.track,
                          color: pct >= 95 ? c.bad : (pct >= 80 ? c.warn : c.ok),
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Text(fmtPct(pct), style: TextStyle(color: pct >= 95 ? c.bad : (pct >= 80 ? c.warn : null))),
                  ],
                ]),
              ),
            ),
          ]),
        ),
      ),
    );
  }

  Widget _meta(IconData icon, String text, Color? color) => Row(mainAxisSize: MainAxisSize.min, children: [
        Icon(icon, size: 14, color: color),
        const SizedBox(width: 2),
        Text(text, style: color == null ? null : TextStyle(color: color)),
      ]);

  Widget _ringCol(String label, double? pct, Color color, String cap, double size) => Expanded(
        child: Column(children: [
          Text(label, style: const TextStyle(fontSize: 13)),
          const SizedBox(height: 4),
          Ring(pct: pct, color: color, size: size, label: label),
          const SizedBox(height: 4),
          Text(cap, style: const TextStyle(fontSize: 11), maxLines: 1, overflow: TextOverflow.clip),
        ]),
      );

  /// 网络 / IO：上行（写）速率与累计、下行（读）速率与累计，四行均分与环同高的空间
  Widget _flowCol(String label, int? up, int? upTotal, int? down, int? downTotal, double ring, AppColors c) {
    final t = TextStyle(fontSize: 11, color: c.textMuted);
    Widget v(IconData icon, int? n) => Row(mainAxisSize: MainAxisSize.min, children: [
          Icon(icon, size: 11, color: c.textMuted),
          Text(fmtBytesShort(n), style: const TextStyle(fontSize: 11)),
        ]);
    return Expanded(
      child: Column(children: [
        Text(label, style: const TextStyle(fontSize: 13)),
        const SizedBox(height: 4),
        SizedBox(
          height: ring + 4 + 15,
          child: up == null
              ? Center(child: Text(dash, style: t))
              : FittedBox(
                  fit: BoxFit.scaleDown,
                  child: Column(mainAxisAlignment: MainAxisAlignment.spaceBetween, children: [
                    v(Icons.arrow_upward, up),
                    Text(fmtBytesShort(upTotal), style: t),
                    v(Icons.arrow_downward, down),
                    Text(fmtBytesShort(downTotal), style: t),
                  ]),
                ),
        ),
      ]),
    );
  }
}

/// 维护中 / 已静音：圆角小标签（与 Web 的 .tag 一致）
class _Pill extends StatelessWidget {
  const _Pill(this.text, this.color, this.border);
  final String text;
  final Color color;
  final Color border;

  @override
  Widget build(BuildContext context) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 6),
        decoration: BoxDecoration(border: Border.all(color: border), borderRadius: BorderRadius.circular(99)),
        child: Text(text, style: TextStyle(fontSize: 11, height: 1.6, color: color)),
      );
}

/// 小标签：告警原因
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
