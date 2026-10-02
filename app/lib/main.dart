// VPS Monitor app — development skeleton.
//
// Current state: connects with server URL + dev admin token (read-only use).
// TODO(M4): replace with AK QR pairing → Device Token + Refresh Token (design 12.3).
// TODO(M5): E2E encrypted push (design ch. 30).
//
// Dev server addresses:
//   iOS simulator     http://127.0.0.1:8080
//   Android emulator  http://10.0.2.2:8080
//   Real phone        use the VPS over HTTPS, or your Mac's LAN IP with --listen 0.0.0.0:8080
import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:http/http.dart' as http;

void main() => runApp(const VpsMonApp());

// Credentials live only in Keychain / Keystore (design 1.6.1).
const _storage = FlutterSecureStorage();
const _kUrl = 'server_url';
const _kToken = 'token';

class VpsMonApp extends StatelessWidget {
  const VpsMonApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'VPS Monitor',
      theme: ThemeData(colorSchemeSeed: Colors.blue, useMaterial3: true),
      darkTheme: ThemeData(colorSchemeSeed: Colors.blue, brightness: Brightness.dark, useMaterial3: true),
      home: const Root(),
    );
  }
}

class Root extends StatefulWidget {
  const Root({super.key});
  @override
  State<Root> createState() => _RootState();
}

class _RootState extends State<Root> {
  String? _url;
  String? _token;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final url = await _storage.read(key: _kUrl);
    final token = await _storage.read(key: _kToken);
    setState(() {
      _url = url;
      _token = token;
      _loading = false;
    });
  }

  Future<void> _connect(String url, String token) async {
    await _storage.write(key: _kUrl, value: url);
    await _storage.write(key: _kToken, value: token);
    setState(() {
      _url = url;
      _token = token;
    });
  }

  Future<void> _disconnect() async {
    await _storage.deleteAll();
    setState(() {
      _url = null;
      _token = null;
    });
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Scaffold(body: Center(child: CircularProgressIndicator()));
    if (_url == null || _token == null) return ConnectPage(onConnect: _connect);
    return ServersPage(api: Api(_url!, _token!), onDisconnect: _disconnect);
  }
}

class ConnectPage extends StatefulWidget {
  const ConnectPage({super.key, required this.onConnect});
  final Future<void> Function(String url, String token) onConnect;
  @override
  State<ConnectPage> createState() => _ConnectPageState();
}

class _ConnectPageState extends State<ConnectPage> {
  final _url = TextEditingController(text: 'http://127.0.0.1:8080');
  final _token = TextEditingController();
  String? _error;

  Future<void> _submit() async {
    final url = _url.text.trim().replaceAll(RegExp(r'/+$'), '');
    final uri = Uri.tryParse(url);
    final isLocal = uri != null && ['127.0.0.1', 'localhost', '10.0.2.2'].contains(uri.host);
    // Release policy: HTTPS only. Plain HTTP is accepted for local dev addresses only.
    if (uri == null || !(uri.scheme == 'https' || (uri.scheme == 'http' && isLocal))) {
      setState(() => _error = '请使用 https:// 地址（本地开发地址除外）');
      return;
    }
    try {
      await Api(url, _token.text.trim()).servers();
      await widget.onConnect(url, _token.text.trim());
    } catch (e) {
      setState(() => _error = '连接失败：$e');
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('连接监控中心')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          TextField(controller: _url, decoration: const InputDecoration(labelText: '服务地址'), keyboardType: TextInputType.url),
          const SizedBox(height: 12),
          TextField(controller: _token, decoration: const InputDecoration(labelText: 'Token（开发阶段）'), obscureText: true),
          const SizedBox(height: 20),
          FilledButton(onPressed: _submit, child: const Text('连接')),
          if (_error != null) Padding(padding: const EdgeInsets.only(top: 12), child: Text(_error!, style: const TextStyle(color: Colors.red))),
          const SizedBox(height: 24),
          const Text('后续版本改为扫描 Web 生成的二维码配对，无需填写 Token。', style: TextStyle(color: Colors.grey)),
        ],
      ),
    );
  }
}

class Api {
  Api(this.baseUrl, this.token);
  final String baseUrl;
  final String token;

