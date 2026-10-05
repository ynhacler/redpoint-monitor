// 添加监控平台（设计 12.2～12.4）：扫描 Web 生成的二维码，或手工填写面板地址与 AK。
// 扫码后先显示目标面板地址，用户确认后才发送配对请求（防止误扫恶意二维码，设计 12.4）。
import 'dart:io' show Platform;

import 'package:flutter/material.dart';

import '../api.dart';
import '../pair_link.dart';
import '../session.dart';
import 'scan_page.dart';

class PairPage extends StatefulWidget {
  const PairPage({super.key, required this.onPaired, this.notice});

  final Future<void> Function(Session s) onPaired;

  /// 进入本页的原因，如“当前设备授权已失效…”（设计 12.7）
  final String? notice;

  @override
  State<PairPage> createState() => _PairPageState();
}

class _PairPageState extends State<PairPage> {
  final _server = TextEditingController();
  final _ak = TextEditingController();
  final _name = TextEditingController(text: Platform.isIOS ? 'iPhone' : 'Android 手机');
  String? _error;
  bool _busy = false;

  @override
  void dispose() {
    _server.dispose();
    _ak.dispose();
    _name.dispose();
    super.dispose();
  }

  Future<void> _pair(PairTarget t) async {
    final name = _name.text.trim();
    if (name.isEmpty) {
      setState(() => _error = '请填写设备名称');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final s = await pairDevice(t.server, t.accessKey, name: name, platform: Platform.isIOS ? 'ios' : 'android');
      await widget.onPaired(s);
    } on ApiException catch (e) {
      setState(() => _error = e.message);
    } catch (e) {
      setState(() => _error = '无法连接面板：$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _scan() async {
    final raw = await Navigator.of(context).push<String>(MaterialPageRoute(builder: (_) => const ScanPage()));
    if (raw == null || !mounted) return;
    final PairTarget t;
    try {
      t = parsePairLink(raw);
    } on FormatException catch (e) {
      setState(() => _error = e.message);
      return;
    }
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('即将连接'),
        content: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
          SelectableText(t.server.toString(), style: const TextStyle(fontWeight: FontWeight.w600)),
          const SizedBox(height: 12),
          const Text('请确认这是你自己的监控面板地址。', style: TextStyle(fontSize: 13)),
        ]),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
          FilledButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('连接')),
        ],
      ),
    );
    if (ok == true) await _pair(t);
  }

  Future<void> _manual() async {
    try {
      await _pair(PairTarget(parseServer(_server.text), parseAccessKey(_ak.text)));
    } on FormatException catch (e) {
      setState(() => _error = e.message);
    }
  }

  @override
  Widget build(BuildContext context) {
    final muted = TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant, fontSize: 13);
    return Scaffold(
      appBar: AppBar(title: const Text('添加监控平台')),
      body: AbsorbPointer(
        absorbing: _busy,
        child: ListView(padding: const EdgeInsets.all(16), children: [
          if (widget.notice != null)
            Card(
              color: Theme.of(context).colorScheme.errorContainer,
              child: Padding(padding: const EdgeInsets.all(12), child: Text(widget.notice!)),
            ),
          const SizedBox(height: 8),
          FilledButton.icon(
            onPressed: _scan,
            icon: const Icon(Icons.qr_code_scanner),
            label: const Padding(padding: EdgeInsets.symmetric(vertical: 12), child: Text('扫描二维码')),
          ),
          const SizedBox(height: 8),
          Text('在面板 Web 管理端打开“App → 创建配对 AK”，扫描页面上的二维码。', style: muted),
          const Padding(padding: EdgeInsets.symmetric(vertical: 20), child: Row(children: [
            Expanded(child: Divider()),
            Padding(padding: EdgeInsets.symmetric(horizontal: 12), child: Text('或')),
            Expanded(child: Divider()),
          ])),
          TextField(
            controller: _server,
            decoration: const InputDecoration(labelText: '监控服务器地址', hintText: 'https://monitor.example.com'),
            keyboardType: TextInputType.url,
            autocorrect: false,
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _ak,
            decoration: const InputDecoration(labelText: 'AK', hintText: 'MNT-____-____-…'),
            autocorrect: false,
            textCapitalization: TextCapitalization.characters,
          ),
          const SizedBox(height: 12),
          TextField(controller: _name, decoration: const InputDecoration(labelText: '设备名称', helperText: '显示在 Web“已连接设备”中')),
          const SizedBox(height: 20),
          FilledButton(onPressed: _manual, child: Text(_busy ? '连接中…' : '连接监控平台')),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.only(top: 12),
              child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
            ),
          const SizedBox(height: 24),
          Text('App 只读：可查看授权范围内的节点与告警，AK 允许时可静音与开启维护模式。AK 只用于这一次配对。', style: muted),
        ]),
      ),
    );
  }
}
