// 测速对比（设计 42.3.5）：与 Web 的“测速对比”同一接口与口径。手机屏幕放不下“节点 × 目标”的整张表，
// 改为先选一个目标，再按延迟从低到高列出各节点，第一名标“最快”；颜色分级与节点详情相同（设计 42.3.3）。每分钟刷新，只读。
import 'dart:async';

import 'package:flutter/material.dart';

import '../widgets/stale_banner.dart';
import '../errors.dart';
import '../api.dart';
import '../format.dart';
import '../models.dart';
import '../theme.dart';

/// 测速对比页：选一个目标，按延迟排列各节点
class LatencyComparePage extends StatefulWidget {
  const LatencyComparePage({super.key, required this.api, required this.onRevoked, this.onOpenServer});
  final ApiClient api;
  final VoidCallback onRevoked;
  final ValueChanged<int>? onOpenServer;

  @override
  State<LatencyComparePage> createState() => _LatencyComparePageState();
}

class _LatencyComparePageState extends State<LatencyComparePage> {
  LatencyOverview? _data;
  String? _error;
  int _target = 0;
  String _group = '';
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _load();
    _timer = Timer.periodic(const Duration(minutes: 1), (_) => _load());
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final d = await widget.api.latencyOverview();
      if (!mounted) return;
      setState(() {
        _data = d;
        _error = null;
      });
    } on DeviceRevoked {
      _timer?.cancel();
      widget.onRevoked();
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.status == 404 ? '面板版本较旧，升级面板后可用' : e.message);
    } catch (e) {
      if (mounted) setState(() => _error = describeError(e)); // 保留上次的数据
    }
  }

  @override
  Widget build(BuildContext context) {
    final d = _data;
    final muted = Theme.of(context).colorScheme.onSurfaceVariant;
    final small = TextStyle(fontSize: 12, color: muted);
    final children = <Widget>[];
    if (_error != null) {
      children.add(StaleBanner(error: _error!, note: '下拉可重新加载'));
    }
    if (d == null) {
      if (_error == null) children.add(const Padding(padding: EdgeInsets.all(32), child: Center(child: CircularProgressIndicator())));
    } else if (d.targets.isEmpty || d.cells.isEmpty) {
      children.add(Padding(
        padding: const EdgeInsets.all(24),
        child: Text('最近 1 小时还没有测速结果。在 Web 的“监测 → 测速设置”中添加并启用目标。', style: small, textAlign: TextAlign.center),
      ));
    } else {
      final groups = d.groups;
      if (!groups.contains(_group)) _group = '';
      final target = d.targets.firstWhere((t) => t.id == _target, orElse: () => d.targets.first);
      Widget chips<T>(List<T> values, T selected, String Function(T) label, ValueChanged<T> onSelected) => SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(vertical: 4),
            child: Row(children: [
              for (final v in values)
                Padding(
                  padding: const EdgeInsets.only(right: 8),
                  child: ChoiceChip(label: Text(label(v)), selected: v == selected, onSelected: (_) => setState(() => onSelected(v))),
                ),
            ]),
          );
      children.add(chips<int>(
          [for (final t in d.targets) t.id], target.id, (id) => d.targets.firstWhere((t) => t.id == id).name, (id) => _target = id));
      if (groups.length > 1) children.add(chips<String>(['', ...groups], _group, (g) => g.isEmpty ? '全部分组' : g, (g) => _group = g));
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(4, 8, 4, 4),
        child: Text(fmtProbeWhere(target.proto, target.port, target.address), style: small.copyWith(fontFamily: 'monospace')),
      ));
      final rank = d.ranking(target.id, group: _group);
      if (rank.isEmpty) {
        children.add(
            Padding(padding: const EdgeInsets.all(24), child: Text('这个分组的节点最近 1 小时没有测这个目标', style: small, textAlign: TextAlign.center)));
      } else {
        children.add(Card(child: Column(children: [for (final (i, c) in rank.indexed) _row(context, d, c, i)])));
      }
    }
    children.add(Padding(
      padding: const EdgeInsets.all(12),
      child: Text('各节点最近 1 小时的延迟中位数与平均丢包；目标在 Web 的“监测 → 测速设置”中管理。', style: small, textAlign: TextAlign.center),
    ));
    return Scaffold(
      appBar: AppBar(title: const Text('测速对比')),
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 24), children: children),
      ),
    );
  }

  Widget _row(BuildContext context, LatencyOverview d, OverviewCell c, int i) {
    final colors = StatusColors.of(context);
    final muted = Theme.of(context).colorScheme.onSurfaceVariant;
    final lat = c.latency;
    // 颜色分级（设计 42.3.3）：< 100 ms 好；> 200 ms 或丢包 > 5% 警告；全部失败为不可达
    final color = lat == null
        ? colors.bad
        : (lat > 200 || c.loss > 5)
            ? colors.warn
            : lat < 100
                ? colors.ok
                : null;
    final best = i == 0 && lat != null;
    const tabular = [FontFeature.tabularFigures()];
    return ListTile(
      dense: true,
      leading: SizedBox(
        width: 24,
        child: Center(child: Text('${i + 1}', style: TextStyle(color: muted, fontFeatures: tabular))),
      ),
      title: Row(children: [
        Flexible(child: Text(d.servers[c.serverId]!.$1, overflow: TextOverflow.ellipsis)),
        if (best)
          Padding(
            padding: const EdgeInsets.only(left: 6),
            child: Text('最快', style: TextStyle(fontSize: 11, color: colors.ok, fontWeight: FontWeight.w600)),
          ),
      ]),
      trailing: Column(mainAxisAlignment: MainAxisAlignment.center, crossAxisAlignment: CrossAxisAlignment.end, children: [
        Text(lat == null ? '不可达' : fmtLatency(lat),
            style: TextStyle(fontSize: 14, color: color, fontWeight: best ? FontWeight.w600 : null, fontFeatures: tabular)),
        Text('丢包 ${fmtPct(c.loss)}', style: TextStyle(fontSize: 11, color: c.loss > 5 ? colors.warn : muted, fontFeatures: tabular)),
      ]),
      onTap: widget.onOpenServer == null ? null : () => widget.onOpenServer!(c.serverId),
    );
  }
}
