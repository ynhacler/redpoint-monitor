import 'dart:convert';

import 'package:cryptography/cryptography.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/push.dart';
import 'package:vpsmon_app/session.dart';

final server = Uri.parse('https://monitor.example.com');
Map<String, dynamic> tokens() => {
      'device_id': 7, 'access_token': 'dev_a', 'refresh_token': 'rt_a', 'expires_in': 1800, 'refresh_expires_in': 1,
      'scope': {'type': 'all', 'value': '', 'allow_low_risk_ops': true},
    };

class FakeSource implements PushTokenSource {
  @override
  bool get available => true;
  @override
  Future<(String, String)?> token() async => ('apns', 'a' * 64);
}

http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

void main() {
  test('配对时提交推送公钥，私钥只保存在会话（与公钥成对）', () async {
    late Map<String, dynamic> sent;
    final client = MockClient((req) async {
      sent = jsonDecode(req.body) as Map<String, dynamic>;
      return ok(tokens());
    });
    final s = await pairDevice(server, 'MNT-X', name: 'iPhone', platform: 'ios', client: client);
    final pub = base64.decode(sent['push_public_key'] as String);
    expect(pub.length, 32);
    final kp = await X25519().newKeyPairFromSeed(base64.decode(s.pushPrivateKey));
    expect((await kp.extractPublicKey()).bytes, pub);
    // 会话 JSON 往返保留私钥
    expect(Session.fromJson(jsonDecode(jsonEncode(s.toJson())) as Map<String, dynamic>).pushPrivateKey, s.pushPrivateKey);
  });

  test('syncPush：面板未启用 / 本构建未接入 / 已登记', () async {
    var available = false;
    final puts = <Map<String, dynamic>>[];
    final client = MockClient((req) async {
      if (req.url.path == '/api/v1/app/me') return ok({'push_available': available});
      if (req.url.path == '/api/v1/app/push' && req.method == 'PUT') {
        puts.add(jsonDecode(req.body) as Map<String, dynamic>);
        return http.Response('', 204);
      }
      return http.Response('', 404);
    });
    final store = MemorySessionStore();
    // 旧版本配对的会话：没有推送私钥
    final api = ApiClient(Session.fromTokens(server, tokens(), DateTime.now()), store, client: client);
    expect(await syncPush(api, FakeSource()), PushState.panelDisabled);
    available = true;
    expect(await syncPush(api, const NoPushTokenSource()), PushState.appUnavailable);
    expect(await syncPush(api, FakeSource()), PushState.enabled);
    expect(puts.single['provider'], 'apns');
    expect(puts.single['token'], 'a' * 64);
    // 没有私钥时生成并提交公钥，私钥写入安全存储
    expect(base64.decode(puts.single['public_key'] as String).length, 32);
    expect(store.value!.pushPrivateKey, isNotEmpty);
    // 已有私钥时不再提交公钥
    await syncPush(api, FakeSource());
    expect(puts.last.containsKey('public_key'), isFalse);
  });
}
