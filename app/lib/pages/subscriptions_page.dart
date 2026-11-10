// 订阅（设计 47，修订第 213 条）：与 Web 相同的订阅列表，只读；点开看号码、VPS 配置与备注。
// 修订第 309 条（与 Web 相同，参考 Wallos 与项目所有者提供的截图）：顶部为续订日历、账期进度与月均花费 / 本月待扣三张卡片；
// 列表可筛选（全部 / 订阅中 / 7 天内续订 / 已结束 / 类别）与排序（日期 / 价格 / 名称 / 类别，可倒序），记在本机；
// 每行为续订说明与本期进度条，右侧原币价格、约合人民币与付款周期。
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
import '../widgets/sub_charges.dart';
import 'subscription_add_page.dart';
import 'subscription_analysis_page.dart';
import 'subscription_calendar_page.dart';

/// 订阅页（只读）
class SubscriptionsPage extends StatefulWidget {
  /// 创建订阅页
  const SubscriptionsPage(
      {super.key, required this.api, required this.onRevoked, this.privacy = false, this.embedded = false, this.view = const SubListView(), this.onView});

  /// 筛选与排序（修订第 309 条，本机偏好）
  final SubListView view;

  /// 改了筛选或排序；为空时只在本页生效
  final ValueChanged<SubListView>? onView;

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
  FxRates _fx = const FxRates();
  late SubListView _view = widget.view;
  bool _yearly = false;
  int _budget = 0; // 每月预算（人民币分，修订第 312 条），0 为不设

