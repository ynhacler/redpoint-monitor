import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/models.dart';
import 'package:vpsmon_app/privacy.dart';
import 'package:vpsmon_app/sorting.dart';

ServerView sv(int id, String name, {String status = 'online', double cpu = 10, int used = 0, int limit = 0, String region = '',
        String provider = '', String ipv4 = '', String note = ''}) =>
    ServerView.fromJson({
      'id': id, 'name': name, 'status': status, 'ipv4': ipv4, 'region': region, 'provider': provider, 'note': note,
      'latest': {'cpu': {'usage': cpu, 'cores': 1, 'load1': 0}, 'memory': {'usage': 1, 'used': 1, 'total': 100}, 'disk': [], 'network': []},
      'traffic': {'cycle_start': '2026-10-01', 'cycle_end': '2026-11-01', 'used': used, 'limit': limit, 'unit': 'decimal'},
    });

void main() {
  test('搜索：名称、IP、供应商、地区、备注；多个词同时匹配；不区分大小写', () {
    final s = sv(1, 'DMIT-HK', ipv4: '103.1.2.3', provider: 'DMIT', region: '香港', note: 'proxy main');
    expect(matchesQuery(s, ''), isTrue);
    expect(matchesQuery(s, 'dmit'), isTrue);
    expect(matchesQuery(s, '103.1'), isTrue);
    expect(matchesQuery(s, '香港 proxy'), isTrue);
    expect(matchesQuery(s, '香港 东京'), isFalse);
  });

  test('异常优先：异常 > 离线 > 收藏 > 普通（设计 1.5.6）', () {
    final list = [sv(1, 'a-normal'), sv(2, 'b-fav'), sv(3, 'c-off', status: 'offline'), sv(4, 'd-late', status: 'unknown')];
    expect(sortServers(list, SortBy.smart, {2}).map((s) => s.name), ['c-off', 'd-late', 'b-fav', 'a-normal']);
  });

  test('数值排序：高的在前，离线 / 无数据在后；流量按使用率', () {
    final list = [sv(1, 'low', cpu: 5), sv(2, 'off', status: 'offline', cpu: 99), sv(3, 'high', cpu: 80)];
    expect(sortServers(list, SortBy.cpu, {}).map((s) => s.name), ['high', 'low', 'off']);
    final t = [sv(1, 'unl', used: 999), sv(2, 'half', used: 50, limit: 100), sv(3, 'most', used: 90, limit: 100)];
    expect(sortServers(t, SortBy.traffic, {}).map((s) => s.name), ['most', 'half', 'unl']);
  });

  test('地区 / 供应商：空的在后', () {
    final list = [sv(1, 'x'), sv(2, 'y', region: '香港'), sv(3, 'z', region: '东京')];
    expect(sortServers(list, SortBy.region, {}).map((s) => s.name).last, 'x');
  });

  test('隐私模式打码（设计 1.5.12）', () {
    expect(maskIP('103.1.2.12'), '103.***.***.12');
    expect(maskIP('2001:db8::1'), '2001:…');
    expect(maskText('DMIT', true), '***');
    expect(maskText('DMIT', false), 'DMIT');
  });
}
