import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/models.dart';
import 'package:vpsmon_app/subscriptions.dart';
import 'package:vpsmon_app/widgets/todo.dart';

ServerView node(int id, String name, String status, {String expire = '', Map<String, dynamic>? traffic}) => ServerView.fromJson({
      'id': id, 'name': name, 'status': status, 'last_seen_at': 1, 'expire_date': expire,
      'traffic': traffic ?? {'cycle_start': '2026-10-01', 'cycle_end': '2026-11-01', 'used': 0, 'limit': 0, 'unit': 'decimal'},
    });

void main() {
  final now = DateTime(2026, 10, 25);
  test('待处理（修订第 242 条）：与 Web 同一规则，严重的在前', () {
    final servers = [
      node(1, '东京', 'online', expire: '2026-10-30'),
      node(2, '香港', 'offline'),
      node(3, '洛杉矶', 'online', traffic: {'cycle_start': '2026-10-01', 'cycle_end': '2026-11-01', 'used': 900, 'limit': 1000, 'unit': 'decimal',
        'forecast': {'daily': 50, 'total': 1300, 'over': true}}),
      node(4, '新加坡', 'online', expire: '2026-12-30'),
      node(5, '待装', 'pending'),
    ];
    const subs = [
      Subscription(id: 1, name: '提醒中', category: 'tools', priceCents: 100, currency: 'CNY', cycle: 'monthly', startDate: '2026-01-27',
          nextCharge: '2026-10-27', remindDays: 3),
      Subscription(id: 2, name: '不提醒', category: 'tools', priceCents: 100, currency: 'CNY', cycle: 'monthly', startDate: '2026-01-27',
          nextCharge: '2026-10-27'),
      Subscription(id: 3, name: '快结束', category: 'video', priceCents: 100, currency: 'CNY', cycle: 'monthly', startDate: '2026-01-01',
          endDate: '2026-10-28', nextCharge: '2026-11-01'),
    ];
    final todos = buildTodos(servers, subs: subs, unlinked: 2, alerts: 3, now: now);
    expect([for (final x in todos) x.text], [
      '香港 离线',
      '3 条告警进行中',
      '洛杉矶 流量约 2 天后用完（重置前）',
      '东京 到期：5 天后',
      '订阅 快结束 结束：3 天后',
      '订阅 提醒中 扣费：2 天后',
      '2 个云实例未关联节点',
    ]);
    expect(todos.first.target, TodoTarget.server);
    expect(todos.first.serverId, 2);
    expect(todos.last.target, TodoTarget.cloud);
  });

  testWidgets('待处理卡片：没有事项时一行说明；默认收成一行显示最紧急的一项，点开看全部（修订第 248 条）', (tester) async {
    Todo row(int i) => Todo('info', '事项 $i', TodoTarget.subscriptions);
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: TodoCard(items: const [], onOpen: (_) {}))));
    expect(find.text('没有待处理的事项'), findsOneWidget);
    Todo? opened;
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: SingleChildScrollView(child: TodoCard(items: [for (var i = 1; i <= 7; i++) row(i)], onOpen: (x) => opened = x)))));
    expect(find.text('事项 1'), findsOneWidget);
    expect(find.text('事项 2'), findsNothing);
    await tester.tap(find.byKey(const ValueKey('todo-toggle')));
    await tester.pump();
    expect(find.text('事项 7'), findsOneWidget);
    expect(opened, isNull);
    await tester.tap(find.text('事项 2'));
    expect(opened?.text, '事项 2');
  });
}
