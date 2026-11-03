import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/lines.dart';
import 'package:vpsmon_app/models.dart';

void main() {
  test('节点标志（修订第 279 条）：v4 / v6 在前，精品线路着色；旧版面板与 Agent 没有时为空', () {
    ServerView node(Map<String, dynamic> extra) => ServerView.fromJson({
          'id': 1, 'name': 'a', 'status': 'online', 'last_seen_at': 1,
          'traffic': {'cycle_start': '2026-10-01', 'cycle_end': '2026-11-01', 'used': 0, 'limit': 0, 'unit': 'decimal'},
          ...extra,
        });
    const latest = {'cpu': {'usage': 1.0, 'cores': 1}, 'memory': {'usage': 1.0, 'used': 1, 'total': 2}, 'disk': [], 'network': []};
    final dual = node({
      'line_tags': ['CN2 GIA', 'BGP'],
      'latest': {...latest, 'system': {'ip_stack': {'ipv4': true, 'ipv6': true}}},
    });
    expect(nodeBadges(dual), [('v4', false), ('v6', false), ('CN2 GIA', true), ('BGP', false)]);
    final v6only = node({'latest': {...latest, 'system': {'ip_stack': {'ipv4': false, 'ipv6': true}}}});
    expect(nodeBadges(v6only), [('v6', false)]);
    expect(nodeBadges(node({'latest': latest})), isEmpty);
  });
}
