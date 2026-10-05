// App 首页（设计 13）：快速看到异常、离线与整体状态。
// 授权范围内的节点，异常优先（设计 1.5.3）；按分组筛选；每 10 秒刷新（设计 1.5.9）；
// 网络不可用时显示离线缓存与“最后更新时间”（设计 1.5.10）。
import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../cache.dart';
import '../format.dart';
import '../metrics.dart';
import '../models.dart';
import '../push.dart';
import '../theme.dart';
import 'server_detail_page.dart';

class ServersPage extends StatefulWidget {
  const ServersPage({super.key, required this.api, required this.cache, required this.onUnpair, required this.onRevoked,
      this.pushSource = const NoPushTokenSource()});
  final ApiClient api;
  final CacheStore cache;
  final PushTokenSource pushSource;

  /// 用户主动解除本机配对
  final Future<void> Function() onUnpair;

  /// 设备授权已失效（被吊销或长期未使用），回到配对页（设计 12.7）
  final VoidCallback onRevoked;

  @override
  State<ServersPage> createState() => _ServersPageState();
}

class _ServersPageState extends State<ServersPage> {
  List<ServerView> _items = [];
  DateTime? _updated;
  String? _error;
  String _group = '';
  Timer? _timer;
  PushState? _push;

  @override
  void initState() {
    super.initState();
    _start();
  }

  Future<void> _start() async {
    final c = await widget.cache.servers(); // 先显示缓存，再联网刷新
    if (c != null && mounted && _updated == null) {
      setState(() {
        _items = c.value..sort(compareServers);
        _updated = c.at;
      });
    }
    await _refresh();
    _timer = Timer.periodic(const Duration(seconds: 10), (_) => _refresh());
    _syncPush();
  }

  // 登记推送（设计 30）：失败不影响列表，设置页显示状态
  Future<void> _syncPush() async {
    try {
      final st = await syncPush(widget.api, widget.pushSource);
      if (mounted) setState(() => _push = st);
    } on DeviceRevoked {
      widget.onRevoked();
    } catch (_) {
      // 网络失败：下次启动再试
    }
  }

  void _openSettings() {
    final s = widget.api.session;
    final scope = switch (s.scopeType) { 'group' => '分组 ${s.scopeValue}', 'servers' => '指定的节点', _ => '全部节点' };
    showModalBottomSheet<void>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
          ListTile(leading: const Icon(Icons.dns_outlined), title: Text(s.server.host), subtitle: Text(s.server.toString())),
          ListTile(
            leading: const Icon(Icons.visibility_outlined),
            title: Text('可查看：$scope'),
            subtitle: Text(s.allowLowRiskOps ? '只读，可静音告警与开启维护模式' : '只读'),
          ),
          ListTile(
            leading: const Icon(Icons.notifications_outlined),
            title: const Text('告警推送'),
            subtitle: Text(_push == null ? '检查中…' : pushStateText[_push]!),
          ),
          ListTile(
            leading: Icon(Icons.link_off, color: Theme.of(ctx).colorScheme.error),
            title: Text('解除配对', style: TextStyle(color: Theme.of(ctx).colorScheme.error)),
            onTap: () {
              Navigator.pop(ctx);
              _confirmUnpair();
            },
          ),
        ]),
      ),
    );
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    try {
      final items = await widget.api.servers();
      final now = DateTime.now();
      await widget.cache.saveServers(items, now);
      if (!mounted) return;
      setState(() {
        _items = items..sort(compareServers);
        _error = null;
        _updated = now;
      });
    } on DeviceRevoked {
      _timer?.cancel();
      widget.onRevoked();
    } catch (e) {
      if (mounted) setState(() => _error = '$e'); // 保留上次的数据
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

  Future<void> _open(ServerView s) async {
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => ServerDetailPage(api: widget.api, cache: widget.cache, initial: s, onRevoked: widget.onRevoked),
    ));
    _refresh(); // 详情页中可能静音或开启了维护
  }

  @override
  Widget build(BuildContext context) {
    final colors = StatusColors.of(context);
    final groups = {for (final s in _items) if (s.group.isNotEmpty) s.group}.toList()..sort();
    if (_group.isNotEmpty && !groups.contains(_group)) _group = '';
    final shown = _group.isEmpty ? _items : _items.where((s) => s.group == _group).toList();
    final online = _items.where((s) => s.status == 'online').length;
    final offline = _items.where((s) => s.status == 'offline').length;
    final attention = _items.where((s) => issues(s).isNotEmpty).length; // 与 Web“需要关注”同一规则（设计 9）

    return Scaffold(
      appBar: AppBar(
        title: const Text('我的服务器'),
        actions: [IconButton(icon: const Icon(Icons.settings_outlined), tooltip: '设置', onPressed: _openSettings)],
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 24), children: [
          Row(children: [
            _Count('在线', online, colors.ok),
            _Count('离线', offline, offline > 0 ? colors.bad : null),
            _Count('需要关注', attention, attention > 0 ? colors.warn : null),
          ]),
          if (_error != null)
            Card(
              color: Theme.of(context).colorScheme.errorContainer,
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: Text('当前离线${_updated != null ? '\n最后更新：${fmtClock(_updated!)}' : ''}\n$_error'),
              ),
            ),
          if (groups.isNotEmpty)
            SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Row(children: [
                for (final g in ['', ...groups])
                  Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: ChoiceChip(
                      label: Text(g.isEmpty ? '全部' : g),
                      selected: _group == g,
                      onSelected: (_) => setState(() => _group = g),
                    ),
                  ),
              ]),
            ),
          if (_items.isEmpty && _updated != null)
            const Padding(padding: EdgeInsets.all(32), child: Center(child: Text('授权范围内还没有节点'))),
          for (final s in shown) _ServerCard(s, onTap: () => _open(s)),
          if (_updated != null && _error == null)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Center(
                child: Text('${widget.api.session.server.host} · 更新于 ${fmtClock(_updated!)}',
                    style: TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant)),
              ),
            ),
        ]),
      ),
    );
  }
}

