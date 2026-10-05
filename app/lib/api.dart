// 面板接口客户端（设计 12.3～12.7、19.3）：配对、自动刷新设备凭证、读取节点。
//
// 凭证生命周期：
//   - Access Token 30 分钟：到期前 60 秒内或收到 401 token_expired 时，用 Refresh Token 换新后重试一次
//   - 刷新同一时间只进行一次（并发请求共用同一次刷新），避免旧 Refresh Token 被重复使用而触发吊销
//   - 401 token_revoked / unauthorized：设备已被吊销或授权失效，清除本地凭证并回到配对页（设计 12.7）
import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'models.dart';
import 'push_crypto.dart';
import 'session.dart';
import 'version.dart';

/// 面板返回的错误（设计 43.4）；message 是中文，可直接展示。
class ApiException implements Exception {
  ApiException(this.status, this.code, this.message);
  final int status;
  final String code;
  final String message;

  @override
  String toString() => message;
}

/// 设备授权已失效：需要重新配对。
class DeviceRevoked implements Exception {
  @override
  String toString() => '当前设备授权已失效，请重新通过 Web 管理端生成 AK 配对。';
}

const _timeout = Duration(seconds: 10);

ApiException _errorOf(http.Response res) {
  try {
    final e = (jsonDecode(utf8.decode(res.bodyBytes)) as Map<String, dynamic>)['error'] as Map<String, dynamic>;
    return ApiException(res.statusCode, e['code'] as String, e['message'] as String);
  } catch (_) {
    return ApiException(res.statusCode, 'http_${res.statusCode}', '面板返回了错误（HTTP ${res.statusCode}）');
  }
}

dynamic _json(http.Response res) => jsonDecode(utf8.decode(res.bodyBytes));

/// 用 AK 配对（POST /app/pair），返回新会话。AK 无效时抛出 [ApiException]（access_key_invalid）。
/// 同时生成推送解密用的 X25519 密钥对：公钥随配对提交，私钥只保存在本机（设计 12.3、30.3.1）。
Future<Session> pairDevice(Uri server, String accessKey, {required String name, required String platform, http.Client? client}) async {
  final c = client ?? http.Client();
  final (priv, pub) = await generatePushKeyPair();
  try {
    final res = await c
        .post(server.replace(path: '${server.path}/api/v1/app/pair'),
            headers: {'Content-Type': 'application/json'},
            body: jsonEncode({
              'access_key': accessKey,
              'device': {'name': name, 'platform': platform, 'app_version': appVersion},
              'push_public_key': base64.encode(pub),
            }))
        .timeout(_timeout);
    if (res.statusCode != 200) throw _errorOf(res);
    return Session.fromTokens(server, _json(res) as Map<String, dynamic>, DateTime.now(), pushPrivateKey: base64.encode(priv));
  } finally {
    if (client == null) c.close();
  }
}

class ApiClient {
  ApiClient(this._session, this._store, {http.Client? client, DateTime Function()? now})
      : _http = client ?? http.Client(),
        _now = now ?? DateTime.now;

  Session _session;
  final SessionStore _store;
  final http.Client _http;
  final DateTime Function() _now;
  Future<void>? _refreshing;

  Session get session => _session;

  Uri _url(String path, [Map<String, String>? query]) =>
      _session.server.replace(path: '${_session.server.path}/api/v1$path', queryParameters: query);

  /// 刷新凭证（单飞）：并发调用共用同一次请求。
  Future<void> refresh() => _refreshing ??= _doRefresh().whenComplete(() => _refreshing = null);

  Future<void> _doRefresh() async {
    final res = await _http
        .post(_url('/app/token/refresh'),
            headers: {'Content-Type': 'application/json'},
            body: jsonEncode({'refresh_token': _session.refreshToken, 'app_version': appVersion}))
        .timeout(_timeout);
    if (res.statusCode == 401) {
      await _revoked();
    }
    if (res.statusCode != 200) throw _errorOf(res);
    _session = Session.fromTokens(_session.server, _json(res) as Map<String, dynamic>, _now(), pushPrivateKey: _session.pushPrivateKey);
    await _store.save(_session);
  }

  Future<Never> _revoked() async {
    await _store.clear();
    throw DeviceRevoked();
  }

