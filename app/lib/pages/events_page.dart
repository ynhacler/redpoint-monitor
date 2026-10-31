// 事件中心（设计 1.5.17）：告警触发与恢复的时间线，最新在前；面板按设备的授权范围过滤（17.3）。
// 连接了多个监控中心时可切到“全部中心”：各中心最近 50 条合并为一条时间线（跨中心事件流，设计 1.5.2）。
// “系统”标签：Agent 升级结果、到期提醒、面板自身告警、节点配置变更（GET /system-events，修订第 163、273 条），只看当前中心。
// 变更的细节只给 Web 管理员，App 只看到标题（设计 4.9.5）。
import 'package:flutter/material.dart';

import '../widgets/stale_banner.dart';
import '../errors.dart';
import '../api.dart';
import '../centers.dart';
import '../format.dart';
import '../models.dart';
import '../theme.dart';

/// 事件中心：告警的触发与恢复（设计 1.5.17）
class EventsPage extends StatefulWidget {
  const EventsPage(
      {super.key,
      required this.api,
      required this.onRevoked,
      required this.onOpenServer,
      this.centers = const [],
      this.onSwitch,
      this.onCentersChanged});
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

  // 系统事件（修订第 163 条）
  bool _system = false;
  final _sys = <SystemEvent>[];
  String _sysCursor = '';
  bool _sysDone = false;

  /// 面板版本过旧，没有系统事件接口
  bool _sysUnsupported = false;

  @override
  void initState() {
    super.initState();
    _reload();
  }

  Future<void> _reload() async {
    if (_system) {
      _sys.clear();
      _sysCursor = '';
      _sysDone = false;
      return _moreSystem();
    }
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
            errors.add('${centerLabel(c.server)}：${describeError(e)}');
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

  Future<void> _moreSystem() async {
    if (_loading || _sysDone) return;
    setState(() => _loading = true);
    try {
      final (items, next) = await widget.api.systemEvents(cursor: _sysCursor);
      if (!mounted) return;
      setState(() {
        _sys.addAll(items);
        _sysCursor = next;
        _sysDone = next.isEmpty;
        _sysUnsupported = false;
        _error = null;
      });
    } on DeviceRevoked {
      widget.onRevoked();
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        if (e.status == 404) {
          _sysUnsupported = true;
          _sysDone = true;
        } else {
          _error = describeError(e);
        }
      });
    } catch (e) {
      if (mounted) setState(() => _error = describeError(e));
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _more() async {
    if (_system) return _moreSystem();
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
      if (mounted) setState(() => _error = describeError(e));
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
          if (!_system)
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
              StaleBanner(error: _error!, note: '下拉可重新加载'),
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: SegmentedButton<bool>(
                segments: const [
                  ButtonSegment(value: false, label: Text('告警'), icon: Icon(Icons.warning_amber_rounded)),
                  ButtonSegment(value: true, label: Text('系统'), icon: Icon(Icons.settings_suggest_outlined)),
                ],
                selected: {_system},
                showSelectedIcon: false,
                onSelectionChanged: (v) {
                  setState(() => _system = v.first);
                  _reload();
                },
              ),
            ),
            if (_system) ...[
              if (_sysUnsupported)
                const Padding(padding: EdgeInsets.all(32), child: Center(child: Text('面板版本较旧，升级面板后显示系统事件', textAlign: TextAlign.center))),
              if (_sys.isEmpty && !_loading && _error == null && !_sysUnsupported)
                const Padding(
                  padding: EdgeInsets.all(32),
                  child: Center(child: Text('还没有系统事件\nAgent 升级结果、到期提醒与面板异常会显示在这里', textAlign: TextAlign.center)),
                ),
              for (final e in _sys)
                _SystemTile(e, onTap: e.serverId > 0 && e.serverName.isNotEmpty ? () => widget.onOpenServer(e.serverId) : null),
            ] else ...[
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
                  _EventTile(e, center: centerLabel(widget.centers[_centerOf[i]].server), onTap: () => widget.onSwitch?.call(_centerOf[i]))
                else
                  _EventTile(e, center: _allCenters ? centerLabel(widget.api.session.server) : null, onTap: () => widget.onOpenServer(e.serverId)),
            ],
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

/// 一条系统事件：图标表示种类，颜色表示级别；属于节点且节点仍在时可点开节点详情。
class _SystemTile extends StatelessWidget {
  const _SystemTile(this.e, {this.onTap});
  final SystemEvent e;
  final VoidCallback? onTap;

  static const _icons = {
    'upgrade': Icons.system_update_alt,
    'reminder': Icons.event_note_outlined,
    'panel': Icons.dns_outlined,
    'change': Icons.manage_history,
  };

  @override
  Widget build(BuildContext context) {
    final colors = StatusColors.of(context);
    final ok = e.title.startsWith('✅');
    final color = ok ? colors.ok : (e.severity == 'critical' ? colors.bad : (e.severity == 'warning' ? colors.warn : colors.muted));
    final muted = TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant);
    return Card(
      child: ListTile(
        onTap: onTap,
        leading: Icon(_icons[e.kind] ?? Icons.info_outline, color: color),
        title: Text(e.title, maxLines: 2, overflow: TextOverflow.ellipsis),
        subtitle: Text([if (e.message.isNotEmpty) e.message, fmtTime(e.createdAt)].join('\n'),
            style: muted, maxLines: 4, overflow: TextOverflow.ellipsis),
      ),
    );
  }
}
