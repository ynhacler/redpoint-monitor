import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/centers.dart';
import 'package:vpsmon_app/pages/me_page.dart';
import 'package:vpsmon_app/session.dart';

Session sess(String host, int device, {String token = 'dev_a'}) => Session(
    server: Uri.parse('https://$host'), deviceId: device, accessToken: token, refreshToken: 'rt_a',
    accessExpiresAt: DateTime(2030), scopeType: 'all', scopeValue: '', allowLowRiskOps: true, pushPrivateKey: 'k$device');

void main() {
  test('添加、替换（同一面板同一设备）、删除与当前中心（设计 1.5.2）', () {
    final c = Centers();
    expect(c.current, isNull);
    expect(c.upsert(sess('a.example.com', 1)), 0);
    expect(c.upsert(sess('b.example.com', 1)), 1);
    expect(c.current!.server.host, 'b.example.com');
    // 同一面板同一设备：替换而不是新增
    expect(c.upsert(sess('a.example.com', 1, token: 'dev_new')), 0);
    expect(c.sessions.length, 2);
    expect(c.sessions[0].accessToken, 'dev_new');
    // 同一面板的新设备（重新配对）：新增
    c.upsert(sess('a.example.com', 2));
    expect(c.sessions.length, 3);
    // 删除当前中心：切到第一个
    c.remove(centerKey(c.current!));
    expect(c.sessions.length, 2);
    expect(c.active, 0);
    // 删除当前之前的中心：当前下标随之前移
    c.active = 1;
    final keep = centerKey(c.current!);
    c.remove(centerKey(c.sessions[0]));
    expect(centerKey(c.current!), keep);
  });

  test('凭证与推送密钥按中心隔离；JSON 往返', () {
    final c = Centers(sessions: [sess('a.example.com', 1), sess('b.example.com', 7)], active: 1);
    final back = Centers.fromJson(c.toJson());
    expect(back.active, 1);
    expect(back.sessions.map((s) => s.pushPrivateKey), ['k1', 'k7']);
    expect(centerKey(back.sessions[1]), 'b.example.com_443_7');
  });

  test('CenterSessionStore：刷新凭证写回自己的一项，吊销时只删除自己', () async {
    final store = MemoryCentersStore();
    final c = Centers(sessions: [sess('a.example.com', 1), sess('b.example.com', 2)]);
    await store.save(c);
    final s = CenterSessionStore(store, c, centerKey(c.sessions[1]));
    await s.save(sess('b.example.com', 2, token: 'dev_refreshed'));
    expect(store.value.sessions[1].accessToken, 'dev_refreshed');
    expect(store.value.sessions[0].accessToken, 'dev_a');
    await s.clear();
    expect(store.value.sessions.map((x) => x.server.host), ['a.example.com']);
  });

  testWidgets('我的：列出监控中心，点其他中心切换，可添加', (tester) async {
    final api = ApiClient(sess('a.example.com', 1), MemorySessionStore());
    int? switched;
    var added = false;
    await tester.pumpWidget(MaterialApp(
      home: MePage(api: api, push: null, privacy: false, onPrivacy: (_) {}, onUnpair: () async {},
          centers: [Uri.parse('https://a.example.com'), Uri.parse('https://b.example.com')],
          onSwitch: (i) => switched = i, onAdd: () => added = true),
    ));
    expect(find.text('当前 · https://a.example.com'), findsOneWidget);
    await tester.tap(find.text('b.example.com'));
    expect(switched, 1);
    await tester.tap(find.text('a.example.com'));
    expect(switched, 1, reason: '点当前中心不切换');
    await tester.tap(find.text('添加监控中心'));
    expect(added, isTrue);
  });
}
