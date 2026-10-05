// 节点列表（设计 13）：授权范围内的节点，异常优先；每 10 秒刷新；离线时保留最近数据（设计 1.5.10）。
import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';

class ServersPage extends StatefulWidget {
  const ServersPage({super.key, required this.api, required this.onUnpair, required this.onRevoked});
  final ApiClient api;

  /// 用户主动解除本机配对
  final Future<void> Function() onUnpair;

  /// 设备授权已失效（被吊销或长期未使用），回到配对页（设计 12.7）
  final VoidCallback onRevoked;

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
    } on DeviceRevoked {
      _timer?.cancel();
      widget.onRevoked();
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e'); // 保留上次的数据（设计 1.5.10）
    }
  }

  Future<void> _confirmUnpair() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('解除配对？'),
        content: Text('本机将退出 ${widget.api.session.server.host}，之后需要在 Web 管理端重新生成 AK 才能再次连接。'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
          FilledButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('解除')),
        ],
      ),
    );
    if (ok == true) {
      _timer?.cancel();
      await widget.onUnpair();
    }
  }

  @override
  Widget build(BuildContext context) {
    final online = _items.where((s) => s.status == 'online').length;
    final abnormal = _items.where((s) => s.status == 'offline' || s.status == 'unknown').length;
    final scheme = Theme.of(context).colorScheme;
    return Scaffold(
      appBar: AppBar(
        title: const Text('服务器'),
        actions: [IconButton(icon: const Icon(Icons.link_off), tooltip: '解除配对', onPressed: _confirmUnpair)],
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: ListView(padding: const EdgeInsets.all(12), children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 0, 4, 12),
            child: Text(
              '${widget.api.session.server.host} · 共 ${_items.length} 台 · 在线 $online · 异常 $abnormal'
              '${_updated != null ? ' · ${TimeOfDay.fromDateTime(_updated!).format(context)}' : ''}',
            ),
          ),
          if (_error != null)
            Card(
              color: scheme.errorContainer,
              child: Padding(padding: const EdgeInsets.all(12), child: Text('当前无法连接面板，显示最近数据：$_error')),
            ),
          ..._items.map(_card),
        ]),
      ),
    );
  }

  Widget _card(ServerItem s) {
    final color = switch (s.status) { 'online' => Colors.green, 'unknown' => Colors.orange, 'offline' => Colors.red, _ => Colors.grey };
    final pct = s.trafficLimit > 0 ? (s.trafficUsed / s.trafficLimit).clamp(0.0, 1.0).toDouble() : null;
    final muted = TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            Icon(Icons.circle, size: 10, color: color),
            const SizedBox(width: 8),
            Expanded(child: Text(s.name, style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16))),
          ]),
          const SizedBox(height: 8),
          if (s.status == 'pending')
            Text('待安装', style: muted)
          else if (s.status != 'offline' && s.cpu != null)
            Text('CPU ${s.cpu!.toStringAsFixed(0)}%   内存 ${(s.mem ?? 0).toStringAsFixed(0)}%   '
                '↓${fmtBytes(s.rx, perSec: true)} ↑${fmtBytes(s.tx, perSec: true)}')
          else
            const Text('离线', style: TextStyle(color: Colors.red)),
          const SizedBox(height: 8),
          Text('本周期 ${fmtBytes(s.trafficUsed)}${s.trafficLimit > 0 ? ' / ${fmtBytes(s.trafficLimit)}' : ' · 不限'}', style: muted),
          if (pct != null) Padding(padding: const EdgeInsets.only(top: 4), child: LinearProgressIndicator(value: pct)),
        ]),
      ),
    );
  }
}
