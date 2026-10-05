// 服务器详情（设计 14）：状态、资源、网络、趋势、流量、活动告警，以及（AK 允许时）静音与维护（设计 8.4.1）。
// 每 10 秒刷新；网络不可用时显示缓存与最后更新时间（设计 1.5.10）。
import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../cache.dart';
import '../format.dart';
import '../metrics.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/line_chart.dart';

class ServerDetailPage extends StatefulWidget {
  const ServerDetailPage({super.key, required this.api, required this.cache, required this.initial, required this.onRevoked});
  final ApiClient api;
  final CacheStore cache;
  final ServerView initial;
  final VoidCallback onRevoked;

  @override
  State<ServerDetailPage> createState() => _ServerDetailPageState();
}

const _ranges = ['1h', '6h', '24h', '7d', '30d'];

class _ServerDetailPageState extends State<ServerDetailPage> {
  late ServerView _s = widget.initial;
  DateTime? _updated;
  String? _error;
  Timer? _timer;

  String _range = '1h';
  String _metric = 'cpu';
  List<MetricPoint> _history = [];
  DateTime? _historyAt;
  bool _historyStale = false;
  bool _busy = false;
  HealthSummary? _health;
  int _ticks = 0;

  @override
  void initState() {
    super.initState();
    _refresh();
    _loadHistory();
    _loadHealth();
    _timer = Timer.periodic(const Duration(seconds: 10), (_) {
      _refresh();
      if (++_ticks % 6 == 0) _loadHealth(); // 摘要变化慢：每分钟刷新一次
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  void _revoked() {
    _timer?.cancel();
    Navigator.of(context).popUntil((r) => r.isFirst);
    widget.onRevoked();
  }

  Future<void> _refresh() async {
    try {
      final s = await widget.api.server(_s.id);
      if (!mounted) return;
      setState(() {
        _s = s;
        _updated = DateTime.now();
        _error = null;
      });
    } on DeviceRevoked {
      _revoked();
    } on ApiException catch (e) {
      if (e.status == 404 && mounted) {
        // 节点已删除，或已不在授权范围内（设计 17.3）
        _timer?.cancel();
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('节点已删除或不在授权范围内')));
        Navigator.of(context).pop();
        return;
      }
      if (mounted) setState(() => _error = e.message);
    } catch (e) {
      if (mounted) setState(() => _error = '$e');
    }
  }

  Future<void> _loadHealth() async {
    if (_s.status == 'pending') return;
    try {
      final h = await widget.api.health(_s.id);
      if (mounted) setState(() => _health = h);
    } on DeviceRevoked {
      _revoked();
    } catch (_) {
      // 网络失败：保留上次的摘要
    }
  }

  Future<void> _loadHistory() async {
    final range = _range;
    final cached = await widget.cache.history(_s.id, range);
    if (cached != null && mounted && range == _range) {
      setState(() {
        _history = cached.value;
        _historyAt = cached.at;
        _historyStale = true;
      });
    }
    try {
      final raw = await widget.api.historyRaw(_s.id, range);
      final now = DateTime.now();
      await widget.cache.saveHistory(_s.id, range, raw, now);
      if (!mounted || range != _range) return;
      setState(() {
        _history = raw.map((e) => MetricPoint.fromJson(e as Map<String, dynamic>)).toList();
        _historyAt = now;
        _historyStale = false;
      });
    } on DeviceRevoked {
      _revoked();
    } catch (_) {
      if (mounted && range == _range && cached == null) setState(() => _history = []);
    }
  }

  // ---- 静音与维护（设计 8.4.1、16.6） ----

  Future<void> _do(Future<void> Function() f, String done) async {
    setState(() => _busy = true);
    try {
      await f();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(done)));
      await _refresh();
    } on DeviceRevoked {
      _revoked();
    } catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('操作失败：$e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _pickDuration(String kind) async {
    final title = kind == 'mute' ? '静音告警' : '开启维护模式';
    final d = await showModalBottomSheet<String>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          ListTile(
            title: Text(title, style: const TextStyle(fontWeight: FontWeight.w600)),
            subtitle: Text(kind == 'mute' ? '照常记录告警，但不发送通知' : '继续采集，不产生告警，不计入需要关注'),
          ),
          for (final o in const [('1h', '1 小时'), ('8h', '8 小时'), ('24h', '24 小时'), ('', '直到手动结束')])
            ListTile(title: Text(o.$2), onTap: () => Navigator.pop(ctx, o.$1)),
        ]),
      ),
    );
    if (d == null) return;
    await _do(() => widget.api.silence(_s.id, kind, d), kind == 'mute' ? '已静音' : '已开启维护模式');
  }

  @override
  Widget build(BuildContext context) {
    final s = _s;
    final colors = StatusColors.of(context);
    final scheme = Theme.of(context).colorScheme;
    final muted = TextStyle(fontSize: 12, color: scheme.onSurfaceVariant);
    final live = isLive(s);
    final disk = diskSummary(s);
    return Scaffold(
      appBar: AppBar(title: Text('${flagEmoji(s.country).isNotEmpty ? '${flagEmoji(s.country)} ' : ''}${s.name}')),
      body: RefreshIndicator(
        onRefresh: () async {
          await Future.wait([_refresh(), _loadHistory(), _loadHealth()]);
        },
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 32), children: [
          Row(children: [
            Icon(Icons.circle, size: 10, color: colors.status(s.status)),
            const SizedBox(width: 6),
            Text(statusText[s.status] ?? s.status, style: TextStyle(color: colors.status(s.status), fontWeight: FontWeight.w600)),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                s.lastSeenAt > 0 ? '最后上报 ${fmtTime(s.lastSeenAt)}' : '尚未上报',
                style: muted,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            if (s.group.isNotEmpty) Text(s.group, style: muted),
          ]),
          if (_error != null)
            Card(
              color: scheme.errorContainer,
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: Text('当前离线，显示最近数据${_updated != null ? '（最后更新 ${fmtClock(_updated!)}）' : ''}\n$_error'),
              ),
            ),
          if (s.maintenance != null || s.muted != null)
            Card(
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: Text([
                  if (s.maintenance != null) '维护中${_until(s.maintenance!)}：不产生告警',
                  if (s.muted != null) '已静音${_until(s.muted!)}：告警不发送通知',
                ].join('\n')),
              ),
            ),
          const SizedBox(height: 8),
          if (live)
            Row(children: [
              _Gauge('CPU', s.latest!.cpu, gaugeLevel(s.latest!.cpu, 'cpu'), '${s.latest!.cores} 核 · 负载 ${s.latest!.load1.toStringAsFixed(2)}'),
              _Gauge('内存', s.latest!.mem, gaugeLevel(s.latest!.mem, 'mem'), '${fmtBytes(s.latest!.memUsed)} / ${fmtBytes(s.latest!.memTotal)}'),
              _Gauge('磁盘', disk?.usage, disk?.level ?? Level.ok, disk == null ? dash : '${fmtBytes(disk.used)} / ${fmtBytes(disk.total)}'),
            ])
          else
            Card(
              child: Padding(
                padding: const EdgeInsets.all(14),
                child: Text(s.status == 'pending' ? '待安装：在 Web 中复制安装命令到主机执行' : '节点离线，不显示实时指标', style: muted),
              ),
            ),
          if (live)
            Card(
              child: Padding(
                padding: const EdgeInsets.all(14),
                child: Row(children: [
                  Expanded(child: Text('↓ ${fmtBytes(s.latest!.rx, perSec: true)}', style: const TextStyle(fontSize: 16))),
                  Expanded(child: Text('↑ ${fmtBytes(s.latest!.tx, perSec: true)}', style: const TextStyle(fontSize: 16))),
                  if (s.latest!.uptime != null) Text('运行 ${fmtDuration(s.latest!.uptime)}', style: muted),
                ]),
              ),
            ),
          if (_health != null) _section(context, '健康摘要', _healthView(context)),
          _section(context, '趋势', _trend(context)),
          _section(context, '流量', _traffic(context)),
          if (s.alerts.isNotEmpty) _section(context, '活动告警', _alerts(context)),
          if (widget.api.session.allowLowRiskOps && s.status != 'pending') _section(context, '操作', _ops(context)),
        ]),
      ),
    );
  }

  String _until(Silence x) => x.endsAt == null ? '（直到手动结束）' : '（至 ${fmtTime(x.endsAt)}）';

  Widget _section(BuildContext context, String title, Widget child) => Card(
        child: Padding(
          padding: const EdgeInsets.all(14),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text(title, style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 15)),
            const SizedBox(height: 10),
            child,
          ]),
        ),
      );

  Widget _healthView(BuildContext context) {
    final h = _health!;
    final colors = StatusColors.of(context);
    Color lc(String l) => switch (l) { 'bad' => colors.bad, 'warn' => colors.warn, 'muted' => colors.muted, _ => colors.ok };
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(h.status, style: TextStyle(fontWeight: FontWeight.w600, color: lc(h.level))),
      for (final it in h.items)
        Padding(
          padding: const EdgeInsets.only(top: 8),
          child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Padding(padding: const EdgeInsets.only(top: 5), child: Icon(Icons.circle, size: 8, color: lc(it.level))),
            const SizedBox(width: 8),
            SizedBox(width: 36, child: Text(it.title, style: TextStyle(fontSize: 13, color: Theme.of(context).colorScheme.onSurfaceVariant))),
            Expanded(child: Text(it.text, style: const TextStyle(fontSize: 13))),
          ]),
        ),
    ]);
  }

  Widget _trend(BuildContext context) {
    final h = _history;
    final times = h.map((p) => p.ts).toList();
    final c0 = seriesColor(context, 0), c1 = seriesColor(context, 1);
    final (series, fmt, max) = switch (_metric) {
      'mem' => ([ChartSeries('内存', h.map((p) => p.mem).toList(), c0)], (double v) => fmtPct(v), 100.0),
      'disk' => ([ChartSeries('磁盘', h.map((p) => p.disk).toList(), c0)], (double v) => fmtPct(v), 100.0),
      'net' => (
          [
            ChartSeries('下载', h.map((p) => p.rx.toDouble()).toList(), c0),
            ChartSeries('上传', h.map((p) => p.tx.toDouble()).toList(), c1),
          ],
          (double v) => fmtBytes(v, perSec: true),
          null
        ),
      _ => ([ChartSeries('CPU', h.map((p) => p.cpu).toList(), c0)], (double v) => fmtPct(v), 100.0),
    };
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      SegmentedButton<String>(
        segments: const [
          ButtonSegment(value: 'cpu', label: Text('CPU')),
          ButtonSegment(value: 'mem', label: Text('内存')),
          ButtonSegment(value: 'net', label: Text('网络')),
          ButtonSegment(value: 'disk', label: Text('磁盘')),
        ],
        selected: {_metric},
        showSelectedIcon: false,
        onSelectionChanged: (v) => setState(() => _metric = v.first),
      ),
      const SizedBox(height: 8),
      Wrap(spacing: 6, children: [
        for (final r in _ranges)
          ChoiceChip(
            label: Text(r.toUpperCase()),
            selected: _range == r,
            visualDensity: VisualDensity.compact,
            onSelected: (_) {
              setState(() => _range = r);
              _loadHistory();
            },
          ),
      ]),
      const SizedBox(height: 12),
      LineChart(times: times, series: series, formatY: fmt, fixedMax: max),
      if (_historyStale && _historyAt != null)
        Padding(
          padding: const EdgeInsets.only(top: 6),
          child: Text('离线缓存，更新于 ${fmtTime(_historyAt!.millisecondsSinceEpoch ~/ 1000)}',
              style: TextStyle(fontSize: 11, color: Theme.of(context).colorScheme.onSurfaceVariant)),
        ),
    ]);
  }

  Widget _traffic(BuildContext context) {
    final t = _s.traffic;
    final colors = StatusColors.of(context);
    final muted = TextStyle(fontSize: 13, color: Theme.of(context).colorScheme.onSurfaceVariant);
    final pct = trafficPct(_s);
    final days = daysToReset(_s);
    final f = t.forecast;
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(
        t.limit > 0 ? '${fmtTraffic(t.used, t.unit)} / ${fmtTraffic(t.limit, t.unit)}' : '${fmtTraffic(t.used, t.unit)} · 不限流量',
        style: const TextStyle(fontSize: 20, fontWeight: FontWeight.w600),
      ),
      if (pct != null) ...[
        const SizedBox(height: 6),
        LinearProgressIndicator(
            value: (pct / 100).clamp(0.0, 1.0), color: pct >= 95 ? colors.bad : (pct >= 80 ? colors.warn : null)),
        const SizedBox(height: 8),
        Text('已使用 ${pct.toStringAsFixed(1)}% · 剩余 ${fmtTraffic((t.limit - t.used).clamp(0, t.limit), t.unit)}', style: muted),
      ],
      const SizedBox(height: 4),
      Text('距离重置 $days 天（${t.cycleEnd}）', style: muted),
      if (f != null)
        Text('预计周期总量 ${fmtTraffic(f.total, t.unit)}${f.over ? '，将超出额度' : ''}',
            style: f.over ? muted.copyWith(color: colors.bad) : muted),
    ]);
  }

  Widget _alerts(BuildContext context) {
    final colors = StatusColors.of(context);
    return Column(children: [
      for (final a in _s.alerts)
        ListTile(
          contentPadding: EdgeInsets.zero,
          dense: true,
          leading: Icon(Icons.warning_amber_rounded, color: a.severity == 'critical' ? colors.bad : colors.warn),
          title: Text(a.message),
          subtitle: Text('${fmtTime(a.firedAt)} 开始${a.silenced ? ' · 已静音' : ''}'),
        ),
    ]);
  }

  Widget _ops(BuildContext context) {
    final s = _s;
    return AbsorbPointer(
      absorbing: _busy,
      child: Wrap(spacing: 8, runSpacing: 8, children: [
        if (s.muted == null)
          OutlinedButton.icon(onPressed: () => _pickDuration('mute'), icon: const Icon(Icons.notifications_off_outlined), label: const Text('静音告警'))
        else
          OutlinedButton.icon(
              onPressed: () => _do(() => widget.api.endSilence(s.muted!.id), '已取消静音'),
              icon: const Icon(Icons.notifications_active_outlined),
              label: const Text('取消静音')),
        if (s.maintenance == null)
          OutlinedButton.icon(onPressed: () => _pickDuration('maintenance'), icon: const Icon(Icons.build_outlined), label: const Text('开启维护'))
        else
          OutlinedButton.icon(
              onPressed: () => _do(() => widget.api.endSilence(s.maintenance!.id), '已结束维护模式'),
              icon: const Icon(Icons.check_circle_outline),
              label: const Text('结束维护')),
      ]),
    );
  }
}

class _Gauge extends StatelessWidget {
  const _Gauge(this.label, this.value, this.level, this.sub);
  final String label;
  final double? value;
  final Level level;
  final String sub;

  @override
  Widget build(BuildContext context) {
    final c = StatusColors.of(context);
    final muted = Theme.of(context).colorScheme.onSurfaceVariant;
    return Expanded(
      child: Card(
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text(label, style: TextStyle(fontSize: 12, color: muted)),
            const SizedBox(height: 2),
            Text(fmtPct(value),
                style: TextStyle(fontSize: 22, fontWeight: FontWeight.w600, color: level == Level.ok ? null : c.level(level))),
            const SizedBox(height: 2),
            Text(sub, style: TextStyle(fontSize: 11, color: muted), maxLines: 2),
          ]),
        ),
      ),
    );
  }
}
