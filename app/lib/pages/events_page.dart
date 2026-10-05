// 事件中心（设计 1.5.17）：告警触发与恢复的时间线，最新在前；面板按设备的授权范围过滤（17.3）。
// 连接了多个监控中心时可切到“全部中心”：各中心最近 50 条合并为一条时间线（跨中心事件流，设计 1.5.2）。
// TODO(N1): Agent 升级、证书到期等系统事件随面板的事件接口加入。
import 'package:flutter/material.dart';

import '../api.dart';
import '../centers.dart';
import '../format.dart';
import '../models.dart';
import '../theme.dart';

class EventsPage extends StatefulWidget {
  const EventsPage({super.key, required this.api, required this.onRevoked, required this.onOpenServer, this.centers = const [],
      this.onSwitch, this.onCentersChanged});
  final ApiClient api;
  final VoidCallback onRevoked;
  final ValueChanged<int> onOpenServer;

  /// 全部监控中心（含当前中心）；多于一个时可以合并查看
  final List<CenterHandle> centers;
  final ValueChanged<int>? onSwitch;
  final VoidCallback? onCentersChanged;

  @override
  State<EventsPage> createState() => _EventsPageState();
}

class _EventsPageState extends State<EventsPage> {
  final _items = <AlertEvent>[];

  /// 合并模式下每条事件所属的中心下标（与 _items 对应）
  final _centerOf = <int>[];
  bool _allCenters = false;
  String _cursor = '';
  bool _loading = false;
  bool _done = false;
  String? _error;
  bool _activeOnly = false;

  @override
  void initState() {
    super.initState();
    _reload();
  }

  Future<void> _reload() async {
    _items.clear();
    _centerOf.clear();
    _cursor = '';
    _done = false;
    await (_allCenters ? _loadAll() : _more());
  }

  /// 跨中心：每个中心取最近 50 条，按最近的时间（恢复时间或触发时间）合并
  Future<void> _loadAll() async {
    setState(() => _loading = true);
    final merged = <(int, AlertEvent)>[];
    final errors = <String>[];
    await Future.wait([
      for (final (i, c) in widget.centers.indexed)
        () async {
          try {
            final (items, _) = await c.api.alerts(state: _activeOnly ? 'active' : 'all');
            merged.addAll(items.map((e) => (i, e)));
          } on DeviceRevoked {
            widget.onCentersChanged?.call();
          } catch (e) {
            errors.add('${c.server.host}：$e');
          }
        }(),
    ]);
    int at(AlertEvent e) => e.resolvedAt ?? e.firedAt;
    merged.sort((a, b) => at(b.$2).compareTo(at(a.$2)));
    if (!mounted) return;
    setState(() {
      _items.addAll(merged.map((x) => x.$2));
      _centerOf.addAll(merged.map((x) => x.$1));
      _done = true;
      _error = errors.isEmpty ? null : errors.join('\n');
      _loading = false;
    });
  }

  Future<void> _more() async {
    if (_loading || _done || _allCenters) return;
    setState(() => _loading = true);
    try {
      final (items, next) = await widget.api.alerts(state: _activeOnly ? 'active' : 'all', cursor: _cursor);
      if (!mounted) return;
      setState(() {
        _items.addAll(items);
        _cursor = next;
        _done = next.isEmpty;
        _error = null;
      });
    } on DeviceRevoked {
      widget.onRevoked();
    } catch (e) {
      if (mounted) setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('事件'),
        actions: [
          Padding(
            padding: const EdgeInsets.only(right: 12),
            child: SegmentedButton<bool>(
              segments: const [ButtonSegment(value: false, label: Text('全部')), ButtonSegment(value: true, label: Text('进行中'))],
              selected: {_activeOnly},
              showSelectedIcon: false,
              onSelectionChanged: (v) {
                setState(() => _activeOnly = v.first);
                _reload();
              },
            ),
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: _reload,
        child: NotificationListener<ScrollNotification>(
          onNotification: (n) {
            if (n.metrics.pixels > n.metrics.maxScrollExtent - 300) _more();
            return false;
          },
          child: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 24), children: [
            if (_error != null)
              Card(
                color: Theme.of(context).colorScheme.errorContainer,
                child: Padding(padding: const EdgeInsets.all(12), child: Text('加载失败：$_error')),
              ),
            if (widget.centers.length > 1)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: FilterChip(
                  label: const Text('全部监控中心'),
                  selected: _allCenters,
                  onSelected: (v) {
                    setState(() => _allCenters = v);
                    _reload();
                  },
                ),
              ),
            if (_allCenters)
              Padding(
                padding: const EdgeInsets.fromLTRB(4, 0, 4, 8),
                child: Text('每个监控中心最近 50 条，按时间合并', style: TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant)),
              ),
            if (_items.isEmpty && !_loading && _error == null)
              Padding(
                padding: const EdgeInsets.all(32),
                child: Center(child: Text(_activeOnly ? '没有正在进行的告警' : '还没有事件')),
              ),
            for (final (i, e) in _items.indexed)
              if (_allCenters && i < _centerOf.length && !identical(widget.centers[_centerOf[i]].api, widget.api))
                _EventTile(e, center: widget.centers[_centerOf[i]].server.host, onTap: () => widget.onSwitch?.call(_centerOf[i]))
              else
                _EventTile(e, center: _allCenters ? widget.api.session.server.host : null, onTap: () => widget.onOpenServer(e.serverId)),
            if (_loading) const Padding(padding: EdgeInsets.all(16), child: Center(child: CircularProgressIndicator())),
          ]),
        ),
      ),
    );
  }
}

class _EventTile extends StatelessWidget {
  const _EventTile(this.e, {required this.onTap, this.center});
  final AlertEvent e;
  final VoidCallback onTap;

  /// 合并模式下显示所属的监控中心
  final String? center;

  @override
  Widget build(BuildContext context) {
    final colors = StatusColors.of(context);
    final resolved = e.state == 'resolved';
    final color = resolved ? colors.ok : (e.severity == 'critical' ? colors.bad : (e.severity == 'warning' ? colors.warn : colors.muted));
    final muted = TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant);
    return Card(
      child: ListTile(
        onTap: onTap,
        leading: Icon(resolved ? Icons.check_circle_outline : Icons.warning_amber_rounded, color: color),
        title: Text('${e.serverName} · ${e.message}', maxLines: 2, overflow: TextOverflow.ellipsis),
        subtitle: Text(
          resolved
              ? '${fmtTime(e.firedAt)} 开始 · ${fmtTime(e.resolvedAt)} 恢复（持续 ${fmtDuration((e.resolvedAt ?? e.firedAt) - e.firedAt)}）'
                  '${center != null ? '\n$center' : ''}'
              : '${fmtTime(e.firedAt)} 开始 · 进行中'
              '${center != null ? '\n$center' : ''}',
          style: muted,
        ),
      ),
    );
  }
}