  Future<List<ServerItem>> servers() async {
    final res = await http
        .get(Uri.parse('$baseUrl/api/v1/servers'), headers: {'Authorization': 'Bearer $token'})
        .timeout(const Duration(seconds: 8));
    if (res.statusCode == 401) throw Exception('Token 无效');
    if (res.statusCode != 200) throw Exception('HTTP ${res.statusCode}');
    final list = jsonDecode(res.body) as List<dynamic>;
    return list.map((e) => ServerItem.fromJson(e as Map<String, dynamic>)).toList();
  }
}

class ServerItem {
  ServerItem({required this.name, required this.status, this.cpu, this.mem, this.rx = 0, this.tx = 0, required this.trafficUsed, required this.trafficLimit});
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

  int get rank => status == 'offline' ? 0 : (status == 'unknown' ? 1 : 2);
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

class ServersPage extends StatefulWidget {
  const ServersPage({super.key, required this.api, required this.onDisconnect});
  final Api api;
  final VoidCallback onDisconnect;
  @override
  State<ServersPage> createState() => _ServersPageState();
}

class _ServersPageState extends State<ServersPage> {
  List<ServerItem> _items = [];
  String? _error;
  DateTime? _updated;
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _refresh();
    // Normal mode: list refresh every 10s (design 1.5.9).
    _timer = Timer.periodic(const Duration(seconds: 10), (_) => _refresh());
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    try {
      final items = await widget.api.servers();
      items.sort((a, b) => a.rank != b.rank ? a.rank - b.rank : a.name.compareTo(b.name));
      if (!mounted) return;
      setState(() {
        _items = items;
        _error = null;
        _updated = DateTime.now();
      });
    } catch (e) {
      if (!mounted) return;
      // Offline cache (design 1.5.10): keep showing the last data.
      setState(() => _error = '$e');
    }
  }

  @override
  Widget build(BuildContext context) {
    final online = _items.where((s) => s.status == 'online').length;
    final abnormal = _items.length - online;
    return Scaffold(
      appBar: AppBar(
        title: const Text('服务器'),
        actions: [IconButton(icon: const Icon(Icons.logout), tooltip: '断开', onPressed: widget.onDisconnect)],
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: ListView(
          padding: const EdgeInsets.all(12),
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(4, 0, 4, 12),
              child: Text(
                '共 ${_items.length} 台 · 在线 $online · 异常 $abnormal'
                '${_updated != null ? ' · ${TimeOfDay.fromDateTime(_updated!).format(context)}' : ''}',
              ),
            ),
            if (_error != null)
              Card(color: Colors.red.shade100, child: Padding(padding: const EdgeInsets.all(12), child: Text('当前离线，显示最近数据：$_error'))),
            ..._items.map(_card),
          ],
        ),
      ),
    );
  }

  Widget _card(ServerItem s) {
    final color = switch (s.status) { 'online' => Colors.green, 'unknown' => Colors.orange, _ => Colors.red };
    final pct = s.trafficLimit > 0 ? (s.trafficUsed / s.trafficLimit).clamp(0.0, 1.0).toDouble() : null;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            Icon(Icons.circle, size: 10, color: color),
            const SizedBox(width: 8),
            Text(s.name, style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16)),
          ]),
          const SizedBox(height: 8),
          if (s.status != 'offline' && s.cpu != null)
            Text('CPU ${s.cpu!.toStringAsFixed(0)}%   内存 ${s.mem!.toStringAsFixed(0)}%   ↓${fmtBytes(s.rx, perSec: true)} ↑${fmtBytes(s.tx, perSec: true)}')
          else
            const Text('离线', style: TextStyle(color: Colors.red)),
          const SizedBox(height: 8),
          Text('本周期 ${fmtBytes(s.trafficUsed)}${s.trafficLimit > 0 ? ' / ${fmtBytes(s.trafficLimit)}' : ' · 不限'}',
              style: const TextStyle(fontSize: 12, color: Colors.grey)),
          if (pct != null) Padding(padding: const EdgeInsets.only(top: 4), child: LinearProgressIndicator(value: pct)),
        ]),
      ),
    );
  }
}
