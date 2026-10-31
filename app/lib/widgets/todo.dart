// 待处理（修订第 242 条，与 Web 仪表板的 TodoCard 同一规则，修订第 241 条）：首页把需要动手处理的事项列在一起，点一行直达。
//   离线节点、进行中的告警、本周期流量已用完或预计重置前用完、7 天内到期的节点、7 天内结束的订阅、
//   到了“扣费前提醒”天数内的订阅（不提醒的不列出）、未关联节点的云实例
// 严重的在前，默认 5 项、其余展开；没有事项时一行“没有待处理的事项”。订阅与云实例只在授权“全部节点”的设备上有数据。
import 'package:flutter/material.dart';

import '../models.dart';
import '../subscriptions.dart';
import '../theme.dart';

/// 待处理事项的去向
enum TodoTarget {
  /// 节点详情（[Todo.serverId]）
  server,

  /// 事件页（告警）
  events,

  /// 工具 → 订阅
  subscriptions,

  /// 工具 → 云平台
  cloud,
}

/// 一条待处理事项
class Todo {
  /// 创建
  const Todo(this.level, this.text, this.target, {this.serverId});

  /// bad / warn / info
  final String level;

  /// 说明
  final String text;

  /// 去向
  final TodoTarget target;

  /// 去向为节点时的节点 ID
  final int? serverId;
}

String _when(int d) => d < 0 ? '已过期 ${-d} 天' : (d == 0 ? '今天' : '$d 天后');

int? _daysTo(String ymd, DateTime today) {
  final p = ymd.split('-');
  if (p.length != 3) return null;
  final d = DateTime(int.parse(p[0]), int.parse(p[1]), int.parse(p[2]));
  // 按 UTC 的年月日相减，跨夏令时切换也不差一天（修订第 272 条）
  return DateTime.utc(d.year, d.month, d.day).difference(DateTime.utc(today.year, today.month, today.day)).inDays;
}

/// 由节点、订阅与未关联的云实例数得出待处理事项，严重的在前
List<Todo> buildTodos(List<ServerView> servers, {List<Subscription> subs = const [], int unlinked = 0, int alerts = 0, DateTime? now}) {
  final today = now ?? DateTime.now();
  final installed = servers.where((s) => s.status != 'pending').toList();
  final out = <Todo>[];
  final offline = installed.where((s) => s.status == 'offline' && s.maintenance == null).toList();
  if (offline.length == 1) {
    out.add(Todo('bad', '${offline.first.name} 离线', TodoTarget.server, serverId: offline.first.id));
  } else if (offline.length > 1) {
    out.add(Todo('bad', '${offline.length} 台节点离线', TodoTarget.server, serverId: offline.first.id));
  }
  if (alerts > 0) out.add(Todo('warn', '$alerts 条告警进行中', TodoTarget.events));
  for (final s in installed) {
    final t = s.traffic;
    if (t.limit <= 0) continue;
    if (t.used >= t.limit) {
      out.add(Todo('bad', '${s.name} 本周期流量已用完', TodoTarget.server, serverId: s.id));
    } else if (t.forecast != null && t.forecast!.over && t.forecast!.daily > 0) {
      out.add(Todo('warn', '${s.name} 流量约 ${(t.limit - t.used) ~/ t.forecast!.daily} 天后用完（重置前）', TodoTarget.server, serverId: s.id));
    }
  }
  for (final s in installed) {
    final d = s.expireDate.isEmpty ? null : _daysTo(s.expireDate, today);
    if (d == null || d > 7 || d < -30) continue;
    out.add(Todo(d <= 3 ? 'bad' : 'warn', '${s.name} 到期：${_when(d)}', TodoTarget.server, serverId: s.id));
  }
  for (final s in subs) {
    if (s.ended) continue;
    final end = s.endDate.isEmpty ? null : _daysTo(s.endDate, today);
    final charge = s.nextCharge.isEmpty ? null : _daysTo(s.nextCharge, today);
    if (end != null && end >= 0 && end <= 7) {
      out.add(Todo('warn', '订阅 ${s.name} 结束：${_when(end)}', TodoTarget.subscriptions));
    } else if (charge != null && charge >= 0 && s.remindDays > 0 && charge <= s.remindDays) {
      out.add(Todo('info', '订阅 ${s.name} 扣费：${_when(charge)}', TodoTarget.subscriptions));
    }
  }
  if (unlinked > 0) out.add(Todo('info', '$unlinked 个云实例未关联节点', TodoTarget.cloud));
  const rank = {'bad': 0, 'warn': 1, 'info': 2};
  final indexed = out.indexed.toList()
    ..sort((a, b) => rank[a.$2.level]! - rank[b.$2.level]! != 0 ? rank[a.$2.level]! - rank[b.$2.level]! : a.$1 - b.$1);
  return [for (final x in indexed) x.$2];
}

/// 首页的待处理卡片
class TodoCard extends StatefulWidget {
  /// 创建
  const TodoCard({super.key, required this.items, required this.onOpen});

  /// 事项
  final List<Todo> items;

  /// 点一行
  final ValueChanged<Todo> onOpen;

  @override
  State<TodoCard> createState() => _TodoCardState();
}

class _TodoCardState extends State<TodoCard> {
  // 默认收成一行（修订第 248 条，UI 精修 UI-007）：只显示数量与最紧急的一项，点开看全部，节点卡片更早出现在首屏
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final colors = StatusColors.of(context);
    final items = widget.items;
    Color dot(String l) => switch (l) { 'bad' => colors.bad, 'warn' => colors.warn, _ => c.accent };
    return Card(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 6, 6, 6),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          InkWell(
            key: const ValueKey('todo-toggle'),
            onTap: items.isEmpty ? null : () => setState(() => _expanded = !_expanded),
            child: ConstrainedBox(
              constraints: const BoxConstraints(minHeight: 40),
              child: Row(children: [
                const Text('待处理', style: TextStyle(fontWeight: FontWeight.w600)),
                const SizedBox(width: 8),
                if (items.isEmpty) ...[
                  Icon(Icons.check_circle, size: 14, color: c.ok),
                  const SizedBox(width: 4),
                  Text('没有待处理的事项', style: TextStyle(fontSize: 12, color: c.ok)),
                ] else ...[
                  Text('${items.length}', style: TextStyle(fontSize: 12, color: c.textMuted, fontFeatures: const [FontFeature.tabularFigures()])),
                  const SizedBox(width: 10),
                  if (!_expanded) ...[
                    Container(width: 8, height: 8, decoration: BoxDecoration(color: dot(items.first.level), shape: BoxShape.circle)),
                    const SizedBox(width: 8),
                  ],
                  Expanded(
                    child:
                        Text(_expanded ? '' : items.first.text, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14)),
                  ),
                  Icon(_expanded ? Icons.expand_less : Icons.expand_more, size: 20, color: c.textMuted),
                ],
              ]),
            ),
          ),
          if (_expanded)
            for (final x in items)
              InkWell(
                onTap: () => widget.onOpen(x),
                child: Padding(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  child: Row(children: [
                    Container(width: 8, height: 8, decoration: BoxDecoration(color: dot(x.level), shape: BoxShape.circle)),
                    const SizedBox(width: 10),
                    Expanded(child: Text(x.text, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14))),
                    Icon(Icons.chevron_right, size: 18, color: c.textMuted),
                  ]),
                ),
              ),
        ]),
      ),
    );
  }
}
