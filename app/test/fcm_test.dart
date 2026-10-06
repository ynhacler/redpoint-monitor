import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/fcm.dart';
import 'package:vpsmon_app/push_crypto.dart';
import 'package:vpsmon_app/session.dart';

Session session(String host, String key) => Session(server: Uri.parse('https://$host'), deviceId: 1, accessToken: 'dev_a',
    refreshToken: 'rt_a', accessExpiresAt: DateTime(2030), scopeType: 'all', scopeValue: '', allowLowRiskOps: false,
    pushPrivateKey: key);

void main() {
  final v = (jsonDecode(File('../internal/push/testdata/vectors.json').readAsStringSync()) as List).first as Map<String, dynamic>;

  test('多个中心：依次尝试各自的私钥，用匹配的那把解密（设计 30.3.3）', () async {
    final (other, _) = await generatePushKeyPair();
    final sessions = [session('a.example.com', ''), session('b.example.com', base64.encode(other)), session('c.example.com', v['private_key'] as String)];
    final m = await openWithAnyKey(sessions, v['sealed'] as String);
    expect(m?.title, '🔴 DMIT-HK 已离线');
  });

  test('【安全】没有匹配的私钥时不解出任何内容', () async {
    final (other, _) = await generatePushKeyPair();
    expect(await openWithAnyKey([session('b.example.com', base64.encode(other))], v['sealed'] as String), isNull);
    expect(await openWithAnyKey([session('c.example.com', v['private_key'] as String)], 'bm90LWNpcGhlcnRleHQ='), isNull);
  });

  test('通知编号：同一中心同一节点同一类通知相同，不同节点不同', () {
    PushMessage m(int sid, String kind) => PushMessage(centerId: 'c1', title: '', body: '', severity: 'critical', kind: kind, serverId: sid, ts: 0);
    expect(notificationId(m(1, 'offline')), notificationId(m(1, 'offline')));
    expect(notificationId(m(1, 'offline')), isNot(notificationId(m(2, 'offline'))));
    expect(notificationId(m(1, 'offline')), greaterThanOrEqualTo(0));
  });
}
