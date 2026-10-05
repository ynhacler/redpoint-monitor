// 离线缓存（设计 1.5.10）：最近一次的节点列表（含最后指标与活动告警）与历史曲线。
// 网络不可用时显示缓存与“最后更新时间”，而不是整页不可用。
//
// 【安全】缓存只含监控数据，不含任何凭证（凭证只在安全存储中，设计 12.6）；保存在 App 私有目录，
// 解除配对或设备授权失效时清空。
import 'dart:convert';
import 'dart:io';

import 'package:path_provider/path_provider.dart';

import 'models.dart';

/// 缓存的数据与获取时间。
class Cached<T> {
  Cached(this.value, this.at);
  final T value;
  final DateTime at;
}

abstract class CacheStore {
  Future<Cached<List<ServerView>>?> servers();
  Future<void> saveServers(List<ServerView> list, DateTime at);
  Future<Cached<List<MetricPoint>>?> history(int serverId, String range);
  Future<void> saveHistory(int serverId, String range, List<dynamic> raw, DateTime at);
  Future<void> clear();
}

/// 历史曲线最多缓存的条数（节点 × 范围），超出时丢弃最早的
const _maxHistory = 30;

/// 以一个 JSON 文件保存在 App 支持目录（Application Support / files）中；多监控中心时每个中心一个文件（name）。
class FileCacheStore implements CacheStore {
  FileCacheStore({Future<Directory> Function()? dir, this.name = 'cache_v1'}) : _dir = dir ?? getApplicationSupportDirectory;

  final Future<Directory> Function() _dir;
  final String name;
  Map<String, dynamic>? _data;

  Future<File> _file() async => File('${(await _dir()).path}/$name.json');

  Future<Map<String, dynamic>> _load() async {
    if (_data != null) return _data!;
    try {
      _data = jsonDecode(await (await _file()).readAsString()) as Map<String, dynamic>;
    } catch (_) {
      _data = {}; // 不存在或损坏：当作没有缓存
    }
    return _data!;
  }

  Future<void> _write() async {
    final f = await _file();
    await f.parent.create(recursive: true);
    final tmp = File('${f.path}.tmp');
    await tmp.writeAsString(jsonEncode(_data)); // 先写临时文件再改名，避免写到一半时被读到
    await tmp.rename(f.path);
  }

  @override
  Future<Cached<List<ServerView>>?> servers() async {
    final d = await _load();
    final s = d['servers'] as Map<String, dynamic>?;
    if (s == null) return null;
    try {
      final list = (s['items'] as List<dynamic>).map((e) => ServerView.fromJson(e as Map<String, dynamic>)).toList();
      return Cached(list, DateTime.fromMillisecondsSinceEpoch(s['at'] as int));
    } catch (_) {
      return null; // 旧版本写入的格式不兼容：忽略
    }
  }

  @override
  Future<void> saveServers(List<ServerView> list, DateTime at) async {
    final d = await _load();
    d['servers'] = {'at': at.millisecondsSinceEpoch, 'items': list.map((s) => s.raw).toList()};
    await _write();
  }

  @override
  Future<Cached<List<MetricPoint>>?> history(int serverId, String range) async {
    final h = ((await _load())['history'] as Map<String, dynamic>?)?['$serverId:$range'] as Map<String, dynamic>?;
    if (h == null) return null;
    try {
      final items = (h['items'] as List<dynamic>).map((e) => MetricPoint.fromJson(e as Map<String, dynamic>)).toList();
      return Cached(items, DateTime.fromMillisecondsSinceEpoch(h['at'] as int));
    } catch (_) {
      return null;
    }
  }

  @override
  Future<void> saveHistory(int serverId, String range, List<dynamic> raw, DateTime at) async {
    final d = await _load();
    final h = (d['history'] as Map<String, dynamic>?) ?? <String, dynamic>{};
    h.remove('$serverId:$range');
    h['$serverId:$range'] = {'at': at.millisecondsSinceEpoch, 'items': raw}; // 重新插入：按最近使用排序
    while (h.length > _maxHistory) {
      h.remove(h.keys.first);
    }
    d['history'] = h;
    await _write();
  }

  @override
  Future<void> clear() async {
    _data = {};
    try {
      await (await _file()).delete();
    } catch (_) {
      // 不存在：无需处理
    }
  }
}
