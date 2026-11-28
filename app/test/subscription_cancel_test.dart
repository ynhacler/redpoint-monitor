import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/subscriptions.dart';

void main() {
  test('已终止的订阅（修订第 343、344 条）：显示到期日，本期进度算到结束日期', () {
    final now = DateTime(2026, 11, 28);
    final s = Subscription.fromJson({
      'id': 1, 'name': 'VPS', 'category': 'vps', 'price_cents': 3850, 'currency': 'CNY', 'cycle': 'monthly',
      'start_date': '2026-10-10', 'end_date': '2026-12-10', 'next_charge': '', 'cancelled': true,
    });
    expect(s.cancelled, isTrue);
    expect(renewText(s, now: now), '12 月 10 日到期');
    final p = billingProgress(s, now: now);
    expect(p, isNotNull);
    expect(p!, closeTo(18 / 30 * 100, 0.5)); // 11-10 → 12-10，已过 18 天
    // 旧面板没有 cancelled 字段：默认为 false
    expect(Subscription.fromJson({'id': 2, 'name': 'x', 'category': 'tools', 'price_cents': 1, 'currency': 'CNY', 'cycle': 'monthly',
      'start_date': '2026-01-01'}).cancelled, isFalse);
  });
}