  /// 发送带设备凭证的请求：快到期时先刷新；401 token_expired 时刷新后重试一次。
  Future<http.Response> _send(String method, String path, {Map<String, String>? query, Object? body}) async {
    if (_session.accessExpiresAt.difference(_now()) < const Duration(seconds: 60)) {
      await refresh();
    }
    for (var attempt = 0;; attempt++) {
      final req = http.Request(method, _url(path, query))..headers['Authorization'] = 'Bearer ${_session.accessToken}';
      if (body != null) {
        req.headers['Content-Type'] = 'application/json';
        req.body = jsonEncode(body);
      }
      final res = await http.Response.fromStream(await _http.send(req).timeout(_timeout));
      if (res.statusCode != 401) return res;
      final err = _errorOf(res);
      if (err.code == 'token_expired' && attempt == 0) {
        await refresh();
        continue;
      }
      await _revoked();
    }
  }

  Future<dynamic> getJson(String path, {Map<String, String>? query}) async {
    final res = await _send('GET', path, query: query);
    if (res.statusCode != 200) throw _errorOf(res);
    return _json(res);
  }

  Future<Map<String, dynamic>> _send2xx(String method, String path, {Object? body}) async {
    final res = await _send(method, path, body: body);
    if (res.statusCode >= 300) throw _errorOf(res);
    return res.body.isEmpty ? const {} : _json(res) as Map<String, dynamic>;
  }

  /// 节点列表（授权范围内），排序由调用方按异常优先处理。
  Future<List<ServerView>> servers() async {
    final body = await getJson('/servers') as Map<String, dynamic>;
    return (body['items'] as List<dynamic>).map((e) => ServerView.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// 单个节点；不在授权范围内时面板返回 404。
  Future<ServerView> server(int id) async => ServerView.fromJson(await getJson('/servers/$id') as Map<String, dynamic>);

  /// 当前设备与面板的推送能力（GET /app/me）。
  Future<Map<String, dynamic>> me() async => await getJson('/app/me') as Map<String, dynamic>;

  /// 登记推送 Token（PUT /app/push）；本机还没有推送密钥时（旧版本配对）生成并一并提交公钥。
  Future<void> registerPush(String provider, String token) async {
    String? pub;
    if (_session.pushPrivateKey.isEmpty) {
      final (priv, p) = await generatePushKeyPair();
      _session = _session.copyWith(pushPrivateKey: base64.encode(priv));
      await _store.save(_session);
      pub = base64.encode(p);
    }
    await _send2xx('PUT', '/app/push', body: {'provider': provider, 'token': token, if (pub != null) 'public_key': pub});
  }

  /// 关闭本机推送（DELETE /app/push）。
  Future<void> disablePush() => _send2xx('DELETE', '/app/push');

  /// 告警事件（事件中心，设计 1.5.17）：按时间倒序，cursor 翻页；面板按设备的授权范围过滤。
  Future<(List<AlertEvent>, String)> alerts({String state = 'all', String cursor = '', int limit = 50}) async {
    final body = await getJson('/alerts', query: {'state': state, 'limit': '$limit', if (cursor.isNotEmpty) 'cursor': cursor})
        as Map<String, dynamic>;
    final items = (body['items'] as List<dynamic>).map((e) => AlertEvent.fromJson(e as Map<String, dynamic>)).toList();
    return (items, (body['next_cursor'] as String?) ?? '');
  }

  /// 健康摘要（设计 1.5.7）。
  Future<HealthSummary> health(int id) async => HealthSummary.fromJson(await getJson('/servers/$id/health') as Map<String, dynamic>);

  /// 历史曲线：range 为 1h / 6h / 24h / 7d / 30d（设计 19.7）。返回原始 JSON 列表，便于离线缓存。
  Future<List<dynamic>> historyRaw(int id, String range) async {
    final body = await getJson('/servers/$id/metrics/history', query: {'range': range}) as Map<String, dynamic>;
    return body['items'] as List<dynamic>;
  }

  /// 静音（mute）或维护（maintenance）单个节点；duration 为 1h / 8h / 24h，空表示直到手动结束（设计 8.4.1）。
  Future<void> silence(int serverId, String kind, String duration) =>
      _send2xx('POST', '/silences', body: {'kind': kind, 'scope_type': 'server', 'scope_id': '$serverId', 'duration': duration});

  /// 立即结束静音或维护。
  Future<void> endSilence(int id) => _send2xx('DELETE', '/silences/$id');

  /// 主动解除本机配对（POST /app/unpair）；网络失败时也清除本地凭证（设备可在 Web 中吊销）。
  Future<void> unpair() async {
    try {
      await _send('POST', '/app/unpair');
    } catch (_) {
      // 忽略：本地凭证照样清除
    } finally {
      await _store.clear();
    }
  }

  void close() => _http.close();
}
