import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/subscriptions.dart';

void main() {
  test('订阅列表搜索（修订第 326 条）：名称、描述、IP、号码、付款人、支付方式、国家；多个词都要出现，不分大小写', () {
    const vps = Subscription(id: 1, name: '腾讯云', category: 'vps', priceCents: 3850, currency: 'CNY', cycle: 'monthly', startDate: '2026-01-01',
        note: '轻量 2C4G', payer: '老王', payment: 'wechat', country: 'CN', vps: SubVPS(ips: ['129.204.23.119'], location: '广州'));
    const sim = Subscription(id: 2, name: 'GiffGaff', category: 'sim', priceCents: 1000, currency: 'GBP', cycle: 'monthly', startDate: '2026-01-01',
        phones: [SubPhone('+44', '7700900123')], country: 'GB');
    expect(subMatchesQuery(vps, ''), isTrue);
    expect(subMatchesQuery(vps, '  '), isTrue);
    expect(subMatchesQuery(vps, '129.204'), isTrue);
    expect(subMatchesQuery(vps, '2c4g'), isTrue); // 不分大小写
    expect(subMatchesQuery(vps, '腾讯 广州'), isTrue);
    expect(subMatchesQuery(vps, '老王'), isTrue);
    expect(subMatchesQuery(vps, '微信'), isTrue);
    expect(subMatchesQuery(vps, '中国'), isTrue);
    expect(subMatchesQuery(vps, '腾讯 东京'), isFalse); // 每个词都要出现
    expect(subMatchesQuery(sim, '+447700'), isTrue);
    expect(subMatchesQuery(sim, '7700900'), isTrue);
    expect(subMatchesQuery(sim, '129.204'), isFalse);
  });
}
