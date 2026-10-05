// 我的（设计 1.5.18）：监控中心、授权范围、推送状态、隐私模式（1.5.12）、解除配对。
import 'package:flutter/material.dart';

import '../api.dart';
import '../push.dart';
import '../version.dart';

class MePage extends StatelessWidget {
  const MePage({super.key, required this.api, required this.push, required this.privacy, required this.onPrivacy, required this.onUnpair});
  final ApiClient api;
  final PushState? push;
  final bool privacy;
  final ValueChanged<bool> onPrivacy;
  final Future<void> Function() onUnpair;

  Future<void> _confirmUnpair(BuildContext context) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('解除配对？'),
        content: Text('本机将退出 ${api.session.server.host}，之后需要在 Web 管理端重新生成 AK 才能再次连接。'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
          FilledButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('解除')),
        ],
      ),
    );
    if (ok == true) await onUnpair();
  }

  @override
  Widget build(BuildContext context) {
    final s = api.session;
    final scope = switch (s.scopeType) { 'group' => '分组 ${s.scopeValue}', 'servers' => '指定的节点', _ => '全部节点' };
    final error = Theme.of(context).colorScheme.error;
    return Scaffold(
      appBar: AppBar(title: const Text('我的')),
      body: ListView(children: [
        ListTile(leading: const Icon(Icons.dns_outlined), title: Text(s.server.host), subtitle: Text(s.server.toString())),
        ListTile(
          leading: const Icon(Icons.visibility_outlined),
          title: Text('可查看：$scope'),
          subtitle: Text(s.allowLowRiskOps ? '只读，可静音告警与开启维护模式' : '只读'),
        ),
        ListTile(
          leading: const Icon(Icons.notifications_outlined),
          title: const Text('告警推送'),
          subtitle: Text(push == null ? '检查中…' : pushStateText[push]!),
        ),
        SwitchListTile(
          secondary: const Icon(Icons.privacy_tip_outlined),
          title: const Text('隐私模式'),
          subtitle: const Text('隐藏公网 IP、价格与供应商，适合截图或给他人展示'),
          value: privacy,
          onChanged: onPrivacy,
        ),
        const Divider(),
        ListTile(
          leading: Icon(Icons.link_off, color: error),
          title: Text('解除配对', style: TextStyle(color: error)),
          onTap: () => _confirmUnpair(context),
        ),
        Padding(
          padding: const EdgeInsets.all(16),
          child: Text('VPS Monitor App $appVersion', style: TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant)),
        ),
      ]),
    );
  }
}
