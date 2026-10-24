// 订阅（设计 47，修订第 213 条）：与 Web 相同的订阅列表，只读。顶部是每月 / 每年花费（按币种），
// 可按类别筛选；7 天内扣费或结束的排在最前并在右上角标红点（修订第 218 条）；点开看号码、VPS 配置与备注。
// AK 允许记录订阅的设备可新增，长按一条可修改或删除（修订第 217、218 条，约束 5 的唯一写入例外）。
import 'dart:typed_data';

import 'package:flutter/material.dart';

import '../api.dart';
import '../app_lock.dart';
import '../catalog.gen.dart';
import '../errors.dart';
import '../format.dart';
import '../privacy.dart';
import '../subscriptions.dart';
import '../theme.dart';
import '../widgets/brand_avatar.dart';
import '../widgets/stale_banner.dart';
import 'subscription_add_page.dart';
import 'subscription_analysis_page.dart';

/// 订阅页（只读）
class SubscriptionsPage extends StatefulWidget {
  /// 创建订阅页
  const SubscriptionsPage({super.key, required this.api, required this.onRevoked, this.privacy = false, this.embedded = false});

  /// 嵌在“工具”页的分页中，不显示自己的标题栏（修订第 216 条）
  final bool embedded;

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
  FxRates _fx = const FxRates();

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
      // 有外币订阅时读取汇率（修订第 223 条）；失败时只显示原币种，人民币合计不计入外币
      if (items.any((s) => s.currency != 'CNY')) {
        widget.api.fxRates().then((fx) {
          if (mounted) setState(() => _fx = fx);
        }).catchError((_) {});
      }
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

  Future<void> _add() async {
    final x = await Navigator.of(context).push<Subscription>(MaterialPageRoute(builder: (_) => SubscriptionAddPage(api: widget.api)));
    if (x == null || !mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('已新增“${x.name}”')));
    await _load();
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

