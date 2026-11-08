// 续订日历（修订第 313 条，与 Web 相同）：按月显示每天扣费的订阅（图标与数量）与当月合计，可前后翻月；
// 点某天在下方列出当天的订阅。金额换算为人民币，缺少汇率的币种不计入合计。周一为每周第一天。
import 'package:flutter/material.dart';

import '../format.dart';
import '../subscriptions.dart';
import '../theme.dart';

/// 续订日历页
class SubscriptionCalendarPage extends StatefulWidget {
  /// 创建
  const SubscriptionCalendarPage({super.key, required this.items, required this.fx, required this.avatar, this.privacy = false, this.onOpen});

  /// 全部订阅
  final List<Subscription> items;

  /// 汇率
  final FxRates fx;

  /// 订阅图标
  final Widget Function(Subscription s, {double size}) avatar;

  /// 隐私模式：隐藏金额
  final bool privacy;

  /// 点当天的订阅时打开详情
  final ValueChanged<Subscription>? onOpen;

  @override
  State<SubscriptionCalendarPage> createState() => _SubscriptionCalendarPageState();
}

class _SubscriptionCalendarPageState extends State<SubscriptionCalendarPage> {
  late final DateTime _today = () {
    final n = DateTime.now();
    return DateTime(n.year, n.month, n.day);
  }();
  int _offset = 0;
  late DateTime? _picked = _today;

  DateTime get _month => DateTime(_today.year, _today.month + _offset, 1);

  void _go(int n) => setState(() {
        _offset += n;
        _picked = _offset == 0 ? _today : null;
      });

  String _money(int cents) => widget.privacy ? '***' : fmtMoney(cents, 'CNY');

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final accent = Theme.of(context).colorScheme.primary;
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final from = _month, to = DateTime(from.year, from.month + 1, 1);
    // 每天的扣费
    final days = <int, List<Subscription>>{};
    var total = 0, count = 0;
    final missing = <String>{};
    for (final s in widget.items) {
      for (final d in chargesBetween(s, from, to)) {
        days.putIfAbsent(d.day, () => []).add(s);
        count++;
        final v = widget.fx.toCny(s.priceCents, s.currency);
        if (v == null) {
          missing.add(s.currency);
        } else {
          total += v;
        }
      }
    }
    final last = DateTime(from.year, from.month + 1, 0).day;
    final lead = (from.weekday + 6) % 7; // 周一为 0
    final picked = _picked != null && _picked!.year == from.year && _picked!.month == from.month ? _picked!.day : null;
    final pickedSubs = picked == null ? const <Subscription>[] : (days[picked] ?? const <Subscription>[]);

    return Scaffold(
      appBar: AppBar(title: const Text('续订日历')),
      body: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 24), children: [
        Row(children: [
          IconButton(tooltip: '上个月', onPressed: () => _go(-1), icon: const Icon(Icons.chevron_left)),
          Expanded(
            child: Column(children: [
              Text('${from.year} 年 ${from.month} 月', style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w700)),
              Text('$count 次扣费 · ${_money(total)}${missing.isEmpty ? '' : '（不含 ${missing.join('、')}）'}',
                  style: muted, textAlign: TextAlign.center, maxLines: 2),
            ]),
          ),
          IconButton(tooltip: '下个月', onPressed: () => _go(1), icon: const Icon(Icons.chevron_right)),
        ]),
        if (_offset != 0) Center(child: TextButton(onPressed: () => _go(-_offset), child: const Text('回到本月'))),
        const SizedBox(height: 6),
        Row(children: [
          for (final w in ['一', '二', '三', '四', '五', '六', '日']) Expanded(child: Text(w, style: muted, textAlign: TextAlign.center)),
        ]),
        const SizedBox(height: 4),
        GridView.count(
          crossAxisCount: 7,
          shrinkWrap: true,
          physics: const NeverScrollableScrollPhysics(),
          mainAxisSpacing: 4,
          crossAxisSpacing: 4,
          childAspectRatio: 0.72,
          children: [
            for (var i = 0; i < lead; i++) const SizedBox.shrink(),
            for (var d = 1; d <= last; d++) _day(context, DateTime(from.year, from.month, d), days[d] ?? const [], picked == d, accent),
          ],
        ),
        if (picked != null) ...[
          const SizedBox(height: 12),
          Text('${from.month} 月 $picked 日', style: muted),
          if (pickedSubs.isEmpty)
            Padding(padding: const EdgeInsets.symmetric(vertical: 8), child: Text('这一天没有扣费。', style: muted))
          else
            for (final s in pickedSubs)
              ListTile(
                contentPadding: EdgeInsets.zero,
                leading: widget.avatar(s, size: 32),
                title: Text(s.name, maxLines: 1, overflow: TextOverflow.ellipsis),
                trailing: Text(widget.privacy ? '***' : fmtMoney(s.priceCents, s.currency),
                    style: const TextStyle(fontWeight: FontWeight.w600, fontFeatures: [FontFeature.tabularFigures()])),
                onTap: widget.onOpen == null ? null : () => widget.onOpen!(s),
              ),
        ],
      ]),
    );
  }

  Widget _day(BuildContext context, DateTime date, List<Subscription> subs, bool picked, Color accent) {
    final c = appColors(context);
    final today = date == _today;
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () => setState(() => _picked = date),
      child: Container(
        decoration: BoxDecoration(
          color: subs.isEmpty ? null : c.surface2,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: picked ? accent : Colors.transparent),
        ),
        padding: const EdgeInsets.symmetric(vertical: 4, horizontal: 2),
        // 系统大字号时整格缩小到放得下
        child: FittedBox(
          fit: BoxFit.scaleDown,
          alignment: Alignment.topCenter,
          child: Column(mainAxisSize: MainAxisSize.min, children: [
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6),
              decoration: today ? BoxDecoration(color: accent, borderRadius: BorderRadius.circular(99)) : null,
              child: Text('${date.day}',
                  style: TextStyle(fontSize: 13, color: today ? Theme.of(context).colorScheme.onPrimary : (date.isBefore(_today) ? c.textMuted : null))),
            ),
            const SizedBox(height: 3),
            if (subs.isNotEmpty)
              Row(mainAxisSize: MainAxisSize.min, children: [
                widget.avatar(subs.first, size: 18),
                if (subs.length > 1) Text('+${subs.length - 1}', style: TextStyle(fontSize: 10, color: c.textMuted)),
              ]),
          ]),
        ),
      ),
    );
  }
}