class _Count extends StatelessWidget {
  const _Count(this.label, this.value, this.color);
  final String label;
  final int value;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    return Expanded(
      child: Card(
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 12),
          child: Column(children: [
            Text('$value', style: TextStyle(fontSize: 24, fontWeight: FontWeight.w600, color: color)),
            Text(label, style: TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant)),
          ]),
        ),
      ),
    );
  }
}

class _ServerCard extends StatelessWidget {
  const _ServerCard(this.s, {required this.onTap});
  final ServerView s;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = StatusColors.of(context);
    final muted = TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant);
    final live = isLive(s);
    final disk = diskSummary(s);
    final pct = trafficPct(s);
    final probs = issues(s);
    return Card(
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.all(14),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Row(children: [
              if (flagEmoji(s.country).isNotEmpty) Text('${flagEmoji(s.country)} '),
              Expanded(
                child: Text(s.name,
                    style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16), overflow: TextOverflow.ellipsis),
              ),
              Icon(Icons.circle, size: 9, color: colors.status(s.status)),
              const SizedBox(width: 4),
              Text(statusText[s.status] ?? s.status, style: TextStyle(fontSize: 12, color: colors.status(s.status))),
            ]),
            if (s.maintenance != null || s.muted != null || probs.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Wrap(spacing: 6, runSpacing: 4, children: [
                  if (s.maintenance != null) _Tag('维护中', colors.muted),
                  if (s.muted != null) _Tag('已静音', colors.muted),
                  for (final i in probs) _Tag(i.text, colors.level(i.level)),
                ]),
              ),
            const SizedBox(height: 10),
            if (s.status == 'pending')
              Text('待安装：在 Web 中复制安装命令到主机执行', style: muted)
            else if (live) ...[
              Row(children: [
                _Stat('CPU', s.latest!.cpu, gaugeLevel(s.latest!.cpu, 'cpu')),
                _Stat('内存', s.latest!.mem, gaugeLevel(s.latest!.mem, 'mem')),
                _Stat('磁盘', disk?.usage, disk?.level ?? Level.ok),
              ]),
              const SizedBox(height: 8),
              Text('↓ ${fmtBytes(s.latest!.rx, perSec: true)}    ↑ ${fmtBytes(s.latest!.tx, perSec: true)}', style: muted),
            ] else
              Text(s.lastSeenAt > 0 ? '最后上报 ${fmtTime(s.lastSeenAt)}' : '尚未上报', style: muted),
            const SizedBox(height: 8),
            Text(
              s.traffic.limit > 0
                  ? '本周期 ${fmtTraffic(s.traffic.used, s.traffic.unit)} / ${fmtTraffic(s.traffic.limit, s.traffic.unit)}'
                  : '本周期 ${fmtTraffic(s.traffic.used, s.traffic.unit)} · 不限',
              style: muted,
            ),
            if (pct != null)
              Padding(
                padding: const EdgeInsets.only(top: 4),
                child: LinearProgressIndicator(
                  value: (pct / 100).clamp(0.0, 1.0),
                  color: pct >= 95 ? colors.bad : (pct >= 80 ? colors.warn : null),
                ),
              ),
          ]),
        ),
      ),
    );
  }
}

class _Stat extends StatelessWidget {
  const _Stat(this.label, this.value, this.level);
  final String label;
  final double? value;
  final Level level;

  @override
  Widget build(BuildContext context) {
    final c = StatusColors.of(context);
    return Expanded(
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Text(label, style: TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant)),
        Text(fmtPct(value), style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600, color: level == Level.ok ? null : c.level(level))),
      ]),
    );
  }
}

class _Tag extends StatelessWidget {
  const _Tag(this.text, this.color);
  final String text;
  final Color color;

  @override
  Widget build(BuildContext context) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
        decoration: BoxDecoration(border: Border.all(color: color), borderRadius: BorderRadius.circular(6)),
        child: Text(text, style: TextStyle(fontSize: 11, color: color)),
      );
}
