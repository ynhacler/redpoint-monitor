import 'package:flutter/material.dart';

import '../format.dart';
import '../subscriptions.dart';
import '../theme.dart';

/// 订阅详情中的“历史扣费”（修订第 294 条，与 Web 相同）：按周期推算到今天的扣费日，金额按当前价格，不是实际付款记录。
/// 隐私模式下金额打码，日期与次数照常显示。
class SubCharges extends StatefulWidget {
  /// 创建历史扣费
  const SubCharges({super.key, required this.sub, this.privacy = false, this.today});

  /// 订阅
  final Subscription sub;

  /// 隐私模式
  final bool privacy;

  /// 今天（测试用）
  final DateTime? today;

  @override
  State<SubCharges> createState() => _SubChargesState();
}

class _SubChargesState extends State<SubCharges> {
  static const _shown = 12;
  bool _all = false;

  String _money(int cents) => widget.privacy ? '***' : fmtMoney(cents, widget.sub.currency);

  String _ymd(DateTime d) => '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

  @override
  Widget build(BuildContext context) {
    final s = widget.sub;
    final muted = TextStyle(fontSize: 12, color: appColors(context).textMuted);
    final dates = pastCharges(s, today: widget.today);
    final list = _all ? dates : dates.take(_shown).toList();
    return ExpansionTile(
      title: const Text('历史扣费', style: TextStyle(fontSize: 14)),
      tilePadding: EdgeInsets.zero,
      childrenPadding: const EdgeInsets.only(bottom: 8),
      expandedCrossAxisAlignment: CrossAxisAlignment.start,
      shape: const Border(),
      collapsedShape: const Border(),
      children: dates.isEmpty
          ? [Text('还没有到首次扣费日。', style: muted)]
          : [
              Text('共 ${dates.length} 次，合计约 ${_money(s.priceCents * dates.length)}'),
              const SizedBox(height: 6),
              for (final d in list)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 2),
                  child: Row(mainAxisAlignment: MainAxisAlignment.spaceBetween, children: [
                    Text(_ymd(d), style: muted),
                    Flexible(child: Text(_money(s.priceCents), overflow: TextOverflow.ellipsis)),
                  ]),
                ),
              if (dates.length > _shown && !_all)
                TextButton(onPressed: () => setState(() => _all = true), child: Text('显示全部 ${dates.length} 次')),
              const SizedBox(height: 6),
              Text('按开始日期与周期推算，金额按当前价格；改过价格时与实际付款不同，以账单为准。', style: muted),
            ],
    );
  }
}
