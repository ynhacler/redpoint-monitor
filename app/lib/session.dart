// 设备会话（设计 12.5、12.6）：面板地址与设备凭证。
//
// 【安全】凭证只保存在系统安全存储（iOS Keychain / Android Keystore，flutter_secure_storage）中，
// 不写入 SharedPreferences、普通文件或日志（设计 12.6）。
import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

class Session {
  Session({
    required this.server,
    required this.deviceId,
    required this.accessToken,
    required this.refreshToken,
    required this.accessExpiresAt,
    required this.scopeType,
    required this.scopeValue,
    required this.allowLowRiskOps,
  });

  final Uri server;
  final int deviceId;
  final String accessToken;
  final String refreshToken;

  /// Access Token 过期时间（本机时钟）
  final DateTime accessExpiresAt;
  final String scopeType;
  final String scopeValue;
  final bool allowLowRiskOps;

  /// 由配对或刷新的响应（AppTokens）生成。
  factory Session.fromTokens(Uri server, Map<String, dynamic> j, DateTime now) {
    final scope = j['scope'] as Map<String, dynamic>;
    return Session(
      server: server,
      deviceId: j['device_id'] as int,
      accessToken: j['access_token'] as String,
      refreshToken: j['refresh_token'] as String,
      accessExpiresAt: now.add(Duration(seconds: j['expires_in'] as int)),
      scopeType: scope['type'] as String,
      scopeValue: scope['value'] as String,
      allowLowRiskOps: scope['allow_low_risk_ops'] as bool,
    );
  }

  Map<String, dynamic> toJson() => {
        'server': server.toString(),
        'device_id': deviceId,
        'access_token': accessToken,
        'refresh_token': refreshToken,
        'access_expires_at': accessExpiresAt.millisecondsSinceEpoch,
        'scope_type': scopeType,
        'scope_value': scopeValue,
        'allow_low_risk_ops': allowLowRiskOps,
      };

  factory Session.fromJson(Map<String, dynamic> j) => Session(
        server: Uri.parse(j['server'] as String),
        deviceId: j['device_id'] as int,
        accessToken: j['access_token'] as String,
        refreshToken: j['refresh_token'] as String,
        accessExpiresAt: DateTime.fromMillisecondsSinceEpoch(j['access_expires_at'] as int),
        scopeType: j['scope_type'] as String,
        scopeValue: j['scope_value'] as String,
        allowLowRiskOps: j['allow_low_risk_ops'] as bool,
      );
}

/// 会话的持久化；测试中替换为内存实现。
abstract class SessionStore {
  Future<Session?> load();
  Future<void> save(Session s);
  Future<void> clear();
}

class SecureSessionStore implements SessionStore {
  const SecureSessionStore();

  static const _storage = FlutterSecureStorage();
  static const _key = 'session_v1';

  /// 开发骨架时期的旧键（面板地址 + 开发 token），读取时清除
  static const _legacy = ['server_url', 'token'];

  @override
  Future<Session?> load() async {
    for (final k in _legacy) {
      await _storage.delete(key: k);
    }
    final raw = await _storage.read(key: _key);
    if (raw == null) return null;
    try {
      return Session.fromJson(jsonDecode(raw) as Map<String, dynamic>);
    } catch (_) {
      await clear(); // 格式不对（如旧版本写入）：当作未配对
      return null;
    }
  }

  @override
  Future<void> save(Session s) => _storage.write(key: _key, value: jsonEncode(s.toJson()));

  @override
  Future<void> clear() => _storage.delete(key: _key);
}

class MemorySessionStore implements SessionStore {
  Session? value;

  @override
  Future<Session?> load() async => value;

  @override
  Future<void> save(Session s) async => value = s;

  @override
  Future<void> clear() async => value = null;
}
