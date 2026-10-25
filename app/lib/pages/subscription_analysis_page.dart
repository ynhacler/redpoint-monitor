// 订阅分析（修订第 223 条）：进行中的订阅按扣费周期折算为月费，换算为人民币后按类别、国家 / 地区、支付方式汇总，并按月费排行。
// 与 Web 订阅页的“分析”同一口径（subscriptions.dart 的 analyze）。
import 'package:flutter/material.dart';

import '../catalog.gen.dart';
import '../format.dart';
import '../subscriptions.dart';
import '../theme.dart';
import '../tokens.dart';

/// 订阅分析页
class SubscriptionAnalysisPage extends StatefulWidget {
  /// 创建
  const SubscriptionAnalysisPage({super.key, required this.items, required this.fx, required this.avatar, this.privacy = false});

  /// 订阅
  final List<Subscription> items;

  /// 汇率
  final FxRates fx;

  /// 订阅的图标（与列表相同）
  final Widget Function(Subscription s, {double size}) avatar;

  /// 隐私模式：金额打码
  final bool privacy;

  @override
  State<SubscriptionAnalysisPage> createState() => _SubscriptionAnalysisPageState();
}

const _groups = {'category': '类别', 'country': '国家地区', 'payment': '支付方式'};
const _cycleShort = {'weekly': '周', 'monthly': '月', 'quarterly': '季', 'semiannually': '半年', 'annually': '年'};

class _SubscriptionAnalysisPageState extends State<SubscriptionAnalysisPage> {
  String _group = 'category';

  String _money(int cents) => widget.privacy ? '***' : fmtMoney(cents, 'CNY');

  List<Color> _palette(AppColors c, StatusColors s) => [c.accent, s.ok, c.series2, c.series3, s.warn, s.bad];

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final colors = StatusColors.of(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final a = analyze(widget.items, widget.fx);
    final rows = switch (_group) { 'country' => a.byCountry, 'payment' => a.byPayment, _ => a.byCategory };
    String name(String key) => switch (_group) {
          'country' => key.isEmpty ? '未设置' : '${flagEmoji(key)} ${countryLabel(key)}'.trim(),
          'payment' => key.isEmpty ? '未设置' : paymentNames[key] ?? key,
          _ => categoryNames[key] ?? key,
        };
    final palette = _palette(c, colors);
    final rate = widget.fx.asOf > 0 ? DateTime.fromMillisecondsSinceEpoch(widget.fx.asOf * 1000) : null;
    final used = {for (final s in widget.items) if (s.currency != 'CNY') s.currency}.toList()..sort();

    return Scaffold(
      appBar: AppBar(title: const Text('订阅分析')),
      body: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 24), children: [
        Card(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 14),
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text('月均折算（人民币）', style: muted),
              const SizedBox(height: 2),
              Text(_money(a.monthly), style: const TextStyle(fontSize: 28, fontWeight: FontWeight.w700, fontFeatures: [FontFeature.tabularFigures()])),
              const SizedBox(height: 4),
              Text('每年约 ${_money(a.monthly * 12)} · ${a.count} 个按周期扣费的订阅 · 平均每个 ${_money(a.count == 0 ? 0 : (a.monthly / a.count).round())}/月',
                  style: muted),
            ]),
          ),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: SegmentedButton<String>(
            segments: [for (final e in _groups.entries) ButtonSegment(value: e.key, label: Text(e.value))],
            selected: {_group},
            showSelectedIcon: false,
            onSelectionChanged: (v) => setState(() => _group = v.first),
          ),
        ),
        Card(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
            child: Column(children: [
              if (rows.isEmpty) Padding(padding: const EdgeInsets.all(12), child: Text('没有进行中的按周期扣费的订阅。', style: muted)),
              for (final (i, r) in rows.indexed)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 6),
                  child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                    Row(children: [
                      Expanded(
                        child: Text.rich(TextSpan(children: [
                          TextSpan(text: name(r.key)),
                          TextSpan(text: '  ${r.pct.round()}%', style: const TextStyle(fontWeight: FontWeight.w700)),
                        ])),
                      ),
                      Text('${_money(r.cents)}/月', style: const TextStyle(fontWeight: FontWeight.w600, fontFeatures: [FontFeature.tabularFigures()])),
                    ]),
                    const SizedBox(height: 4),
                    Row(children: [
                      Expanded(
                        child: ClipRRect(
                          borderRadius: BorderRadius.circular(2),
                          child: LinearProgressIndicator(value: r.pct / 100, minHeight: 5, backgroundColor: c.track, color: palette[i % palette.length]),
                        ),
                      ),
                      const SizedBox(width: 10),
                      Text('${r.count} 个', style: muted),
                    ]),
                  ]),
                ),
            ]),
          ),
        ),
        Padding(padding: const EdgeInsets.fromLTRB(4, 14, 4, 6), child: Text('月费排行（不是按月付费的折算为每月）', style: muted)),
        Card(
          clipBehavior: Clip.antiAlias,
          child: Column(children: [
            for (final (i, (s, cents)) in a.top.take(10).indexed) ...[
              if (i > 0) Divider(height: 1, indent: 56, color: c.border),
              Padding(
                padding: const EdgeInsets.fromLTRB(10, 8, 14, 8),
                child: Row(children: [
                  SizedBox(width: 18, child: Text('${i + 1}', textAlign: TextAlign.center, style: muted)),
                  const SizedBox(width: 6),
                  widget.avatar(s, size: 32),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                      Text(s.name, style: const TextStyle(fontWeight: FontWeight.w600), maxLines: 1, overflow: TextOverflow.ellipsis),
                      Text(
                        widget.privacy
                            ? '***'
                            : '${fmtMoney(s.priceCents, s.currency)}/${_cycleShort[s.cycle] ?? s.cycle}'
                                '${s.cycle == 'monthly' ? '' : ' · 折合 ${fmtMoney(s.monthlyCents.round(), s.currency)}/月'}',
                        style: muted,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ]),
                  ),
                  Text(_money(cents), style: const TextStyle(fontWeight: FontWeight.w700, fontFeatures: [FontFeature.tabularFigures()])),
                ]),
              ),
            ],
          ]),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(4, 10, 4, 0),
          child: Text(
            [
              '月均折算是按周期摊到每月的成本，不等于本月扣款：年付、季付只在扣费的月份扣款。',
              '每周 × 52 ÷ 12、每季 ÷ 3、每半年 ÷ 6、每年 ÷ 12 折算为每月；已结束的不计入。',
              if (a.once > 0) '一次性付费的 ${a.once} 个不计入。',
              if (a.missing.isNotEmpty) '缺少 ${a.missing.join('、')} 的汇率，这些订阅没有计入。',
              if (used.isNotEmpty)
                '汇率：${used.map((cur) => widget.fx.rates[cur] == null ? '$cur 无' : '1 $cur = ¥${widget.fx.rates[cur]!.toStringAsFixed(widget.fx.rates[cur]! < 0.1 ? 4 : 2)}').join('，')}'
                    '${rate != null ? '（${widget.fx.source}，${rate.year}-${rate.month.toString().padLeft(2, '0')}-${rate.day.toString().padLeft(2, '0')}）' : ''}',
            ].join(''),
            style: muted,
          ),
        ),
      ]),
    );
  }
}
