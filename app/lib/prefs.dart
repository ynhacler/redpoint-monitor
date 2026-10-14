// 本机偏好（设计 1.5.5、1.5.6、1.5.12）：收藏、排序方式（含自定义顺序）、隐私模式。只是界面偏好，不含凭证，
// 与离线缓存一样保存在 App 支持目录；解除配对时清除（收藏属于某个面板的节点）。
import 'dart:convert';
import 'dart:io';

import 'package:path_provider/path_provider.dart';

import 'sorting.dart';

/// 本机界面偏好：收藏、排序方式与自定义顺序、隐私模式
class Prefs {
  Prefs({Set<int>? favorites, this.sortBy = SortBy.smart, this.privacy = false, List<int>? order})
      : favorites = favorites ?? {},
        order = order ?? [];
  final Set<int> favorites;

  /// 自定义顺序（节点 ID），排序方式为 custom 时使用
  List<int> order;
  SortBy sortBy;
  bool privacy;

  Map<String, dynamic> toJson() => {'favorites': favorites.toList()..sort(), 'sort_by': sortBy.name, 'privacy': privacy, 'order': order};

  factory Prefs.fromJson(Map<String, dynamic> j) => Prefs(
        favorites: {for (final x in (j['favorites'] as List<dynamic>? ?? const [])) (x as num).toInt()},
        sortBy: SortBy.values.where((v) => v.name == j['sort_by']).firstOrNull ?? SortBy.smart,
        privacy: (j['privacy'] as bool?) ?? false,
        order: [for (final x in (j['order'] as List<dynamic>? ?? const [])) (x as num).toInt()],
      );
}

/// 偏好的存储
abstract class PrefsStore {
  Future<Prefs> load();
  Future<void> save(Prefs p);
  Future<void> clear();
}

/// 把偏好保存为 App 支持目录中的 JSON 文件（每个监控中心一个）
class FilePrefsStore implements PrefsStore {
  FilePrefsStore({Future<Directory> Function()? dir, this.name = 'prefs_v1'}) : _dir = dir ?? getApplicationSupportDirectory;
  final Future<Directory> Function() _dir;

  /// 文件名（不含扩展名）；多监控中心时每个中心一个
  final String name;

  Future<File> _file() async => File('${(await _dir()).path}/$name.json');

  @override
  Future<Prefs> load() async {
    try {
      return Prefs.fromJson(jsonDecode(await (await _file()).readAsString()) as Map<String, dynamic>);
    } catch (_) {
      return Prefs();
    }
  }

  @override
  Future<void> save(Prefs p) async {
    final f = await _file();
    await f.parent.create(recursive: true);
    await f.writeAsString(jsonEncode(p.toJson()));
  }

  @override
  Future<void> clear() async {
    try {
      await (await _file()).delete();
    } catch (_) {}
  }
}

/// 内存中的偏好（测试用）
class MemoryPrefsStore implements PrefsStore {
  Prefs value = Prefs();
  @override
  Future<Prefs> load() async => value;
  @override
  Future<void> save(Prefs p) async => value = p;
  @override
  Future<void> clear() async => value = Prefs();
}
