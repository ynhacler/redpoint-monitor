// 服务器详情（设计 14）：状态、资源、网络、趋势、流量、活动告警，以及（AK 允许时）静音与维护（设计 8.4.1）。
// 每 10 秒刷新；网络不可用时显示缓存与最后更新时间（设计 1.5.10）。
import 'dart:async';

import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../widgets/stale_banner.dart';
import '../errors.dart';
import '../api.dart';
import '../app_lock.dart';
import '../cache.dart';
import '../format.dart';
import '../metrics.dart';
import '../models.dart';
import '../privacy.dart';
import '../theme.dart';
import '../tokens.dart';
import '../widgets/line_chart.dart';

/// 节点详情：实时指标、健康摘要、趋势、流量、测速、国内可达性、资产、云实例与操作
class ServerDetailPage extends StatefulWidget {
  const ServerDetailPage({super.key, required this.api, required this.cache, required this.initial, required this.onRevoked,
      this.privacy = false, this.cloud});
  final ApiClient api;

  /// 云账户摘要（首页已读取，设计 44.8）：关联到这台节点的云实例显示在“云实例”中
  final CloudSummary? cloud;
  final CacheStore cache;
  final ServerView initial;
  final VoidCallback onRevoked;

  /// 隐私模式：隐藏 IP、价格与供应商（设计 1.5.12）
  final bool privacy;

  @override
  State<ServerDetailPage> createState() => _ServerDetailPageState();
}

const _ranges = ['1h', '6h', '24h', '7d', '30d'];
const _rangeSeconds = {'1h': 3600, '6h': 21600, '24h': 86400, '7d': 604800, '30d': 2592000};

class _ServerDetailPageState extends State<ServerDetailPage> {
  late ServerView _s = widget.initial;
  DateTime? _updated;
  String? _error;
  Timer? _timer;

  String _range = '1h';
  bool _healthAll = false; // 健康摘要展开全部（修订第 256 条）
  String _metric = 'cpu';
  List<MetricPoint> _history = [];
  DateTime? _historyAt;
  bool _historyStale = false;
  bool _busy = false;
  HealthSummary? _health;
  int _ticks = 0;
  // 测速（设计 42.3.2）：与 Web 相同的表格与曲线；旧版面板没有接口时不显示
  LatencyView? _latency;
  // 国内可达性（设计 42.2，修订第 156 条）：没有设置国内探针或旧版面板时不显示
  ReachView? _reach;
  String _latencyRange = '24h';
  String _latencySel = ''; // “p:地区” 或 “i:目标 ID”

