// VPS Monitor App（设计 12）：AK 配对 → 设备凭证 → 只读查看授权范围内的节点。
//
// 启动（设计 12.2）：有已保存的设备会话 → 节点列表；没有或已失效 → 添加监控平台。
// TODO(C): 端到端加密推送（设计 30）。
//
// 本地开发：面板以 make dev 启动；iOS 模拟器填 http://127.0.0.1:8080，Android 模拟器填 http://10.0.2.2:8080
// （调试构建才允许这两个明文地址）；真机使用 https 面板地址。
import 'package:flutter/material.dart';

import 'api.dart';
import 'cache.dart';
import 'pages/pair_page.dart';
import 'pages/servers_page.dart';
import 'session.dart';
import 'theme.dart';

void main() => runApp(VpsMonApp(store: const SecureSessionStore(), cache: FileCacheStore()));

class VpsMonApp extends StatelessWidget {
  const VpsMonApp({super.key, required this.store, required this.cache});
  final SessionStore store;
  final CacheStore cache;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'VPS Monitor',
      theme: appTheme(Brightness.light),
      darkTheme: appTheme(Brightness.dark),
      home: Root(store: store, cache: cache),
    );
  }
}

class Root extends StatefulWidget {
  const Root({super.key, required this.store, required this.cache});
  final SessionStore store;
  final CacheStore cache;

  @override
  State<Root> createState() => _RootState();
}

class _RootState extends State<Root> {
  ApiClient? _api;
  bool _loading = true;
  String? _notice;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final s = await widget.store.load();
    setState(() {
      _api = s == null ? null : ApiClient(s, widget.store);
      _loading = false;
    });
  }

  Future<void> _paired(Session s) async {
    await widget.cache.clear(); // 换了面板或重新配对：不显示上一次的缓存
    await widget.store.save(s);
    setState(() {
      _api = ApiClient(s, widget.store);
      _notice = null;
    });
  }

  Future<void> _unpair() async {
    await _api?.unpair();
    await widget.cache.clear();
    _api?.close();
    setState(() => _api = null);
  }

  // 设计 12.7：清除凭证（ApiClient 已清除）、回到配对页并说明原因
  void _revoked() {
    widget.cache.clear();
    _api?.close();
    setState(() {
      _api = null;
      _notice = DeviceRevoked().toString();
    });
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Scaffold(body: Center(child: CircularProgressIndicator()));
    final api = _api;
    if (api == null) return PairPage(onPaired: _paired, notice: _notice);
    return ServersPage(api: api, cache: widget.cache, onUnpair: _unpair, onRevoked: _revoked);
  }
}