  void _setView(SubListView v) {
    setState(() => _view = v);
    widget.onView?.call(v);
  }

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final (limited, items, budget) = await widget.api.subscriptions();
      if (!mounted) return;
      setState(() {
        _limited = limited;
        _items = items;
        _budget = budget;
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
    final filters = items == null ? const <(String, String, int)>[] : _filters(items);
    final filter = filters.any((f) => f.$1 == _view.filter) ? _view.filter : 'all';
    final shown = items == null ? <Subscription>[] : sortSubs(items.where((s) => matchesFilter(s, filter)), _view.sort, _view.desc, _fx);

    return Scaffold(
      appBar: widget.embedded ? null : AppBar(title: const Text('订阅')),
      // 新增订阅（修订第 217 条）：只有 AK 允许记录订阅、授权全部节点的设备显示
      floatingActionButton: widget.api.session.allowSubscriptions && !_limited
          ? FloatingActionButton.extended(onPressed: _add, icon: const Icon(Icons.add), label: const Text('新增订阅'))
          : null,
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 8, 12, 88), children: [
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
            _overview(context, items),
            _listBar(context, filters, filter, items),
            if (shown.isEmpty)
              Padding(padding: const EdgeInsets.all(24), child: Text('没有符合筛选条件的订阅。', style: muted, textAlign: TextAlign.center))
            else
              Column(
                key: const ValueKey('sub-list'),
                children: _view.sort == 'category' ? _groupedList(context, shown, items, colors) : [_card(context, shown, colors)],
              ),
          ],
        ]),
      ),
    );
  }

  // 筛选项：（键、名称、数量）
  List<(String, String, int)> _filters(List<Subscription> items) {
    final ended = items.where((s) => s.ended).length;
    final payers = {
      for (final s in items)
        if (s.payer.isNotEmpty) s.payer
    }.toList()
      ..sort();
    return [
      ('all', '全部', items.length),
      ('active', '订阅中', items.length - ended),
      ('soon', '7 天内续订', items.where(renewsSoon).length),
      if (ended > 0) ('ended', '已结束', ended),
      for (final k in allCategoryKeys())
        if (items.any((s) => s.category == k)) (k, catName(k), items.where((s) => s.category == k).length),
      // 付款人（修订第 311 条）：有两个以上不同的付款人时才列出
      if (payers.length > 1)
        for (final p in payers) ('payer:$p', '付款人：$p', items.where((s) => s.payer == p).length),
    ];
  }

  // 一张卡片内的行，行间细线从名称处开始；已结束的半透明
  Widget _card(BuildContext context, List<Subscription> list, StatusColors colors) {
    final c = appColors(context);
    return Card(
      margin: EdgeInsets.zero,
      clipBehavior: Clip.antiAlias,
      child: Column(children: [
        for (final (i, s) in list.indexed) ...[
          if (i > 0) Divider(height: 1, indent: 68, color: c.border),
          Opacity(opacity: s.ended ? 0.55 : 1, child: _row(context, s, colors)),
        ],
      ]),
    );
  }

  // 概览（修订第 309 条）：续订日历、账期进度两张小卡片并排，下方月均花费（点击切换年均）与本月待扣
  Widget _overview(BuildContext context, List<Subscription> items) {
    final c = appColors(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final accent = Theme.of(context).colorScheme.primary;
    final active = items.where((s) => !s.ended).toList();
    String money(int cents) => widget.privacy ? '***' : fmtMoney(cents, 'CNY');

    // 续订日历：今天起 14 天
    const window = 14;
    final upcoming = [
      for (final s in active)
        if (daysUntilCharge(s) case final d? when d >= 0 && d < window) (s, d)
    ]..sort((a, b) => a.$2.compareTo(b.$2));
    final clusters = <(int, List<Subscription>)>[];
    for (final (s, d) in upcoming) {
      if (clusters.isNotEmpty && d - clusters.last.$1 < 3) {
        clusters.last.$2.add(s);
      } else {
        clusters.add((d, [s]));
      }
    }
    final next = ([
      for (final s in active)
        if (daysUntilCharge(s) case final d? when d >= 0) (s, d)
    ]..sort((a, b) => a.$2.compareTo(b.$2)))
        .firstOrNull;
    String dayLabel(int d) {
      final x = DateTime.now().add(Duration(days: d));
      return '${x.month} 月 ${x.day} 日';
    }

    // 账期进度：最接近续订的在前
    final progress = [
      for (final s in active)
        if (billingProgress(s) case final p?) (s, p)
    ]..sort((a, b) => b.$2.compareTo(a.$2));
    final half = progress.where((x) => x.$2 >= 50).length;

    final a = analyze(items, _fx);
    final due = dueRestOfMonth(items, _fx);

    Widget head(String title, int n) => Row(children: [
          Expanded(child: Text(title, style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 15), maxLines: 1, overflow: TextOverflow.ellipsis)),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 1),
            decoration: BoxDecoration(color: accent.withValues(alpha: 0.12), borderRadius: BorderRadius.circular(99)),
            child: Text('$n 项', style: TextStyle(fontSize: 11, color: accent)),
          ),
        ]);

    final calendar = Card(
      margin: EdgeInsets.zero,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          head('续订日历', upcoming.length),
          const SizedBox(height: 6),
          SizedBox(
            height: 50,
            child: LayoutBuilder(builder: (context, box) {
              double x(int d) => box.maxWidth * (0.16 + d / (window - 1) * 0.70);
              return Stack(clipBehavior: Clip.none, children: [
                Positioned(left: box.maxWidth * 0.04, right: box.maxWidth * 0.04, top: 15, child: Container(height: 1.5, color: c.border)),
                Positioned(
                    left: box.maxWidth * 0.04,
                    top: 8,
                    child: Container(width: 3, height: 16, decoration: BoxDecoration(color: accent, borderRadius: BorderRadius.circular(2)))),
                for (final (d, subs) in clusters)
                  Positioned(
                    left: x(d) - 14,
                    top: 2,
                    child: Row(mainAxisSize: MainAxisSize.min, children: [
                      for (final (i, s) in subs.take(2).indexed) Transform.translate(offset: Offset(i * -8.0, 0), child: _avatar(s, size: 26)),
                      if (subs.length > 2) Text('+${subs.length - 2}', style: muted),
                    ]),
                  ),
                if (clusters.isNotEmpty)
                  Positioned(left: x(clusters.first.$1) - 24, top: 32, child: Text(dayLabel(clusters.first.$1), style: muted.copyWith(fontSize: 10))),
              ]);
            }),
          ),
          Row(children: [
            Flexible(child: Text('今天', style: TextStyle(fontSize: 11, color: accent), maxLines: 1, overflow: TextOverflow.ellipsis)),
            const Spacer(),
            Flexible(child: Text('${window - 1} 天后', style: muted.copyWith(fontSize: 11), maxLines: 1, overflow: TextOverflow.ellipsis)),
          ]),
          const SizedBox(height: 6),
          if (next != null) ...[
            Text('下一项续订', style: muted),
            Text(next.$1.name, style: const TextStyle(fontWeight: FontWeight.w600), maxLines: 1, overflow: TextOverflow.ellipsis),
            Text(next.$2 == 0 ? '今天' : (next.$2 < window ? '${next.$2} 天后' : dayLabel(next.$2)), style: TextStyle(fontSize: 12, color: accent)),
          ] else
            Text('没有待续订的订阅', style: muted),
        ]),
      ),
    );

    final billing = Card(
      margin: EdgeInsets.zero,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          head('账期进度', progress.length),
          const SizedBox(height: 8),
          for (final (s, p) in progress.take(4))
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Row(children: [
                _avatar(s, size: 22),
                const SizedBox(width: 8),
                Expanded(child: _bar(context, p, categoryColor(c, s.category))),
              ]),
            ),
          Text(progress.isEmpty ? '没有按周期扣费的订阅' : '$half 项已过半', style: muted),
        ]),
      ),
    );

    final spend = Card(
      margin: EdgeInsets.zero,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 12),
        child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
          IntrinsicHeight(
            child: Row(children: [
              Expanded(
                child: InkWell(
                  key: const ValueKey('sub-yearly'),
                  onTap: () => setState(() => _yearly = !_yearly),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 14),
                    child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                      Row(children: [
                        Icon(Icons.calendar_month_outlined, size: 16, color: c.textMuted),
                        const SizedBox(width: 4),
                        Flexible(child: Text(_yearly ? '年均花费' : '月均花费', style: muted.copyWith(fontSize: 13), maxLines: 1, overflow: TextOverflow.ellipsis)),
                        const SizedBox(width: 4),
                        Icon(Icons.swap_horiz, size: 16, color: c.textMuted),
                      ]),
                      const SizedBox(height: 4),
                      FittedBox(
                        fit: BoxFit.scaleDown,
                        child: Text(a.count == 0 ? dash : money(_yearly ? a.monthly * 12 : a.monthly),
                            style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w700, fontFeatures: [FontFeature.tabularFigures()])),
                      ),
                      if (a.missing.isNotEmpty) Text('不含 ${a.missing.join('、')}（缺少汇率）', style: muted.copyWith(fontSize: 11)),
                    ]),
                  ),
                ),
              ),
              VerticalDivider(width: 1, color: c.border),
              Expanded(
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 14),
                  child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                    Row(children: [
                      Icon(Icons.schedule, size: 16, color: c.textMuted),
                      const SizedBox(width: 4),
                      Expanded(child: Text('本月待扣', style: muted.copyWith(fontSize: 13), maxLines: 1, overflow: TextOverflow.ellipsis)),
                      InkWell(
                        onTap: () => _spendInfo(context, active.length, items.length),
                        child: Icon(Icons.info_outline, size: 18, color: c.textMuted, semanticLabel: '说明'),
                      ),
                    ]),
                    const SizedBox(height: 4),
                    FittedBox(
                      fit: BoxFit.scaleDown,
                      child: Text(money(due.cents),
                          style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w700, fontFeatures: [FontFeature.tabularFigures()])),
                    ),
                    if (due.missing.isNotEmpty) Text('不含 ${due.missing.join('、')}（缺少汇率）', style: muted.copyWith(fontSize: 11)),
                  ]),
                ),
              ),
            ]),
          ),
          // 每月预算（修订第 312 条）：只在面板设了预算时显示，修改请在 Web 中进行
          if (_budget > 0) _budgetLine(context, items),
        ]),
      ),
    );

    return Column(children: [
      IntrinsicHeight(
        child: Row(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
          Expanded(child: calendar),
          const SizedBox(width: 10),
          Expanded(child: billing),
        ]),
      ),
      const SizedBox(height: 10),
      spend,
    ]);
  }

  Widget _budgetLine(BuildContext context, List<Subscription> items) {
    final c = appColors(context);
    final colors = StatusColors.of(context);
    final n = DateTime.now();
    final month = dueRestOfMonth(items, _fx, now: DateTime(n.year, n.month, 1)).cents;
    final pct = month / _budget * 100;
    final color = pct > 100 ? colors.bad : (pct > 90 ? colors.warn : colors.ok);
    String money(int cents) => widget.privacy ? '***' : fmtMoney(cents, 'CNY');
    return Padding(
      padding: const EdgeInsets.fromLTRB(14, 12, 14, 0),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Divider(height: 1, color: c.border),
        const SizedBox(height: 10),
        Text('本月预计 ${money(month)} / 预算 ${money(_budget)}', style: TextStyle(fontSize: 12, color: pct > 100 ? colors.bad : c.textMuted)),
        const SizedBox(height: 6),
        _bar(context, pct.clamp(0, 100).toDouble(), color),
      ]),
    );
  }

  void _spendInfo(BuildContext context, int active, int total) => showDialog<void>(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('说明'),
          content: Text('月均花费：订阅中的订阅按周期折算为每月（年付 ÷ 12、季付 ÷ 3…），换算为人民币；一次性付费不计入。年均为月均 × 12。\n\n'
              '本月待扣：从今天到月底还要扣的金额。\n\n每月预算在 Web 的“订阅”页设置，本月预计扣费超出时经通知渠道提醒一次。\n\n订阅中 $active 个，共记录 $total 个。'),
          actions: [TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('知道了'))],
        ),
      );

  Widget _bar(BuildContext context, double pct, Color color, {double height = 6}) => ClipRRect(
        borderRadius: BorderRadius.circular(99),
        child: LinearProgressIndicator(value: pct / 100, minHeight: height, color: color, backgroundColor: appColors(context).border),
      );

  // 列表标题行：筛选（大字，下拉）、分析、排序与正序 / 倒序
  Widget _listBar(BuildContext context, List<(String, String, int)> filters, String filter, List<Subscription> items) {
    final c = appColors(context);
    final name = filters.firstWhere((f) => f.$1 == filter, orElse: () => filters.first).$2;
    // 系统大字号时“分析”只显示图标，整行放得下
    final large = MediaQuery.textScalerOf(context).scale(14) > 18;
    void analysis() => Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => SubscriptionAnalysisPage(items: items, fx: _fx, privacy: widget.privacy, avatar: _avatar, loadFx: widget.api.fxHistory)));
    return Padding(
      padding: const EdgeInsets.fromLTRB(2, 14, 0, 8),
      child: Row(children: [
        Expanded(
          child: Align(
            alignment: Alignment.centerLeft,
            child: PopupMenuButton<String>(
              key: const ValueKey('sub-filter'),
              tooltip: '筛选',
              onSelected: (v) => _setView(_view.copyWith(filter: v)),
              itemBuilder: (_) => [for (final f in filters) CheckedPopupMenuItem(value: f.$1, checked: f.$1 == filter, child: Text('${f.$2}（${f.$3}）'))],
              child: Row(mainAxisSize: MainAxisSize.min, children: [
                Flexible(child: Text(name, style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w700), maxLines: 1, overflow: TextOverflow.ellipsis)),
                Icon(Icons.expand_more, color: c.textMuted),
              ]),
            ),
          ),
        ),
        // 续订日历（修订第 313 条）
        IconButton(
          key: const ValueKey('sub-calendar'),
          tooltip: '续订日历',
          visualDensity: VisualDensity.compact,
          onPressed: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => SubscriptionCalendarPage(
                  items: items, fx: _fx, privacy: widget.privacy, avatar: _avatar, onOpen: (s) => _detail(context, s)))),
          icon: Icon(Icons.calendar_month_outlined, color: c.textMuted),
        ),
        if (large)
          IconButton(onPressed: analysis, tooltip: '分析', icon: const Icon(Icons.insights_outlined))
        else
          TextButton.icon(
            onPressed: analysis,
            style: TextButton.styleFrom(visualDensity: VisualDensity.compact),
            icon: const Icon(Icons.insights_outlined, size: 18),
            label: const Text('分析'),
          ),
        PopupMenuButton<String>(
          key: const ValueKey('sub-sort'),
          tooltip: '排序',
          onSelected: (v) => _setView(_view.copyWith(sort: v)),
          itemBuilder: (_) => [
            for (final e in subSortNames.entries) CheckedPopupMenuItem(value: e.key, checked: e.key == _view.sort, child: Text(e.value)),
          ],
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 8),
            child: Text(subSortNames[_view.sort] ?? '日期', style: TextStyle(fontSize: 15, color: c.textMuted)),
          ),
        ),
        IconButton(
          key: const ValueKey('sub-desc'),
          tooltip: _view.desc ? '倒序' : '正序',
          visualDensity: VisualDensity.compact,
          onPressed: () => _setView(_view.copyWith(desc: !_view.desc)),
          icon: Icon(_view.desc ? Icons.arrow_downward : Icons.arrow_upward, size: 20, color: c.textMuted),
        ),
      ]),
    );
  }

  // 按类别排序时加组头（类别颜色、名称、数量与月均折算）；已结束的在最后单独一组
  List<Widget> _groupedList(BuildContext context, List<Subscription> shown, List<Subscription> all, StatusColors colors) {
    final c = appColors(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final monthly = {for (final x in analyze(all, _fx).byCategory) x.key: x.cents};
    final groups = <String, List<Subscription>>{};
    for (final s in shown) {
      groups.putIfAbsent(s.ended ? '' : s.category, () => []).add(s);
    }
    return [
      for (final MapEntry(key: k, value: xs) in groups.entries) ...[
        Padding(
          padding: const EdgeInsets.fromLTRB(4, 10, 4, 6),
          child: Row(children: [
            if (k.isNotEmpty) ...[
              Container(width: 10, height: 10, decoration: BoxDecoration(color: categoryColor(c, k), shape: BoxShape.circle)),
              const SizedBox(width: 6),
            ],
            Text(k.isEmpty ? '已结束' : catName(k), style: const TextStyle(fontWeight: FontWeight.w600)),
            const SizedBox(width: 6),
            Text('${xs.length} 个', style: muted),
            const Spacer(),
            if ((monthly[k] ?? 0) > 0) Text('月均 ${widget.privacy ? '***' : fmtMoney(monthly[k]!, 'CNY')}', style: muted),
          ]),
        ),
        _card(context, xs, colors),
      ],
    ];
  }

  // 列表中 VPS 名称旁的“描述  IP”（修订第 325 条）
  String _vpsTag(Subscription s) {
    if (s.category != 'vps') return '';
    final ip = s.vps.ips.isEmpty ? '' : (widget.privacy ? maskIP(s.vps.ips.first) : s.vps.ips.first);
    return [s.note.trim(), ip].where((x) => x.isNotEmpty).join('  ');
  }

  Widget _row(BuildContext context, Subscription s, StatusColors colors) {
    final c = appColors(context);
    final soon = renewsSoon(s);
    final p = billingProgress(s);
    final cny = _cnyText(s);
    return InkWell(
      onTap: () => _detail(context, s),
      onLongPress: widget.api.session.allowSubscriptions ? () => _actions(context, s) : null,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 12, 6, 12),
        child: Row(children: [
          _avatar(s, size: 44),
          const SizedBox(width: 12),
          Expanded(
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text.rich(
                TextSpan(text: s.name, children: [
                  // VPS 名称旁依次是描述（产品备注）与第一个 IP，与 Web 一致（修订第 325 条）；隐私模式下 IP 打码
                  if (_vpsTag(s).isNotEmpty)
                    TextSpan(text: '  ${_vpsTag(s)}', style: TextStyle(fontWeight: FontWeight.w400, fontSize: 12, color: c.textMuted)),
                ]),
                style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              const SizedBox(height: 2),
              Text(renewText(s), style: TextStyle(fontSize: 13, color: soon ? colors.warn : c.textMuted), maxLines: 1, overflow: TextOverflow.ellipsis),
              if (p != null) ...[
                const SizedBox(height: 6),
                FractionallySizedBox(widthFactor: 0.75, child: _bar(context, p, categoryColor(c, s.category), height: 4)),
              ],
            ]),
          ),
          const SizedBox(width: 8),
          // 金额不能截断：系统大字号时整列缩小到放得下，平时布局不变
          ConstrainedBox(
            constraints: BoxConstraints(maxWidth: MediaQuery.sizeOf(context).width * 0.38),
            child: FittedBox(
              fit: BoxFit.scaleDown,
              alignment: Alignment.centerRight,
              child: Column(crossAxisAlignment: CrossAxisAlignment.end, children: [
                Text(_price(s), style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 16, fontFeatures: [FontFeature.tabularFigures()])),
                if (cny != null) Text(cny, style: TextStyle(fontSize: 12, color: c.textMuted, fontFeatures: const [FontFeature.tabularFigures()])),
                Row(mainAxisSize: MainAxisSize.min, children: [
                  Text(cyclePay[s.cycle] ?? s.cycle, style: TextStyle(fontSize: 12, color: c.textMuted)),
                  if (s.cycle != 'once') ...[const SizedBox(width: 3), Icon(Icons.autorenew, size: 14, color: c.textMuted)],
                ]),
              ]),
            ),
          ),
          Icon(Icons.chevron_right, color: c.textMuted),
        ]),
      ),
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
      final x = await Navigator.of(context).push<Subscription>(MaterialPageRoute(builder: (_) => SubscriptionAddPage(api: widget.api, initial: s)));
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
                  Text(catName(s.category), style: muted),
                ]),
              ),
            ]),
            const SizedBox(height: 12),
            line('价格', '${_price(s)}${cycleSuffix[s.cycle] ?? ''}${_cnyText(s) != null ? '（${_cnyText(s)}）' : ''}'),
            if (s.country.isNotEmpty) line('国家地区', '${flagEmoji(s.country)} ${countryLabel(s.country)}'.trim()),
            if (s.payment.isNotEmpty) line('支付方式', paymentNames[s.payment] ?? s.payment),
            if (s.payer.isNotEmpty) line('付款人', s.payer),
            // 链接（修订第 311 条）：可选中复制，App 不直接打开外部网页
            if (s.url.isNotEmpty) line('链接', s.url),
            line('扣费', chargeText(s)),
            if (s.nextCharge.isNotEmpty && !s.ended) line('下次扣费', s.nextCharge),
            line(s.category == 'vps' ? '购买日期' : '开始订阅', s.startDate),
            if (s.trialDays > 0) line('免费试用', '${s.trialDays} 天'),
            if (s.endDate.isNotEmpty) line('结束订阅', s.endDate),
            if (s.phones.isNotEmpty) line('号码', s.phones.map(phone).join('\n')),
            if (s.category == 'vps' && s.vps.summary.isNotEmpty) line('配置', s.vps.summary),
            if (s.category == 'vps' && s.vps.ips.isNotEmpty) line('地址', s.vps.ips.map((ip) => widget.privacy ? maskIP(ip) : ip).join('\n')),
            if (s.note.isNotEmpty) line('备注', s.note),
            // 历史扣费（修订第 294 条）：免费的订阅不显示
            if (s.priceCents > 0) SubCharges(sub: s, privacy: widget.privacy),
            const SizedBox(height: 8),
            Text(widget.api.session.allowSubscriptions ? '长按列表中的订阅可修改或删除；上传图标请在 Web 中进行。' : '修改与删除请在 Web 的“订阅”页进行。',
                style: TextStyle(fontSize: 12, color: c.textMuted)),
          ]),
        ),
      ),
    );
  }
}
