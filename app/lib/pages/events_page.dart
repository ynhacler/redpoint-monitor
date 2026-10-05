// 事件中心（设计 1.5.17）：告警触发与恢复的时间线，最新在前；面板按设备的授权范围过滤（17.3）。
// TODO(N1): Agent 升级、证书到期等系统事件随面板的事件接口加入。
import 'package:flutter/material.dart';

import '../api.dart';
import '../format.dart';
import '../models.dart';
import '../theme.dart';

class EventsPage extends StatefulWidget {
  const EventsPage({super.key, required this.api, required this.onRevoked, required this.onOpenServer});
  final ApiClient api;
  final VoidCallback onRevoked;
  final ValueChanged<int> onOpenServer;

  @override
  State<EventsPage> createState() => _EventsPageState();
}

class _EventsPageState extends State<EventsPage> {
  final _items = <AlertEvent>[];
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
    _cursor = '';
    _done = false;
    await _more();
  }

  Future<void> _more() async {
    if (_loading || _done) return;
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
            if (_items.isEmpty && !_loading && _error == null)
              Padding(
                padding: const EdgeInsets.all(32),
                child: Center(child: Text(_activeOnly ? '没有正在进行的告警' : '还没有事件')),
              ),
            for (final e in _items) _EventTile(e, onTap: () => widget.onOpenServer(e.serverId)),
            if (_loading) const Padding(padding: EdgeInsets.all(16), child: Center(child: CircularProgressIndicator())),
          ]),
        ),
      ),
    );
  }
}

class _EventTile extends StatelessWidget {
  const _EventTile(this.e, {required this.onTap});
  final AlertEvent e;
  final VoidCallback onTap;

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
              : '${fmtTime(e.firedAt)} 开始 · 进行中',
          style: muted,
        ),
      ),
    );
  }
}