  @override
  void initState() {
    super.initState();
    _refresh();
    _loadHistory();
    _loadHealth();
    _loadLatency();
    _loadReach();
    _timer = Timer.periodic(const Duration(seconds: 10), (_) {
      _refresh();
      if (++_ticks % 6 == 0) {
        _loadHealth(); // 摘要变化慢：每分钟刷新一次
        _loadLatency(); // 测速每几分钟才测一轮
        _loadReach();
      }
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
      if (mounted) setState(() => _error = describeError(e));
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

  Future<void> _loadReach() async {
    if (_s.status == 'pending') return;
    try {
      final v = await widget.api.reach(_s.id);
      if (mounted) setState(() => _reach = v);
    } on DeviceRevoked {
      _revoked();
    } catch (_) {
      // 网络失败或旧版面板：保留上次的数据
    }
  }

  Future<void> _loadLatency() async {
    if (_s.status == 'pending') return;
    try {
      final v = await widget.api.latency(_s.id, _latencyRange);
      if (mounted) setState(() => _latency = v);
    } on DeviceRevoked {
      _revoked();
    } catch (_) {
      // 网络失败或旧版面板：保留上次的数据
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
    // 静音、维护属于敏感操作：开启了“敏感操作前验证”时先验证（设计 1.5.11）
    final lock = AppLockScope.of(context);
    if (lock != null && !await lock.guard(done)) return;
    if (!mounted) return;
    setState(() => _busy = true);
    try {
      await f();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(done)));
      await _refresh();
    } on DeviceRevoked {
      _revoked();
    } catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('操作失败：${describeError(e)}')));
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
    final live = isLive(s);
    final disk = diskSummary(s);
    return Scaffold(
      appBar: AppBar(title: Text('${flagEmoji(s.country).isNotEmpty ? '${flagEmoji(s.country)} ' : ''}${s.name}')),
      body: RefreshIndicator(
        onRefresh: () async {
          await Future.wait([_refresh(), _loadHistory(), _loadHealth(), _loadLatency(), _loadReach()]);
        },
        // 与 Web 节点详情同一结构（修订第 214 条）：概况 → 健康摘要 → 分页（监控 / 网络 / 应用 / 告警）
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 32), children: [
          if (_error != null) StaleBanner(error: _error!, updated: _updated, onRetry: _refresh),
          _summary(context),
          if (_health != null) _section(context, '健康摘要', _healthView(context)),
          const SizedBox(height: 4),
          _tabs(context),
          const SizedBox(height: 4),
          ...switch (_tab) {
            'net' => [
                _section(context, '流量', _traffic(context)),
                if (UnlockResult.of(s) case final u? when s.status != 'pending') _section(context, '流媒体与网站解锁', _unlockView(context, u)),
                if (_latency != null && (_latency!.targets.isNotEmpty || _latency!.mode == 'off'))
                  _section(context, '测速延迟', _latencyView(context)),
                if (_reach != null && _reach!.configured && _reach!.probes.isNotEmpty) _section(context, '国内可达性', _reachView(context)),
                if (_cloudInstances().isNotEmpty) _section(context, '云实例', _cloudView(context)),
              ],
            'apps' => [
                if (ContainerInfo.of(s).isNotEmpty || ContainerMissing.of(s).isNotEmpty)
                  _section(context, '容器', _containersView(context, ContainerInfo.of(s), ContainerMissing.of(s)))
                else
                  _empty(context, '没有发现容器。Agent 从 cgroup 读取 Docker / Podman 容器，容器名在 Web 节点详情中设置。'),
              ],
            'alerts' => [
                if (s.alerts.isNotEmpty) _section(context, '活动告警', _alerts(context)) else _empty(context, '没有进行中的告警。'),
                if (widget.api.session.allowLowRiskOps && s.status != 'pending') _section(context, '静音与维护', _ops(context)),
              ],
            _ => [
                if (live) ...[
                  Row(children: [
                    _Gauge('CPU', s.latest!.cpu, gaugeLevel(s.latest!.cpu, 'cpu'), '${s.latest!.cores} 核 · 负载 ${s.latest!.load1.toStringAsFixed(2)}'),
                    _Gauge('内存', s.latest!.mem, gaugeLevel(s.latest!.mem, 'mem'), '${fmtBytes(s.latest!.memUsed)} / ${fmtBytes(s.latest!.memTotal)}'),
                    _Gauge('磁盘', disk?.usage, disk?.level ?? Level.ok, disk == null ? dash : '${fmtBytes(disk.used)} / ${fmtBytes(disk.total)}'),
                  ]),
                  _netCard(context),
                ] else
                  _empty(context, s.status == 'pending' ? '待安装：在 Web 中复制安装命令到主机执行' : '节点离线，不显示实时数值；历史曲线见下方。'),
                _section(context, '历史', _trend(context)),
              ],
          },
        ]),
      ),
    );
  }

  String _tab = 'monitor';

  // 分页：与 Web 的“监控 / 网络 / 应用 / 告警”一致；告警页带进行中的告警数
  Widget _tabs(BuildContext context) {
    final c = appColors(context);
    final colors = StatusColors.of(context);
    final n = _s.alerts.length;
    Widget tab(String key, String label, {int badge = 0}) {
      final on = _tab == key;
      return Expanded(
        child: InkWell(
          key: ValueKey('detail-tab-$key'),
          borderRadius: BorderRadius.circular(8),
          onTap: () => setState(() => _tab = key),
          child: Container(
            padding: const EdgeInsets.symmetric(vertical: 8),
            decoration: BoxDecoration(
              color: on ? c.surface : null,
              borderRadius: BorderRadius.circular(8),
              boxShadow: on ? [BoxShadow(color: Colors.black.withValues(alpha: 0.06), blurRadius: 4, offset: const Offset(0, 1))] : null,
            ),
            child: Row(mainAxisAlignment: MainAxisAlignment.center, children: [
              Text(label, style: TextStyle(fontSize: 14, fontWeight: on ? FontWeight.w600 : FontWeight.w500, color: on ? c.text : c.textMuted)),
              if (badge > 0) ...[
                const SizedBox(width: 4),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 5),
                  decoration: BoxDecoration(color: colors.bad, borderRadius: BorderRadius.circular(99)),
                  child: Text('$badge', style: const TextStyle(fontSize: 11, color: Colors.white, fontWeight: FontWeight.w600)),
                ),
              ],
            ]),
          ),
        ),
      );
    }

    return Container(
      padding: const EdgeInsets.all(3),
      decoration: BoxDecoration(color: c.surface2, borderRadius: BorderRadius.circular(10), border: Border.all(color: c.border)),
      child: Row(children: [tab('monitor', '监控'), tab('net', '网络'), tab('apps', '应用'), tab('alerts', '告警', badge: n)]),
    );
  }

  Widget _empty(BuildContext context, String text) => Card(
        child: Padding(
          padding: const EdgeInsets.all(14),
          child: Text(text, style: TextStyle(fontSize: 13, color: Theme.of(context).colorScheme.onSurfaceVariant)),
        ),
      );

  // 概况（与 Web 的 ServerSummary 对应）：状态、最后上报、地址与 ASN、供应商、续费与到期、静音 / 维护
  Widget _summary(BuildContext context) {
    final s = _s;
    final colors = StatusColors.of(context);
    final c = appColors(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final stale = _error != null;
    final ip = IpInfo.of(s);
    final rows = _assetRows();
    final up = isLive(s) ? s.latest?.uptime : null;
    return Card(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 12, 14, 10),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            // 连不上面板时状态来自缓存，用灰色并注明“上次”，不把几天前的“在线”当成现在（修订第 211 条）
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
              decoration: BoxDecoration(
                color: (stale ? colors.muted : colors.status(s.status)).withValues(alpha: 0.12),
                borderRadius: BorderRadius.circular(99),
              ),
              child: Row(mainAxisSize: MainAxisSize.min, children: [
                Icon(Icons.circle, size: 8, color: stale ? colors.muted : colors.status(s.status)),
                const SizedBox(width: 5),
                Text('${stale ? '上次' : ''}${statusText[s.status] ?? s.status}',
                    style: TextStyle(fontSize: 12, color: stale ? colors.muted : colors.status(s.status), fontWeight: FontWeight.w600)),
              ]),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                [s.lastSeenAt > 0 ? '最后上报 ${fmtTime(s.lastSeenAt)}' : '尚未上报', if (up != null) '已运行 ${fmtDuration(up)}'].join(' · '),
                style: muted,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            if (s.group.isNotEmpty)
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                decoration: BoxDecoration(border: Border.all(color: c.border), borderRadius: BorderRadius.circular(6)),
                child: Text(s.group, style: muted.copyWith(fontSize: 11)),
              ),
          ]),
          if (rows.isNotEmpty || ip != null) const SizedBox(height: 8),
          for (final (k, v) in rows) ...[
            _kv(k, v, c,
                trailing: k == '到期' && _canRenew
                    ? TextButton(
                        onPressed: _busy ? null : _renew,
                        style: TextButton.styleFrom(visualDensity: VisualDensity.compact, padding: const EdgeInsets.symmetric(horizontal: 8)),
                        child: const Text('已续费'),
                      )
                    : null),
            // ASN 与位置紧跟在地址之后（设计 49）
            if (ip != null && (k == 'IPv4' || (k == 'IPv6' && s.ipv4.isEmpty)))
              _kv('归属', [if (ip.asn.isNotEmpty) ip.asn, if (ip.org.isNotEmpty) maskText(ip.org, widget.privacy), if (ip.place.isNotEmpty) ip.place].join(' · '), c),
          ],
          if (s.maintenance != null || s.muted != null) ...[
            const SizedBox(height: 6),
            for (final t in [
              if (s.maintenance != null) '维护中${_until(s.maintenance!)}：不产生告警',
              if (s.muted != null) '已静音${_until(s.muted!)}：告警不发送通知',
            ])
              Row(children: [
                Icon(Icons.info_outline, size: 14, color: colors.warn),
                const SizedBox(width: 4),
                Expanded(child: Text(t, style: TextStyle(fontSize: 12, color: colors.warn))),
              ]),
          ],
        ]),
      ),
    );
  }

  Widget _kv(String k, String v, AppColors c, {Widget? trailing}) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: Row(crossAxisAlignment: trailing == null ? CrossAxisAlignment.start : CrossAxisAlignment.center, children: [
          SizedBox(width: 52, child: Text(k, style: TextStyle(fontSize: 13, color: c.textMuted))),
          Expanded(child: SelectableText(v, style: const TextStyle(fontSize: 13))),
          if (trailing != null) trailing,
        ]),
      );

  // 已续费（修订第 224 条）：到期日按计费周期顺延一个周期；AK 允许记录订阅且授权全部节点的设备才显示
  bool get _canRenew {
    final sess = widget.api.session;
    return sess.allowSubscriptions && _s.expireDate.isNotEmpty && _period.containsKey(_s.billingPeriod) && _s.billingPeriod != 'one_time';
  }

  Future<void> _renew() async {
    final period = _period[_s.billingPeriod] ?? _s.billingPeriod;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('已续费'),
        content: Text('到期日 ${_s.expireDate} 顺延一个计费周期（$period）？'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
          TextButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('顺延')),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    // _do 先按“敏感操作前验证”验证，完成后提示并刷新（新的到期日显示在概况中）
    await _do(() => widget.api.renewServer(_s.id), '已续费：到期日顺延一个周期（$period）');
  }

  // 实时网速：下行 / 上行 + 开机以来累计
  Widget _netCard(BuildContext context) {
    final s = _s;
    final scheme = Theme.of(context).colorScheme;
    final muted = TextStyle(fontSize: 12, color: scheme.onSurfaceVariant);
    return Card(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 10, 14, 10),
        child: Row(children: [
          for (final (label, icon, v, total) in [
            ('下行', Icons.arrow_downward, s.latest!.rx, s.latest!.rxTotal),
            ('上行', Icons.arrow_upward, s.latest!.tx, s.latest!.txTotal),
          ])
            Expanded(
              child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                Row(children: [Icon(icon, size: 13, color: scheme.onSurfaceVariant), const SizedBox(width: 2), Text(label, style: muted)]),
                Text(fmtBytes(v, perSec: true), style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w600, fontFeatures: [FontFeature.tabularFigures()])),
                if (total > 0) Text('开机以来 ${fmtBytes(total)}', style: muted.copyWith(fontSize: 11)),
              ]),
            ),
        ]),
      ),
    );
  }

  // 解锁检测服务名（与 Web 的 UnlockCard 一致）
  static const _unlockNames = {
    'netflix': 'Netflix', 'disney': 'Disney+', 'youtube': 'YouTube Premium', 'prime': 'Prime Video', 'tiktok': 'TikTok',
    'chatgpt': 'ChatGPT', 'claude': 'Claude', 'google': 'Google 搜索', 'reddit': 'Reddit', 'steam': 'Steam 商店',
    'yahoo': 'Yahoo', 'truthsocial': 'Truth Social', 'telegram': 'Telegram',
    'abema': 'ABEMA', 'radiko': 'radiko', 'mytvsuper': 'myTV SUPER', 'viu': 'Viu', 'bilibili_hmt': 'bilibili 港澳台', 'bilibili_tw': 'bilibili 台湾',
    'kktv': 'KKTV', 'coupangplay': 'Coupang Play', 'hulu': 'Hulu', 'bbc_iplayer': 'BBC iPlayer',
  };
  static const _unlockStatus = {'yes': '可用', 'no': '不可用', 'partial': '部分可用', 'captcha': '需人机验证', 'failed': '检测失败'};

  Widget _unlockView(BuildContext context, UnlockResult u) {
    final c = appColors(context);
    final colors = StatusColors.of(context);
    final small = TextStyle(fontSize: 12, color: c.textMuted);
    // 超过 24 小时的结果（修订第 236 条）：说明多久以前、颜色变灰，不当作当前状态
    final stale = u.checkedAt > 0 && DateTime.now().millisecondsSinceEpoch ~/ 1000 - u.checkedAt > 24 * 3600;
    Color tone(String st) => stale
        ? colors.muted
        : switch (st) { 'yes' => colors.ok, 'partial' || 'captcha' => colors.warn, 'no' => colors.bad, _ => colors.muted };
    Widget item(UnlockItem it) => Container(
          padding: const EdgeInsets.fromLTRB(10, 7, 10, 7),
          decoration: BoxDecoration(border: Border.all(color: c.border), borderRadius: BorderRadius.circular(8)),
          child: Row(children: [
            Icon(Icons.circle, size: 8, color: tone(it.status)),
            const SizedBox(width: 6),
            Expanded(child: Text(_unlockNames[it.service] ?? it.service, style: const TextStyle(fontSize: 13), maxLines: 1, overflow: TextOverflow.ellipsis)),
            Text(
              it.status == 'yes' || it.status == 'partial'
                  ? '${it.region.isNotEmpty ? '${flagEmoji(it.region)} ${it.region}' : _unlockStatus[it.status]}${it.status == 'partial' ? ' 部分' : ''}'
                  : _unlockStatus[it.status] ?? it.status,
              style: TextStyle(fontSize: 12, color: tone(it.status), fontWeight: FontWeight.w600),
            ),
          ]),
        );
    final general = u.items.where((it) => it.local.isEmpty).toList();
    final local = u.items.where((it) => it.local.isNotEmpty).toList();
    Widget grid(List<UnlockItem> xs) => LayoutBuilder(builder: (context, box) {
          final w = (box.maxWidth - 8) / 2;
          return Wrap(spacing: 8, runSpacing: 8, children: [for (final it in xs) SizedBox(width: w, child: item(it))]);
        });
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if (u.items.isEmpty)
        Text(u.pending ? '检测中，约 1 分钟出结果。' : (u.supported ? '还没有检测过。在 Web 节点详情的“网络”页点“检测”。' : 'Agent 版本不支持，升级后可用。'), style: small)
      else ...[
        if (stale)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text('结果是 ${fmtAgo(u.checkedAt)}的，可能已经变化；下面的状态不代表现在。', style: TextStyle(fontSize: 12, color: colors.warn)),
          ),
        grid(general),
        if (local.isNotEmpty) ...[
          Padding(padding: const EdgeInsets.fromLTRB(2, 10, 0, 6), child: Text('当地服务 · ${flagEmoji(local.first.local)} ${local.first.local}', style: small)),
          grid(local),
        ],
        const SizedBox(height: 8),
        Text('${u.checkedAt > 0 ? '上次检测 ${fmtTime(u.checkedAt)}（${fmtAgo(u.checkedAt)}）· ' : ''}${u.pending ? '正在重新检测 · ' : ''}检测在 Web 中发起',
            style: small),
      ],
    ]);
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
    // 默认只显示需要关注的项（最多 3 条，严重的在前）；全部正常时一句话概括，点“详情”展开（修订第 256 条，与 Web 相同）
    const rank = {'bad': 0, 'warn': 1};
    final abnormal = h.items.where((it) => it.level == 'bad' || it.level == 'warn').toList()
      ..sort((a, b) => (rank[a.level] ?? 2) - (rank[b.level] ?? 2));
    final shown = _healthAll ? h.items : abnormal.take(3).toList();
    final okNames = [for (final it in h.items) if (it.level != 'bad' && it.level != 'warn') it.title];
    final muted = TextStyle(fontSize: 13, color: Theme.of(context).colorScheme.onSurfaceVariant);
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(h.status, style: TextStyle(fontWeight: FontWeight.w600, color: lc(h.level))),
      if (shown.isEmpty && okNames.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 6), child: Text('${okNames.join('、')}均正常', style: muted)),
      for (final it in shown)
        Padding(
          padding: const EdgeInsets.only(top: 8),
          child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Padding(padding: const EdgeInsets.only(top: 5), child: Icon(Icons.circle, size: 8, color: lc(it.level))),
            const SizedBox(width: 8),
            SizedBox(width: 36, child: Text(it.title, style: TextStyle(fontSize: 13, color: Theme.of(context).colorScheme.onSurfaceVariant))),
            Expanded(child: Text(it.text, style: const TextStyle(fontSize: 13))),
          ]),
        ),
      if (h.items.length > shown.length || _healthAll)
        TextButton(
          onPressed: () => setState(() => _healthAll = !_healthAll),
          style: TextButton.styleFrom(padding: EdgeInsets.zero, visualDensity: VisualDensity.compact),
          child: Text(_healthAll ? '收起' : '详情（${h.items.length} 项）'),
        ),
    ]);
  }

  static const _isps = [('ct', '电信'), ('cu', '联通'), ('cm', '移动')];

  /// 测速（设计 42.3.2、42.3.3）：每行一个地区、每列一个运营商；境外单独一张表。数值为最近 1 小时中位数与丢包率。
  /// 点一行看该地区三网（或该境外城市）的延迟曲线。App 只读：设置在 Web 的节点“编辑”中。
  Widget _latencyView(BuildContext context) {
    final v = _latency!;
    final muted = Theme.of(context).colorScheme.onSurfaceVariant;
    final small = TextStyle(fontSize: 12, color: muted);
    if (v.mode == 'off') return Text('这台节点已关闭测速（在 Web 的节点“编辑”中开启）', style: small);
    if (v.status == 'off') return Text('节点本机已关闭网络探测（/etc/vpsmon-agent/no-probe）', style: small);
    final hasData = v.targets.any((t) => t.lastAt > 0);
    if (!hasData) {
      return Text(v.status == 'unsupported' ? '当前 Agent 不支持测速，升级 Agent 后显示' : 'Agent 每 ${v.intervalS ~/ 60} 分钟测一轮，结果稍后出现',
          style: small);
    }
    final rows = <String, Map<String, LatencyTarget>>{};
    final intl = <LatencyTarget>[];
    for (final t in v.targets) {
      if (t.isp == 'intl') {
        intl.add(t);
      } else {
        rows.putIfAbsent(t.province, () => {})[t.isp] ??= t;
      }
    }
    final keys = [...rows.keys.map((p) => 'p:$p'), ...intl.map((t) => 'i:${t.id}')];
    final sel = keys.contains(_latencySel) ? _latencySel : (keys.isEmpty ? '' : keys.first);

    Widget cell(LatencyTarget? t) {
      if (t == null || t.loss == null) return Text('—', textAlign: TextAlign.right, style: small);
      final colors = StatusColors.of(context);
      final lat = t.latency;
      final c = lat == null
          ? colors.bad
          : (lat > 200 || t.loss! > 5)
              ? colors.warn
              : lat < 100
                  ? colors.ok
                  : null;
      return Opacity(
        opacity: t.enabled ? 1 : 0.45, // 这台节点已不再测：只是之前的数据
        child: Column(crossAxisAlignment: CrossAxisAlignment.end, children: [
          Text(lat == null ? '不可达' : fmtLatency(lat), style: TextStyle(fontSize: 13, color: c, fontFeatures: const [FontFeature.tabularFigures()])),
          Text(fmtPct(t.loss), style: small),
        ]),
      );
    }

    TableRow row(String key, List<Widget> cells) => TableRow(
          decoration: BoxDecoration(color: key == sel ? appColors(context).accent.withValues(alpha: 0.08) : null),
          children: [
            for (final c in cells)
              TableRowInkWell(
                onTap: () => setState(() => _latencySel = key),
                child: Padding(padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 4), child: c),
              ),
          ],
        );

    // 选中行的曲线：同一轮各运营商的时间相同，按第一条的时间对齐；缺的点为 NaN（断开）
    final picked = <(String, LatencyTarget)>[];
    if (sel.startsWith('i:')) {
      final t = intl.where((x) => 'i:${x.id}' == sel).firstOrNull;
      if (t != null) picked.add((t.note.isEmpty ? t.province : t.note, t));
    } else if (sel.isNotEmpty) {
      for (final (k, name) in _isps) {
        final t = rows[sel.substring(2)]?[k];
        if (t != null) picked.add((name, t));
      }
    }
    final times = <int>{for (final (_, t) in picked) ...(v.series[t.id] ?? []).map((p) => p.ts)}.toList()..sort();
    final series = [
      for (final (i, (name, t)) in picked.indexed)
        ChartSeries(name, [
          for (final ts in times)
            (v.series[t.id] ?? []).where((p) => p.ts == ts).firstOrNull?.latency ?? double.nan,
        ], seriesColor(context, i)),
    ];

    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if (rows.isNotEmpty)
        Table(
          columnWidths: const {0: FlexColumnWidth(1.2)},
          children: [
            TableRow(children: [
              Padding(padding: const EdgeInsets.all(4), child: Text('地区', style: small)),
              for (final (_, name) in _isps) Padding(padding: const EdgeInsets.all(4), child: Text(name, textAlign: TextAlign.right, style: small)),
            ]),
            for (final e in rows.entries)
              row('p:${e.key}', [Text(e.key, style: const TextStyle(fontSize: 13)), for (final (k, _) in _isps) cell(e.value[k])]),
          ],
        ),
      if (intl.isNotEmpty) ...[
        const SizedBox(height: 12),
        Table(
          columnWidths: const {0: FlexColumnWidth(1.2)},
          children: [
            TableRow(children: [
              Padding(padding: const EdgeInsets.all(4), child: Text('境外', style: small)),
              Padding(padding: const EdgeInsets.all(4), child: Text('提供商', style: small)),
              Padding(padding: const EdgeInsets.all(4), child: Text('延迟', textAlign: TextAlign.right, style: small)),
            ]),
            for (final t in intl)
              row('i:${t.id}', [Text(t.province, style: const TextStyle(fontSize: 13)), Text(t.note.isEmpty ? '—' : t.note, style: small), cell(t)]),
          ],
        ),
      ],
      // 选中行的目标明细（与 Web 悬停提示相同）：地址、失败原因、已不再测
      if (picked.isNotEmpty) ...[
        const SizedBox(height: 8),
        for (final (name, t) in picked)
          Padding(
            padding: const EdgeInsets.only(bottom: 2),
            child: Text.rich(TextSpan(style: small, children: [
              TextSpan(text: '$name  '),
              TextSpan(text: fmtProbeWhere(t.proto, t.port, t.address), style: const TextStyle(fontFamily: 'monospace')),
              if (t.latency == null && t.lastError.isNotEmpty)
                TextSpan(text: '  最近一轮：${fmtProbeError(t.lastError)}', style: TextStyle(color: StatusColors.of(context).bad)),
              if (!t.enabled) const TextSpan(text: '  已不再测，显示的是之前的数据'),
            ])),
          ),
      ],
      const SizedBox(height: 10),
      Wrap(spacing: 6, children: [
        for (final r in const ['24h', '7d'])
          ChoiceChip(
            label: Text(r.toUpperCase()),
            selected: _latencyRange == r,
            visualDensity: VisualDensity.compact,
            onSelected: (_) {
              setState(() => _latencyRange = r);
              _loadLatency();
            },
          ),
      ]),
      const SizedBox(height: 8),
      LineChart(times: times, series: series, formatY: (x) => fmtLatency(x)),
      const SizedBox(height: 6),
      Text('从这台 VPS 出发测量；${v.mode == 'custom' ? '自定义' : '跟随全局'} · 每 ${v.intervalS ~/ 60} 分钟', style: small),
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
      // 百分比按数据取整（10%～100%），低负载时也能看清起伏
      // 数据覆盖不到所选范围时注明（修订第 254 条）
      if (historyCoverage(times, _rangeSeconds[_range] ?? 3600) case final cov?)
        Padding(
          padding: const EdgeInsets.only(bottom: 6),
          child: Text('所选 ${_range.toUpperCase()} 内仅有约 ${cov < 3 * 86400 ? '${math.max(1, (cov / 3600).round())} 小时' : fmtDuration(cov)}的数据，缺失的时段不画线（不是 0）',
              style: TextStyle(fontSize: 12, color: appColors(context).textMuted)),
        ),
      LineChart(times: times, series: series, formatY: fmt, capMax: max, minMax: max == null ? null : 10),
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
        Text('已使用 ${fmtPctFine(pct)} · 剩余 ${fmtTrafficLeft((t.limit - t.used).clamp(0, t.limit), t.limit, t.unit)}', style: muted),
      ],
      const SizedBox(height: 4),
      Text('距离重置 $days 天（${t.cycleEnd}）', style: muted),
      if (f != null)
        Text('预计周期总量 ${fmtTraffic(f.total, t.unit)}${f.over ? '，将超出额度' : ''}',
            style: f.over ? muted.copyWith(color: colors.bad) : muted),
    ]);
  }

  // 资产信息（设计 1.2.3）；隐私模式下 IP、价格、供应商打码（1.5.12）
  List<(String, String)> _assetRows() {
    final s = _s;
    final p = widget.privacy;
    final price = s.priceCents > 0 ? '${s.currency} ${(s.priceCents / 100).toStringAsFixed(2)}${s.billingPeriod.isNotEmpty ? ' · ${_period[s.billingPeriod] ?? s.billingPeriod}' : ''}' : '';
    return [
      if (s.ipv4.isNotEmpty) ('IPv4', p ? maskIP(s.ipv4) : s.ipv4),
      if (s.ipv6.isNotEmpty) ('IPv6', p ? maskIP(s.ipv6) : s.ipv6),
      if (s.provider.isNotEmpty) ('供应商', maskText(s.provider, p)),
      if (s.plan.isNotEmpty) ('套餐', s.plan),
      if (s.region.isNotEmpty) ('地区', s.region),
      if (price.isNotEmpty) ('续费', maskText(price, p)),
      if (fmtExpiry(s.expireDate, s.billingPeriod) case final e?)
        ('到期', s.expireDate.isEmpty ? e.text : (e.text == s.expireDate ? s.expireDate : '${s.expireDate}（${e.text}）')),
      if (s.note.isNotEmpty) ('备注', s.note),
    ];
  }

  // 关联到这台节点的云实例（设计 44.5）
  List<(CloudInstanceBrief, CloudAccountBrief?)> _cloudInstances() {
    final c = widget.cloud;
    if (c == null) return const [];
    return [
      for (final i in c.instances)
        if (i.serverId == _s.id) (i, c.accounts.where((a) => a.id == i.accountId).firstOrNull),
    ];
  }

  /// 云实例：账户、地区与类型、规格、状态、到期、流量包与账户本月费用；隐私模式下账户名打码
  /// 容器（设计 33.4，修订第 164 条）：只读；名称在 Web 中设置，没有名称时显示容器 ID。
  Widget _containersView(BuildContext context, List<ContainerInfo> cs, List<ContainerMissing> missing) {
    final ac = appColors(context);
    final muted = TextStyle(fontSize: 12, color: ac.textMuted);
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      // 已停止的容器（修订第 166 条）：“不再提醒”在 Web 中操作
      for (final m in missing)
        Padding(
          padding: const EdgeInsets.only(bottom: 4),
          child: Row(children: [
            Icon(Icons.stop_circle_outlined, size: 16, color: ac.warn),
            const SizedBox(width: 6),
            Expanded(child: Text('已停止：${m.label}${m.missing > 1 ? ' ×${m.missing}' : ''}', style: TextStyle(color: ac.warn))),
            Text('${fmtTime(m.since)} 起', style: muted),
          ]),
        ),
      for (final c in cs)
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 6),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Row(children: [
              Expanded(child: Text(c.label, style: const TextStyle(fontWeight: FontWeight.w600), overflow: TextOverflow.ellipsis)),
              Text('CPU ${fmtPct(c.cpu)}', style: const TextStyle(fontFeatures: [FontFeature.tabularFigures()])),
            ]),
            const SizedBox(height: 2),
            Row(children: [
              Expanded(
                child: Text(
                  [c.id, if (c.name.isNotEmpty && c.command.isNotEmpty) c.command, c.runtime == 'podman' ? 'Podman' : 'Docker',
                    if (c.pids > 0) '${c.pids} 个进程', if (c.restarts24h > 0) '24 小时内重启 ${c.restarts24h} 次'].join(' · '),
                  style: muted,
                ),
              ),
              Text(c.memLimit > 0 ? '${fmtBytes(c.memory)} / ${fmtBytes(c.memLimit)}' : fmtBytes(c.memory), style: muted),
            ]),
            if (c.memLimit > 0)
              Padding(
                padding: const EdgeInsets.only(top: 4),
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(2),
                  child: LinearProgressIndicator(
                    value: (c.memory / c.memLimit).clamp(0.0, 1.0),
                    minHeight: 4,
                    backgroundColor: ac.track,
                    color: c.memory >= c.memLimit * 0.95 ? ac.bad : (c.memory >= c.memLimit * 0.8 ? ac.warn : ac.ok),
                  ),
                ),
              ),
          ]),
        ),
      Text('从 cgroup 读取，不含已停止的容器；容器名在 Web 节点详情中设置。', style: muted),
    ]);
  }

  Widget _cloudView(BuildContext context) {
    final c = appColors(context);
    final muted = TextStyle(fontSize: 13, color: c.textMuted);
    final rows = _cloudInstances();
    Widget row(String k, Widget v) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 3),
          child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [SizedBox(width: 56, child: Text(k, style: muted)), Expanded(child: v)]),
        );
    const t = TextStyle(fontSize: 13);
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      for (final (idx, (i, a)) in rows.indexed) ...[
        if (idx > 0) const Divider(height: 20),
        row('账户', Text(a == null ? dash : '${a.providerName} · ${maskText(a.name, widget.privacy)}', style: t)),
        row('实例', Text([if (i.name.isNotEmpty) i.name, cloudKindNames[i.kind] ?? i.kind, if (i.region.isNotEmpty) i.region].join(' · '), style: t)),
        if (i.plan.isNotEmpty) row('规格', Text(i.plan, style: t)),
        if (i.state.isNotEmpty && i.state != '-') row('状态', Text(i.state, style: t)),
        if (i.expireAt > 0) row('到期', _expiryText(i.expireAt, c)),
        if (i.trafficLimit > 0)
          row('流量包', Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text('${fmtBytes(i.trafficUsed)} / ${fmtBytes(i.trafficLimit)}', style: t),
            const SizedBox(height: 4),
            ClipRRect(
              borderRadius: BorderRadius.circular(2),
              child: LinearProgressIndicator(
                value: (i.trafficUsed / i.trafficLimit).clamp(0.0, 1.0),
                minHeight: 4,
                backgroundColor: c.track,
                color: i.trafficUsed >= i.trafficLimit * 0.95 ? c.bad : (i.trafficUsed >= i.trafficLimit * 0.8 ? c.warn : c.ok),
              ),
            ),
          ])),
        if (a?.cost != null) row('本月', Text('账户已产生 ${fmtMoney(a!.cost!.amountCents, a.cost!.currency)}', style: t)),
      ],
    ]);
  }

  Widget _expiryText(int unix, AppColors c) {
    final d = DateTime.fromMillisecondsSinceEpoch(unix * 1000);
    final date = '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';
    final e = fmtExpiry(date, '');
    final color = switch (e?.level) { 'bad' => c.bad, 'warn' => c.warn, _ => null };
    return Text(e == null || e.text == date ? date : '$date（${e.text}）', style: TextStyle(fontSize: 13, color: color));
  }

  static const _ispNames = {'ct': '电信', 'cu': '联通', 'cm': '移动'};
  static const _reachStatus = {'ok': '可达', 'unstable': '不稳定', 'port': '端口疑似被封', 'blocked': '疑似被墙'};

  /// 国内可达性（设计 42.2、42.3.1）：结论、各探针的状态与最近一轮、24 小时国内方向延迟曲线。结论都是“疑似”。
  Widget _reachView(BuildContext context) {
    final v = _reach!;
    final colors = StatusColors.of(context);
    final muted = Theme.of(context).colorScheme.onSurfaceVariant;
    final small = TextStyle(fontSize: 12, color: muted);
    Color? tone(String st) => switch (st) {
          'blocked' || 'partial' || 'port' => colors.bad,
          'unstable' => colors.warn,
          'ok' => colors.ok,
          _ => null,
        };
    // 曲线：每个探针一条，按第一个探针的时间对齐
    final first = v.probes.isEmpty ? const <LatencyPoint>[] : (v.series[v.probes.first.probeId] ?? const <LatencyPoint>[]);
    final times = [for (final p in first) p.ts];
    final series = [
      for (final (i, p) in v.probes.indexed)
        ChartSeries(_ispNames[p.isp] ?? p.name, [
          for (final ts in times) (v.series[p.probeId] ?? const <LatencyPoint>[]).where((x) => x.ts == ts).firstOrNull?.latency ?? double.nan,
        ], seriesColor(context, i)),
    ];
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(v.text, style: TextStyle(fontSize: 16, fontWeight: FontWeight.w600, color: tone(v.status))),
      const SizedBox(height: 8),
      for (final p in v.probes)
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Row(children: [
              Text(_ispNames[p.isp] ?? '探针', style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600)),
              const SizedBox(width: 6),
              Expanded(child: Text(p.name, style: small, overflow: TextOverflow.ellipsis)),
              Text('${_reachStatus[p.status] ?? p.status}${p.status == 'port' ? ' ${p.badPorts.join('、')}' : ''}',
                  style: TextStyle(fontSize: 13, color: tone(p.status))),
            ]),
            Wrap(spacing: 10, runSpacing: 2, children: [
              for (final c in p.checks)
                Text.rich(TextSpan(style: small, children: [
                  TextSpan(text: '${c.name} '),
                  TextSpan(
                    text: c.latency != null ? fmtLatency(c.latency) : (c.error == 'icmp_unavailable' ? dash : '失败'),
                    style: TextStyle(color: c.loss >= 100 && c.error != 'icmp_unavailable' ? colors.bad : null),
                  ),
                ])),
              if (p.status != 'ok') Text('自 ${fmtTime(p.since)}', style: small),
            ]),
          ]),
        ),
      if (times.length > 1) ...[
        const SizedBox(height: 8),
        LineChart(times: times, series: series, formatY: (x) => fmtLatency(x)),
      ],
      const SizedBox(height: 6),
      Text('结论为“疑似”：连续 3 轮异常才判定，可能是封锁、运营商故障或路由问题。国内探针在 Web“监测 → 测速设置”中设置。', style: small),
    ]);
  }

  // 与 web/src/format.ts 的 periodNames 一致
  static const _period = {'monthly': '月付', 'quarterly': '季付', 'semiannually': '半年付', 'annually': '年付',
      'biennially': '两年付', 'triennially': '三年付', 'one_time': '一次性'};

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
            const SizedBox(height: 6),
            ClipRRect(
              borderRadius: BorderRadius.circular(2),
              child: LinearProgressIndicator(
                  value: ((value ?? 0) / 100).clamp(0.0, 1.0), minHeight: 4, backgroundColor: appColors(context).track, color: c.level(level)),
            ),
            const SizedBox(height: 6),
            Text(sub, style: TextStyle(fontSize: 11, color: muted), maxLines: 2),
          ]),
        ),
      ),
    );
  }
}
