// 订阅（设计 47，修订第 213 条）：与 Web 相同的订阅列表，只读。顶部是每月 / 每年花费（按币种），
// 可按类别筛选；7 天内扣费的标出倒计时；点开看号码、VPS 配置与备注。添加与修改在 Web 中进行（约束 5）。
import 'dart:typed_data';

import 'package:flutter/material.dart';

import '../api.dart';
import '../catalog.gen.dart';
import '../errors.dart';
import '../format.dart';
import '../privacy.dart';
import '../subscriptions.dart';
import '../theme.dart';
import '../widgets/brand_avatar.dart';
import '../widgets/stale_banner.dart';

/// 订阅页（只读）
class SubscriptionsPage extends StatefulWidget {
  /// 创建订阅页
  const SubscriptionsPage({super.key, required this.api, required this.onRevoked, this.privacy = false});

  /// 当前监控中心
  final ApiClient api;

  /// 设备授权失效
  final VoidCallback onRevoked;

  /// 隐私模式：隐藏价格、号码与地址
  final bool privacy;

  @override
  State<SubscriptionsPage> createState() => _SubscriptionsPageState();
}

/// 每期价格后的周期简写
const cycleSuffix = {'weekly': '/周', 'monthly': '/月', 'quarterly': '/季', 'semiannually': '/半年', 'annually': '/年', 'once': ' 一次性'};

// 上传的图标按“编号:版本”缓存，切换标签不重复下载
final _logoCache = <String, Uint8List?>{};

