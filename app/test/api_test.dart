import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/session.dart';

final server = Uri.parse('https://monitor.example.com');

Map<String, dynamic> tokens(int n) => {
      'device_id': 7,
      'access_token': 'dev_access$n',
      'refresh_token': 'rt_refresh$n',
      'expires_in': 1800,
      'refresh_expires_in': 7776000,
      'scope': {'type': 'all', 'value': '', 'allow_low_risk_ops': true},
    };

http.Response err(int status, String code) =>
    http.Response(jsonEncode({'error': {'code': code, 'message': '错误 $code', 'request_id': 'r_1'}}), status,
        headers: {'content-type': 'application/json; charset=utf-8'});

http.Response ok(Object body) => http.Response.bytes(utf8.encode(jsonEncode(body)), 200,
    headers: {'content-type': 'application/json; charset=utf-8'});

const serverJson = {
  'id': 1, 'name': 'tokyo', 'status': 'online',
  'latest': {'cpu': {'usage': 12.5}, 'memory': {'usage': 40}, 'network': [{'rx_speed': 1000, 'tx_speed': 2000}]},
  'traffic': {'used': 5000, 'limit': 10000},
};

void main() {
  test('配对：发送 AK 与设备信息，返回会话', () async {
    late Map<String, dynamic> sent;
    final client = MockClient((req) async {
      expect(req.url.toString(), 'https://monitor.example.com/api/v1/app/pair');
      sent = jsonDecode(req.body) as Map<String, dynamic>;
      return ok(tokens(1));
    });
    final s = await pairDevice(server, 'MNT-X', name: 'iPhone', platform: 'ios', client: client);
    expect(sent['access_key'], 'MNT-X');
    expect((sent['device'] as Map)['platform'], 'ios');
    expect(s.accessToken, 'dev_access1');
    expect(s.deviceId, 7);
  });

  test('配对失败：显示面板的中文提示', () async {
    final client = MockClient((_) async => err(400, 'access_key_invalid'));
    expect(() => pairDevice(server, 'MNT-X', name: 'a', platform: 'ios', client: client),
        throwsA(isA<ApiException>().having((e) => e.code, 'code', 'access_key_invalid')));
  });

  test('Access Token 过期：刷新一次后重试，新凭证写入存储', () async {
    final store = MemorySessionStore();
    var refreshes = 0;
    final client = MockClient((req) async {
      if (req.url.path == '/api/v1/app/token/refresh') {
        refreshes++;
        expect((jsonDecode(req.body) as Map)['refresh_token'], 'rt_refresh1');
        return ok(tokens(2));
      }
      if (req.headers['Authorization'] == 'Bearer dev_access1') return err(401, 'token_expired');
      expect(req.headers['Authorization'], 'Bearer dev_access2');
      return ok({'items': [serverJson], 'next_cursor': ''});
    });
    final s = Session.fromTokens(server, tokens(1), DateTime.now());
    final api = ApiClient(s, store, client: client);
    final list = await api.servers();
    expect(list.single.name, 'tokyo');
    expect(list.single.cpu, 12.5);
    expect(refreshes, 1);
    expect(store.value?.refreshToken, 'rt_refresh2');
  });

  test('快到期时先刷新；并发请求只刷新一次（避免旧 Refresh Token 被重复使用）', () async {
    var refreshes = 0;
    final client = MockClient((req) async {
      if (req.url.path == '/api/v1/app/token/refresh') {
        refreshes++;
        await Future<void>.delayed(const Duration(milliseconds: 20));
        return ok(tokens(2));
      }
      return ok({'items': [], 'next_cursor': ''});
    });
    final s = Session.fromTokens(server, {...tokens(1), 'expires_in': 30}, DateTime.now());
    final api = ApiClient(s, MemorySessionStore(), client: client);
    await Future.wait([api.servers(), api.servers(), api.servers()]);
    expect(refreshes, 1);
  });

  test('设备被吊销：清除本地凭证并抛出 DeviceRevoked（设计 12.7）', () async {
    final store = MemorySessionStore();
    final client = MockClient((_) async => err(401, 'token_revoked'));
    final s = Session.fromTokens(server, tokens(1), DateTime.now());
    await store.save(s);
    final api = ApiClient(s, store, client: client);
    await expectLater(api.servers(), throwsA(isA<DeviceRevoked>()));
    expect(store.value, isNull);
  });

  test('刷新被拒（Refresh Token 失效）：同样回到配对页', () async {
    final store = MemorySessionStore();
    final client = MockClient((req) async =>
        req.url.path == '/api/v1/app/token/refresh' ? err(401, 'token_revoked') : err(401, 'token_expired'));
    final s = Session.fromTokens(server, tokens(1), DateTime.now());
    await store.save(s);
    await expectLater(ApiClient(s, store, client: client).servers(), throwsA(isA<DeviceRevoked>()));
    expect(store.value, isNull);
  });

  test('会话 JSON 往返', () {
    final s = Session.fromTokens(server, tokens(3), DateTime.fromMillisecondsSinceEpoch(1000000));
    final back = Session.fromJson(jsonDecode(jsonEncode(s.toJson())) as Map<String, dynamic>);
    expect(back.server, server);
    expect(back.refreshToken, 'rt_refresh3');
    expect(back.accessExpiresAt, s.accessExpiresAt);
  });
}
