// 面板接口客户端（设计 12.3～12.7、19.3）：配对、自动刷新设备凭证、读取节点。
//
// 凭证生命周期：
//   - Access Token 30 分钟：到期前 60 秒内或收到 401 token_expired 时，用 Refresh Token 换新后重试一次
//   - 刷新同一时间只进行一次（并发请求共用同一次刷新），避免旧 Refresh Token 被重复使用而触发吊销
//   - 401 token_revoked / unauthorized：设备已被吊销或授权失效，清除本地凭证并回到配对页（设计 12.7）
import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

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
Future<Session> pairDevice(Uri server, String accessKey, {required String name, required String platform, http.Client? client}) async {
  final c = client ?? http.Client();
  try {
    final res = await c
        .post(server.replace(path: '${server.path}/api/v1/app/pair'),
            headers: {'Content-Type': 'application/json'},
            body: jsonEncode({
              'access_key': accessKey,
              'device': {'name': name, 'platform': platform, 'app_version': appVersion},
            }))
        .timeout(_timeout);
    if (res.statusCode != 200) throw _errorOf(res);
    return Session.fromTokens(server, _json(res) as Map<String, dynamic>, DateTime.now());
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
    _session = Session.fromTokens(_session.server, _json(res) as Map<String, dynamic>, _now());
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

  /// 节点列表（授权范围内），异常优先由调用方排序。
  Future<List<ServerItem>> servers() async {
    final body = await getJson('/servers') as Map<String, dynamic>;
    return (body['items'] as List<dynamic>).map((e) => ServerItem.fromJson(e as Map<String, dynamic>)).toList();
  }

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

class ServerItem {
  ServerItem({required this.id, required this.name, required this.status, this.cpu, this.mem, this.rx = 0, this.tx = 0,
      required this.trafficUsed, required this.trafficLimit});
  final int id;
  final String name;
  final String status;
  final double? cpu;
  final double? mem;
  final num rx;
  final num tx;
  final num trafficUsed;
  final num trafficLimit;

  factory ServerItem.fromJson(Map<String, dynamic> j) {
    final latest = j['latest'] as Map<String, dynamic>?;
    final nets = (latest?['network'] as List<dynamic>?) ?? const [];
    final traffic = j['traffic'] as Map<String, dynamic>;
    return ServerItem(
      id: j['id'] as int,
      name: j['name'] as String,
      status: j['status'] as String,
      cpu: (latest?['cpu']?['usage'] as num?)?.toDouble(),
      mem: (latest?['memory']?['usage'] as num?)?.toDouble(),
      rx: nets.fold<num>(0, (a, n) => a + ((n as Map<String, dynamic>)['rx_speed'] as num)),
      tx: nets.fold<num>(0, (a, n) => a + ((n as Map<String, dynamic>)['tx_speed'] as num)),
      trafficUsed: traffic['used'] as num,
      trafficLimit: traffic['limit'] as num,
    );
  }

  /// 异常优先（设计 1.5.3）：离线 → 未知 → 在线 → 待安装
  int get rank => switch (status) { 'offline' => 0, 'unknown' => 1, 'online' => 2, _ => 3 };
}

String fmtBytes(num n, {bool perSec = false}) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var v = n.toDouble();
  var i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return '${v.toStringAsFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}${perSec ? '/s' : ''}';
}
