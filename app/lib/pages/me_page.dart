// 我的（设计 1.5.18）：监控中心（添加与切换，1.5.2）、授权范围、云账户（44.8）、推送状态、生物识别锁（1.5.11）、隐私模式（1.5.12）、解除配对。
import 'package:flutter/material.dart';

import '../api.dart';
import '../app_lock.dart';
import '../push.dart';
import '../version.dart';

class MePage extends StatelessWidget {
  const MePage({super.key, required this.api, required this.push, required this.privacy, required this.onPrivacy, required this.onUnpair,
      this.centers = const [], this.onSwitch, this.onAdd, this.onCloud});
  final ApiClient api;

  /// 全部监控中心的地址（当前中心为 api.session.server）
  final List<Uri> centers;
  final ValueChanged<int>? onSwitch;
  final VoidCallback? onAdd;

  /// 云账户（设计 44.8）：只读
  final VoidCallback? onCloud;
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
    if (ok != true || !context.mounted) return;
    // 敏感操作前验证（设计 1.5.11）
    final lock = AppLockScope.of(context);
    if (lock != null && !await lock.guard('解除配对')) return;
    await onUnpair();
  }

  // 生物识别锁（设计 1.5.11）：打开时先验证一次，失败时提示原因、不打开
  List<Widget> _lockTiles(BuildContext context, AppLockController lock) {
    Future<void> change({bool? onOpen, bool? sensitive}) async {
      final err = await lock.set(onOpen: onOpen, sensitive: sensitive);
      if (err != null && err.isNotEmpty && context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(err)));
      }
    }

    return [
      SwitchListTile(
        secondary: const Icon(Icons.fingerprint),
        title: const Text('打开 App 时验证'),
        subtitle: const Text('Face ID / 指纹 / 设备密码；离开超过 1 分钟后回来也要验证'),
        value: lock.settings.onOpen,
        onChanged: (v) => change(onOpen: v),
      ),
      SwitchListTile(
        secondary: const Icon(Icons.verified_user_outlined),
        title: const Text('敏感操作前验证'),
        subtitle: const Text('静音告警、开启维护与解除配对前先验证'),
        value: lock.settings.sensitive,
        onChanged: (v) => change(sensitive: v),
      ),
    ];
  }

  Widget _centerTile(BuildContext context, Uri u, int i) {
    final current = u == api.session.server;
    return ListTile(
      leading: Icon(current ? Icons.radio_button_checked : Icons.radio_button_unchecked,
          color: current ? Theme.of(context).colorScheme.primary : null),
      title: Text(u.host),
      subtitle: Text(current ? '当前 · ${u.toString()}' : u.toString()),
      onTap: current || onSwitch == null ? null : () => onSwitch!(i),
    );
  }

  @override
  Widget build(BuildContext context) {
    final s = api.session;
    final scope = switch (s.scopeType) { 'group' => '分组 ${s.scopeValue}', 'servers' => '指定的节点', _ => '全部节点' };
    final error = Theme.of(context).colorScheme.error;
    return Scaffold(
      appBar: AppBar(title: const Text('我的')),
      body: ListView(children: [
        const Padding(padding: EdgeInsets.fromLTRB(16, 12, 16, 4), child: Text('监控中心', style: TextStyle(fontWeight: FontWeight.w600))),
        for (var i = 0; i < (centers.isEmpty ? 1 : centers.length); i++)
          _centerTile(context, centers.isEmpty ? s.server : centers[i], i),
        if (onAdd != null)
          ListTile(leading: const Icon(Icons.add), title: const Text('添加监控中心'), subtitle: const Text('连接另一套自建面板'), onTap: onAdd),
        const Divider(),
        ListTile(
          leading: const Icon(Icons.visibility_outlined),
          title: Text('可查看：$scope'),
          subtitle: Text(s.allowLowRiskOps ? '只读，可静音告警与开启维护模式' : '只读'),
        ),
        if (onCloud != null)
          ListTile(
            leading: const Icon(Icons.cloud_outlined),
            title: const Text('云账户'),
            subtitle: const Text('费用、余额与云实例（只读）'),
            trailing: const Icon(Icons.chevron_right),
            onTap: onCloud,
          ),
        ListTile(
          leading: const Icon(Icons.notifications_outlined),
          title: const Text('告警推送'),
          subtitle: Text(push == null ? '检查中…' : pushStateText[push]!),
        ),
        if (AppLockScope.of(context) case final lock?) ..._lockTiles(context, lock),
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