  // 外币价格约合人民币（修订第 223 条）：“≈ ¥52.30”；人民币、没有汇率或隐私模式时为 null
  String? _cnyText(Subscription s) {
    if (s.currency == 'CNY' || widget.privacy) return null;
    final v = _fx.toCny(s.priceCents, s.currency);
    return v == null ? null : '≈ ${fmtMoney(v, 'CNY')}';
  }

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final colors = StatusColors.of(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final items = _items;
    final cats = items == null ? <String>[] : [for (final k in categoryNames.keys) if (items.any((s) => s.category == k)) k];
    if (_category.isNotEmpty && !cats.contains(_category)) _category = '';
    final shown = items?.where((s) => _category.isEmpty || s.category == _category).toList() ?? [];
    // 快到期（7 天内扣费或结束）的排在最前，其余按面板的顺序（下次扣费日）；已结束的放最后
    final active = shown.where((s) => !s.ended).toList();
    final soonOf = {for (final s in active) s.id: dueSoonDays(s)};
    final order = {for (final (i, s) in active.indexed) s.id: i};
    active.sort((a, b) {
      final x = soonOf[a.id], y = soonOf[b.id];
      if (x != null && y != null && x != y) return x.compareTo(y);
      if ((x == null) != (y == null)) return x == null ? 1 : -1;
      return order[a.id]!.compareTo(order[b.id]!);
    });
    final ended = shown.where((s) => s.ended).toList();
    final soon = active.where((s) => soonOf[s.id] != null).length;

    return Scaffold(
      appBar: widget.embedded ? null : AppBar(title: const Text('订阅')),
      // 新增订阅（修订第 217 条）：只有 AK 允许记录订阅、授权全部节点的设备显示
      floatingActionButton: widget.api.session.allowSubscriptions && !_limited
          ? FloatingActionButton.extended(onPressed: _add, icon: const Icon(Icons.add), label: const Text('新增订阅'))
          : null,
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 88), children: [
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
              child: Text(widget.api.session.allowSubscriptions ? '还没有订阅。点右下角“新增订阅”添加，或在 Web 的“订阅”页添加。' : '还没有订阅。在 Web 的“订阅”页添加后，这里会显示扣费日与花费。',
                  style: muted, textAlign: TextAlign.center),
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

  // 概览（修订第 223 条）：进行中的订阅折算为每月花费，统一换算为人民币；点“分析”看分组与排行
  Widget _summary(BuildContext context, List<Subscription> list, int soon) {
    final c = appColors(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final a = analyze(list, _fx);
    String money(int cents) => widget.privacy ? '***' : fmtMoney(cents, 'CNY');
    return Card(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 12, 8, 8),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            Text('每月约', style: muted),
            const Spacer(),
            Text('${list.where((s) => !s.ended).length} 个订阅${soon > 0 ? ' · 快到期 $soon 个' : ''}', style: muted),
            const SizedBox(width: 8),
          ]),
          const SizedBox(height: 2),
          Row(crossAxisAlignment: CrossAxisAlignment.end, children: [
            Expanded(
              child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                Text(a.count == 0 ? dash : money(a.monthly),
                    style: const TextStyle(fontSize: 24, fontWeight: FontWeight.w700, fontFeatures: [FontFeature.tabularFigures()])),
                const SizedBox(height: 2),
                Text('每年约 ${money(a.monthly * 12)}${a.missing.isEmpty ? '' : ' · ${a.missing.join('、')} 无汇率未计入'}', style: muted),
              ]),
            ),
            TextButton.icon(
              onPressed: () => Navigator.of(context).push(MaterialPageRoute(
                  builder: (_) => SubscriptionAnalysisPage(items: list, fx: _fx, privacy: widget.privacy, avatar: _avatar))),
              icon: const Icon(Icons.insights_outlined, size: 18),
              label: const Text('分析'),
            ),
          ]),
        ]),
      ),
    );
  }

  Widget _row(BuildContext context, Subscription s, StatusColors colors) {
    final c = appColors(context);
    final days = daysUntilCharge(s);
    final dueColor = days == null ? c.textMuted : (days <= 1 ? colors.bad : (days <= 7 ? colors.warn : c.textMuted));
    final end = daysUntilEnd(s);
    final due = end != null && end <= 7 && (days == null || end <= days)
        ? (end == 0 ? '今天结束' : '$end 天后结束')
        : (days == null ? '' : (days == 0 ? '今天扣费' : (days == 1 ? '明天扣费' : '$days 天后')));
    final soon = dueSoonDays(s) != null;
    return InkWell(
      onTap: () => _detail(context, s),
      onLongPress: widget.api.session.allowSubscriptions ? () => _actions(context, s) : null,
      child: Stack(children: [
        Padding(
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
              if (s.country.isNotEmpty || s.payment.isNotEmpty)
                Text(
                  [
                    if (s.country.isNotEmpty) '${flagEmoji(s.country)} ${countryLabel(s.country)}'.trim(),
                    if (s.payment.isNotEmpty) paymentNames[s.payment] ?? s.payment,
                  ].join(' · '),
                  style: TextStyle(fontSize: 12, color: c.textMuted),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
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
            if (_cnyText(s) case final t?) Text(t, style: TextStyle(fontSize: 11, color: c.textMuted, fontFeatures: const [FontFeature.tabularFigures()])),
            if (due.isNotEmpty) Text(due, style: TextStyle(fontSize: 12, color: soon ? colors.bad : dueColor)),
          ]),
        ]),
        ),
        // 快到期：右上角红点
        if (soon)
          Positioned(
            top: 6,
            right: 6,
            child: Container(width: 8, height: 8, decoration: BoxDecoration(color: colors.bad, shape: BoxShape.circle)),
          ),
      ]),
    );
  }

  // 长按：修改 / 删除（修订第 218 条）
  Future<void> _actions(BuildContext context, Subscription s) async {
    final act = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (ctx) => SafeArea(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          ListTile(title: Text(s.name, style: const TextStyle(fontWeight: FontWeight.w600)), subtitle: Text(chargeText(s))),
          // 已续费（修订第 224 条）：有结束日期的订阅按周期顺延结束日期
          if (s.endDate.isNotEmpty && s.cycle != 'once')
            ListTile(
              leading: const Icon(Icons.event_repeat_outlined),
              title: const Text('已续费'),
              subtitle: Text('结束日期 ${s.endDate} 顺延一个周期（${cycleNames[s.cycle] ?? s.cycle}）'),
              onTap: () => Navigator.pop(ctx, 'renew'),
            ),
          ListTile(leading: const Icon(Icons.edit_outlined), title: const Text('修改'), onTap: () => Navigator.pop(ctx, 'edit')),
          ListTile(
            leading: Icon(Icons.delete_outline, color: Theme.of(ctx).colorScheme.error),
            title: Text('删除', style: TextStyle(color: Theme.of(ctx).colorScheme.error)),
            onTap: () => Navigator.pop(ctx, 'delete'),
          ),
        ]),
      ),
    );
    if (!context.mounted || act == null) return;
    if (act == 'renew') {
      final lock = AppLockScope.of(context);
      if (lock != null && !await lock.guard('已续费')) return;
      try {
        final x = await widget.api.renewSubscription(s.id);
        if (!mounted) return;
        ScaffoldMessenger.of(this.context).showSnackBar(SnackBar(content: Text('“${x.name}”结束日期已顺延到 ${x.endDate}')));
        await _load();
      } on DeviceRevoked {
        widget.onRevoked();
      } catch (e) {
        if (mounted) ScaffoldMessenger.of(this.context).showSnackBar(SnackBar(content: Text('续费失败：${describeError(e)}')));
      }
      return;
    }
    if (act == 'edit') {
      final x = await Navigator.of(context).push<Subscription>(
          MaterialPageRoute(builder: (_) => SubscriptionAddPage(api: widget.api, initial: s)));
      if (x == null || !mounted) return;
      ScaffoldMessenger.of(this.context).showSnackBar(SnackBar(content: Text('已保存“${x.name}”')));
      await _load();
      return;
    }
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('删除订阅'),
        content: Text('删除“${s.name}”？删除后无法恢复，上传的图标一并删除。'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
          TextButton(onPressed: () => Navigator.pop(ctx, true), child: Text('删除', style: TextStyle(color: Theme.of(ctx).colorScheme.error))),
        ],
      ),
    );
    if (ok != true || !context.mounted) return;
    final lock = AppLockScope.of(context);
    if (lock != null && !await lock.guard('删除订阅')) return;
    try {
      await widget.api.deleteSubscription(s.id);
      if (!mounted) return;
      ScaffoldMessenger.of(this.context).showSnackBar(SnackBar(content: Text('已删除“${s.name}”')));
      await _load();
    } on DeviceRevoked {
      widget.onRevoked();
    } catch (e) {
      if (mounted) ScaffoldMessenger.of(this.context).showSnackBar(SnackBar(content: Text('删除失败：${describeError(e)}')));
    }
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
            line('价格', '${_price(s)}${cycleSuffix[s.cycle] ?? ''}${_cnyText(s) != null ? '（${_cnyText(s)}）' : ''}'),
            if (s.country.isNotEmpty) line('国家地区', '${flagEmoji(s.country)} ${countryLabel(s.country)}'.trim()),
            if (s.payment.isNotEmpty) line('支付方式', paymentNames[s.payment] ?? s.payment),
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
            Text(widget.api.session.allowSubscriptions ? '长按列表中的订阅可修改或删除；上传图标请在 Web 中进行。' : '修改与删除请在 Web 的“订阅”页进行。',
                style: TextStyle(fontSize: 12, color: c.textMuted)),
          ]),
        ),
      ),
    );
  }
}