class _SubscriptionsPageState extends State<SubscriptionsPage> {
  List<Subscription>? _items;
  bool _limited = false;
  String? _error;
  String _category = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final (limited, items) = await widget.api.subscriptions();
      if (!mounted) return;
      setState(() {
        _limited = limited;
        _items = items;
        _error = null;
      });
      for (final s in items.where((s) => s.logoVersion > 0)) {
        final k = '${s.id}:${s.logoVersion}';
        if (_logoCache.containsKey(k)) continue;
        _logoCache[k] = null;
        widget.api.subscriptionLogo(s.id).then((b) {
          if (b != null && mounted) setState(() => _logoCache[k] = b);
        }).catchError((_) {});
      }
    } on DeviceRevoked {
      widget.onRevoked();
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.status == 404 ? '面板版本较旧，升级面板后可用' : e.message);
    } catch (e) {
      if (mounted) setState(() => _error = describeError(e));
    }
  }

  Widget _avatar(Subscription s, {double size = 40}) {
    final png = _logoCache['${s.id}:${s.logoVersion}'];
    return BrandAvatar(
      image: png != null ? MemoryImage(png) : null,
      logo: s.brand.isEmpty ? null : brandLogos[s.brand],
      glyph: s.glyph,
      color: s.entry?.color ?? paletteColor(s.name),
      letter: s.letter,
      size: size,
    );
  }

  String _price(Subscription s) => widget.privacy ? '***' : fmtMoney(s.priceCents, s.currency);

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final colors = StatusColors.of(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final items = _items;
    final cats = items == null ? <String>[] : [for (final k in categoryNames.keys) if (items.any((s) => s.category == k)) k];
    if (_category.isNotEmpty && !cats.contains(_category)) _category = '';
    final shown = items?.where((s) => _category.isEmpty || s.category == _category).toList() ?? [];
    // 进行中的在前（面板已按下次扣费日排序），已结束的放最后
    final active = shown.where((s) => !s.ended).toList();
    final ended = shown.where((s) => s.ended).toList();
    final soon = active.where((s) => (daysUntilCharge(s) ?? 99) <= 7).length;

    return Scaffold(
      appBar: AppBar(title: const Text('订阅')),
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 24), children: [
          if (_error != null) StaleBanner(error: _error!, note: '下拉可重新加载'),
          if (items == null && _error == null) const Padding(padding: EdgeInsets.all(32), child: Center(child: CircularProgressIndicator())),
          if (_limited)
            Padding(
              padding: const EdgeInsets.all(24),
              child: Text('本机的授权范围不是“全部节点”。订阅属于整个面板，只对全部节点授权的设备显示。', style: muted, textAlign: TextAlign.center),
            )
          else if (items != null && items.isEmpty)
            Padding(
              padding: const EdgeInsets.all(24),
              child: Text('还没有订阅。在 Web 的“订阅”页添加后，这里会显示扣费日与花费。', style: muted, textAlign: TextAlign.center),
            ),
          if (items != null && items.isNotEmpty) ...[
            _summary(context, shown, soon),
            if (cats.length > 1)
              SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.symmetric(vertical: 6),
                child: Row(children: [
                  for (final k in ['', ...cats])
                    Padding(
                      padding: const EdgeInsets.only(right: 6),
                      child: ChoiceChip(
                        label: Text(k.isEmpty ? '全部' : categoryNames[k]!),
                        selected: _category == k,
                        visualDensity: VisualDensity.compact,
                        onSelected: (_) => setState(() => _category = k),
                      ),
                    ),
                ]),
              ),
            Card(
              margin: const EdgeInsets.only(top: 4),
              clipBehavior: Clip.antiAlias,
              child: Column(children: [
                for (final (i, s) in active.indexed) ...[
                  if (i > 0) Divider(height: 1, indent: 64, color: c.border),
                  _row(context, s, colors),
                ],
              ]),
            ),
            if (ended.isNotEmpty) ...[
              Padding(padding: const EdgeInsets.fromLTRB(4, 16, 4, 6), child: Text('已结束', style: muted)),
              Opacity(
                opacity: 0.6,
                child: Card(
                  clipBehavior: Clip.antiAlias,
                  child: Column(children: [
                    for (final (i, s) in ended.indexed) ...[
                      if (i > 0) Divider(height: 1, indent: 64, color: c.border),
                      _row(context, s, colors),
                    ],
                  ]),
                ),
              ),
            ],
          ],
        ]),
      ),
    );
  }

  Widget _summary(BuildContext context, List<Subscription> list, int soon) {
    final c = appColors(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final monthly = monthlyByCurrency(list);
    String money(double cents, String cur) => widget.privacy ? '***' : fmtMoney(cents.round(), cur);
    return Card(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            Text('每月约', style: muted),
            const Spacer(),
            Text('${list.where((s) => !s.ended).length} 个订阅${soon > 0 ? ' · 7 天内扣费 $soon 个' : ''}', style: muted),
          ]),
          const SizedBox(height: 2),
          if (monthly.isEmpty) const Text(dash, style: TextStyle(fontSize: 24, fontWeight: FontWeight.w700)),
          Wrap(spacing: 14, runSpacing: 2, crossAxisAlignment: WrapCrossAlignment.end, children: [
            for (final (i, (cur, cents)) in monthly.indexed)
              Text(money(cents, cur),
                  style: TextStyle(fontSize: i == 0 ? 24 : 16, fontWeight: FontWeight.w700, fontFeatures: const [FontFeature.tabularFigures()])),
          ]),
          if (monthly.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text('每年约 ${monthly.map((m) => money(m.$2 * 12, m.$1)).join(' + ')}', style: muted),
          ],
        ]),
      ),
    );
  }

  Widget _row(BuildContext context, Subscription s, StatusColors colors) {
    final c = appColors(context);
    final days = daysUntilCharge(s);
    final dueColor = days == null ? c.textMuted : (days <= 1 ? colors.bad : (days <= 7 ? colors.warn : c.textMuted));
    final due = days == null ? '' : (days == 0 ? '今天扣费' : (days == 1 ? '明天扣费' : '$days 天后'));
    return InkWell(
      onTap: () => _detail(context, s),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 10, 14, 10),
        child: Row(children: [
          _avatar(s),
          const SizedBox(width: 12),
          Expanded(
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(s.name, style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 15), maxLines: 1, overflow: TextOverflow.ellipsis),
              const SizedBox(height: 2),
              Text('${categoryNames[s.category] ?? s.category} · ${chargeText(s)}',
                  style: TextStyle(fontSize: 12, color: c.textMuted), maxLines: 1, overflow: TextOverflow.ellipsis),
            ]),
          ),
          const SizedBox(width: 8),
          Column(crossAxisAlignment: CrossAxisAlignment.end, children: [
            Text.rich(
              TextSpan(children: [
                TextSpan(text: _price(s), style: const TextStyle(fontWeight: FontWeight.w600)),
                TextSpan(text: cycleSuffix[s.cycle] ?? '', style: TextStyle(fontSize: 11, color: c.textMuted)),
              ]),
              style: const TextStyle(fontFeatures: [FontFeature.tabularFigures()]),
            ),
            if (due.isNotEmpty) Text(due, style: TextStyle(fontSize: 12, color: dueColor)),
          ]),
        ]),
      ),
    );
  }

  void _detail(BuildContext context, Subscription s) {
    final c = appColors(context);
    final muted = TextStyle(fontSize: 13, color: c.textMuted);
    Widget line(String k, String v) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 5),
          child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
            SizedBox(width: 72, child: Text(k, style: muted)),
            Expanded(child: SelectableText(v)),
          ]),
        );
    String phone(SubPhone p) {
      if (!widget.privacy || p.number.length < 4) return '${p.cc} ${p.number}';
      return '${p.cc} ${'*' * (p.number.length - 4)}${p.number.substring(p.number.length - 4)}';
    }

    showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      isScrollControlled: true,
      builder: (_) => SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Row(children: [
              _avatar(s, size: 48),
              const SizedBox(width: 14),
              Expanded(
                child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  Text(s.name, style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700)),
                  Text(categoryNames[s.category] ?? s.category, style: muted),
                ]),
              ),
            ]),
            const SizedBox(height: 12),
            line('价格', '${_price(s)}${cycleSuffix[s.cycle] ?? ''}'),
            line('扣费', chargeText(s)),
            if (s.nextCharge.isNotEmpty && !s.ended) line('下次扣费', s.nextCharge),
            line(s.category == 'vps' ? '购买日期' : '开始订阅', s.startDate),
            if (s.trialDays > 0) line('免费试用', '${s.trialDays} 天'),
            if (s.endDate.isNotEmpty) line('结束订阅', s.endDate),
            if (s.phones.isNotEmpty) line('号码', s.phones.map(phone).join('\n')),
            if (s.category == 'vps' && s.vps.summary.isNotEmpty) line('配置', s.vps.summary),
            if (s.category == 'vps' && s.vps.ips.isNotEmpty) line('地址', s.vps.ips.map((ip) => widget.privacy ? maskIP(ip) : ip).join('\n')),
            if (s.note.isNotEmpty) line('备注', s.note),
            const SizedBox(height: 8),
            Text('添加与修改请在 Web 的“订阅”页进行。', style: TextStyle(fontSize: 12, color: c.textMuted)),
          ]),
        ),
      ),
    );
  }
}
