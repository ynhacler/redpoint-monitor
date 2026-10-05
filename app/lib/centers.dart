// 多监控中心（设计 1.5.2、12.8）：App 同时连接多套自建面板，凭证与推送密钥相互隔离，可添加与切换。
//
// 每个中心保存自己的 server_url、device_id、access_token、refresh_token、push_private_key（Session），
// 全部在系统安全存储的一个键中（centers_v1）；离线缓存与界面偏好按中心分文件。
// 旧版本的单个会话（session_v1）在首次读取时迁移为第一个中心。
import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'api.dart';
import 'metrics.dart';
import 'models.dart';
import 'session.dart';

/// 中心的稳定标识：面板主机 + 设备编号（同一面板重新配对得到新设备，视为新的中心项）
String centerKey(Session s) => '${s.server.host}_${s.server.port}_${s.deviceId}'.replaceAll(RegExp(r'[^A-Za-z0-9._-]'), '_');

class Centers {
  Centers({List<Session>? sessions, this.active = 0}) : sessions = sessions ?? [];
  final List<Session> sessions;
  int active;

  Session? get current => sessions.isEmpty ? null : sessions[active.clamp(0, sessions.length - 1)];

  /// 添加或替换（同一面板同一设备）并设为当前，返回下标。
  int upsert(Session s) {
    final i = sessions.indexWhere((x) => centerKey(x) == centerKey(s));
    if (i >= 0) {
      sessions[i] = s;
      active = i;
    } else {
      sessions.add(s);
      active = sessions.length - 1;
    }
    return active;
  }

  /// 删除一个中心；当前中心被删除时切到第一个。
  void remove(String key) {
    final i = sessions.indexWhere((x) => centerKey(x) == key);
    if (i < 0) return;
    sessions.removeAt(i);
    if (active >= sessions.length || active == i) active = 0;
    if (active > i) active--;
  }

  Map<String, dynamic> toJson() => {'active': active, 'sessions': [for (final s in sessions) s.toJson()]};

  factory Centers.fromJson(Map<String, dynamic> j) => Centers(
        sessions: [for (final s in (j['sessions'] as List<dynamic>)) Session.fromJson(s as Map<String, dynamic>)],
        active: (j['active'] as num?)?.toInt() ?? 0,
      );
}

abstract class CentersStore {
  Future<Centers> load();
  Future<void> save(Centers c);
}

class SecureCentersStore implements CentersStore {
  const SecureCentersStore();
  static const _storage = FlutterSecureStorage();
  static const _key = 'centers_v1';

  @override
  Future<Centers> load() async {
    final raw = await _storage.read(key: _key);
    if (raw != null) {
      try {
        return Centers.fromJson(jsonDecode(raw) as Map<String, dynamic>);
      } catch (_) {
        return Centers(); // 格式不对：当作没有中心
      }
    }
    // 迁移：旧版本只有一个会话
    final old = await const SecureSessionStore().load();
    final c = Centers(sessions: [if (old != null) old]);
    if (old != null) {
      await save(c);
      await const SecureSessionStore().clear();
    }
    return c;
  }

  @override
  Future<void> save(Centers c) => _storage.write(key: _key, value: jsonEncode(c.toJson()));
}

class MemoryCentersStore implements CentersStore {
  Centers value = Centers();
  @override
  Future<Centers> load() async => value;
  @override
  Future<void> save(Centers c) async => value = c;
}

/// 把一个中心适配为 ApiClient 使用的 SessionStore：刷新凭证时更新列表中的这一项，吊销时删除这一项。
class CenterSessionStore implements SessionStore {
  CenterSessionStore(this._store, this._centers, this._key, {this.onChanged});
  final CentersStore _store;
  final Centers _centers;
  final String _key;

  /// 会话变化后（刷新凭证、生成推送密钥、删除）通知外层，例如把推送私钥同步给 iOS 扩展
  final void Function()? onChanged;

  @override
  Future<Session?> load() async => _centers.sessions.where((s) => centerKey(s) == _key).firstOrNull;

  @override
  Future<void> save(Session s) async {
    final i = _centers.sessions.indexWhere((x) => centerKey(x) == _key);
    if (i >= 0) {
      _centers.sessions[i] = s;
      await _store.save(_centers);
      onChanged?.call();
    }
  }

  @override
  Future<void> clear() async {
    _centers.remove(_key);
    await _store.save(_centers);
    onChanged?.call();
  }
}

/// 一个已连接的监控中心：界面用来显示、切换与聚合（设计 1.5.2）。
class CenterHandle {
  CenterHandle(this.key, this.server, this.api);
  final String key;
  final Uri server;
  final ApiClient api;
}

/// 一个中心的汇总（聚合视图，设计 1.5.2）。error 非空表示这次没能取到，显示上次的数字。
class CenterSummary {
  const CenterSummary({required this.total, required this.online, required this.offline, required this.attention, this.error});
  final int total;
  final int online;
  final int offline;
  final int attention;
  final String? error;

  /// 与首页同一口径：需要关注按 issues（与 Web 的“需要关注”一致，设计 9）
  factory CenterSummary.of(List<ServerView> items) => CenterSummary(
        total: items.length,
        online: items.where((s) => s.status == 'online').length,
        offline: items.where((s) => s.status == 'offline').length,
        attention: items.where((s) => issues(s).isNotEmpty).length,
      );

  CenterSummary withError(String e) => CenterSummary(total: total, online: online, offline: offline, attention: attention, error: e);

  static CenterSummary sum(Iterable<CenterSummary> xs) => CenterSummary(
        total: xs.fold(0, (a, x) => a + x.total),
        online: xs.fold(0, (a, x) => a + x.online),
        offline: xs.fold(0, (a, x) => a + x.offline),
        attention: xs.fold(0, (a, x) => a + x.attention),
      );
}
