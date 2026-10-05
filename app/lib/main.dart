// VPS Monitor App（设计 12）：AK 配对 → 设备凭证 → 只读查看授权范围内的节点。
//
// 启动（设计 12.2）：有已保存的监控中心 → 当前中心的首页；没有 → 添加监控平台。
// 多监控中心（设计 1.5.2、12.8）：每个中心独立的凭证、推送密钥、离线缓存与偏好；“我的”中添加与切换。
// TODO(C): 系统推送的接入（firebase_messaging、iOS NSE，设计 30）。
//
// 本地开发：面板以 make dev 启动；iOS 模拟器填 http://127.0.0.1:8080，Android 模拟器填 http://10.0.2.2:8080
// （调试构建才允许这两个明文地址）；真机使用 https 面板地址。
import 'package:flutter/material.dart';

import 'api.dart';
import 'cache.dart';
import 'centers.dart';
import 'pages/pair_page.dart';
import 'pages/shell.dart';
import 'prefs.dart';
import 'session.dart';
import 'theme.dart';

void main() => runApp(const VpsMonApp(store: SecureCentersStore()));

/// 每个中心的离线缓存与偏好文件；测试中替换为内存实现
typedef CenterFiles = (CacheStore, PrefsStore) Function(String key);

(CacheStore, PrefsStore) defaultCenterFiles(String key) =>
    (FileCacheStore(name: 'cache_v1_$key'), FilePrefsStore(name: 'prefs_v1_$key'));

class VpsMonApp extends StatelessWidget {
  const VpsMonApp({super.key, required this.store, this.files = defaultCenterFiles});
  final CentersStore store;
  final CenterFiles files;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'VPS Monitor',
      theme: appTheme(Brightness.light),
      darkTheme: appTheme(Brightness.dark),
      home: Root(store: store, files: files),
    );
  }
}

class Root extends StatefulWidget {
  const Root({super.key, required this.store, required this.files});
  final CentersStore store;
  final CenterFiles files;

  @override
  State<Root> createState() => _RootState();
}

class _RootState extends State<Root> {
  Centers _centers = Centers();
  ApiClient? _api;
  CacheStore? _cache;
  PrefsStore? _prefs;
  bool _loading = true;
  String? _notice;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final c = await widget.store.load();
    if (c.sessions.isNotEmpty && widget.files == defaultCenterFiles) {
      // 迁移：旧版本单个中心的缓存与偏好文件不再使用
      await FileCacheStore().clear();
      await FilePrefsStore().clear();
    }
    if (!mounted) return;
    setState(() {
      _centers = c;
      _loading = false;
    });
    _activate();
  }

  /// 切到当前中心：每个中心独立的 ApiClient、缓存与偏好。
  void _activate() {
    _api?.close();
    final s = _centers.current;
    if (s == null) {
      setState(() => _api = null);
      return;
    }
    final key = centerKey(s);
    final (cache, prefs) = widget.files(key);
    setState(() {
      _api = ApiClient(s, CenterSessionStore(widget.store, _centers, key));
      _cache = cache;
      _prefs = prefs;
    });
  }

  Future<void> _paired(Session s) async {
    _centers.upsert(s);
    await widget.store.save(_centers);
    final (cache, prefs) = widget.files(centerKey(s));
    await cache.clear(); // 重新配对：不显示上一次的缓存与收藏
    await prefs.clear();
    _notice = null;
    _activate();
  }

  void _switch(int i) {
    if (i < 0 || i >= _centers.sessions.length) return;
    _centers.active = i;
    widget.store.save(_centers);
    _activate();
  }

  Future<void> _add() async {
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (ctx) => PairPage(onPaired: (s) async {
        await _paired(s);
        if (ctx.mounted) Navigator.of(ctx).pop();
      }),
    ));
  }

  // 解除当前中心：ApiClient 通知面板并删除这一项；还有其他中心时切过去
  Future<void> _unpair() async {
    await _api?.unpair();
    await _cache?.clear();
    await _prefs?.clear();
    _activate();
  }

  // 设计 12.7：当前中心的授权已失效（ApiClient 已删除这一项）：清除它的缓存，切到其他中心或回到配对页并说明原因
  void _revoked() {
    _cache?.clear();
    _prefs?.clear();
    _notice = DeviceRevoked().toString();
    _activate();
    if (_api != null && mounted) {
      ScaffoldMessenger.maybeOf(context)?.showSnackBar(SnackBar(content: Text('一个监控中心的授权已失效，已移除。$_notice')));
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Scaffold(body: Center(child: CircularProgressIndicator()));
    final api = _api;
    if (api == null) return PairPage(onPaired: _paired, notice: _notice);
    return AppShell(
      key: ValueKey(centerKey(api.session)), // 切换中心时重建页面状态
      api: api,
      cache: _cache!,
      prefs: _prefs!,
      onUnpair: _unpair,
      onRevoked: _revoked,
      centers: [for (final s in _centers.sessions) s.server],
      onSwitch: _switch,
      onAdd: _add,
    );
  }
}
