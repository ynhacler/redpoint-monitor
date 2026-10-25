// App 主框架（设计 1.5.18，修订第 213、216、218、219 条）：底部导航 服务器 / 网络（本机 IP、连通性测试）/ 工具 / 设置。
// 原“首页”的概况并入“服务器”顶部；事件从“服务器”右上角进入；“工具”中分页放订阅与云平台。
// 节点数据在这里统一获取：每 10 秒刷新（1.5.9），先显示离线缓存，网络失败时保留最近数据并显示最后更新时间（1.5.10）。
import 'dart:async';

import 'package:flutter/material.dart';

import '../widgets/stale_banner.dart';
import '../errors.dart';
import '../api.dart';
import '../cache.dart';
import '../centers.dart';
import '../format.dart';
import '../home_widget.dart';
import '../metrics.dart';
import '../models.dart';
import '../prefs.dart';
import '../push.dart';
import '../sorting.dart';
import '../theme.dart';
import '../widgets/fleet.dart';
import '../widgets/server_card.dart';
import 'network_page.dart';
import 'events_page.dart';
import 'latency_compare_page.dart';
import 'reorder_page.dart';
import 'me_page.dart';
import 'server_detail_page.dart';
import 'tools_page.dart';

/// App 主框架：底部导航与节点数据的统一刷新（设计 1.5.18）
class AppShell extends StatefulWidget {
  const AppShell({super.key, required this.api, required this.cache, required this.prefs, required this.onUnpair,
      required this.onRevoked, this.pushSource = const NoPushTokenSource(), this.centers = const [], this.onSwitch, this.onAdd,
      this.onCentersChanged});
  final ApiClient api;
  final CacheStore cache;
  final PrefsStore prefs;
  final PushTokenSource pushSource;
  final Future<void> Function() onUnpair;
  final VoidCallback onRevoked;

  /// 多监控中心（设计 1.5.2）
  final List<CenterHandle> centers;
  final ValueChanged<int>? onSwitch;
  final VoidCallback? onAdd;

  /// 其他中心的授权失效（已从列表移除）时通知外层刷新
  final VoidCallback? onCentersChanged;

  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> {
  int _tab = 0;
  final _visited = <int>{0};
  List<ServerView> _items = [];
  DateTime? _updated;
  String? _error;
  Timer? _timer;
  Prefs _prefs = Prefs();
  PushState? _push;

  // 其他监控中心的汇总（聚合视图，设计 1.5.2）：每分钟刷新一次
  final Map<String, CenterSummary> _others = {};
  Timer? _othersTimer;

  // 云账户摘要（设计 44.8）：首页一行，每 5 分钟刷新；旧版面板或读取失败时不显示
  CloudSummary? _cloud;
  Timer? _cloudTimer;

  // 桌面小组件快照（设计 1.5.4）
  final _widget = WidgetPublisher();

  void _publishWidget() {
    if (_updated != null) _widget.publish(widgetSnapshot(_items, _others.values, _updated!));
  }

  @override
  void initState() {
    super.initState();
    _start();
  }

  @override
  void dispose() {
    _timer?.cancel();
    _othersTimer?.cancel();
    _cloudTimer?.cancel();
    super.dispose();
  }

  Future<void> _refreshOthers() async {
    for (final c in widget.centers) {
      if (identical(c.api, widget.api)) continue;
      try {
        final s = CenterSummary.of(await c.api.servers());
        if (mounted) setState(() => _others[c.key] = s);
      } on DeviceRevoked {
        widget.onCentersChanged?.call(); // 该中心已从列表移除
      } catch (e) {
        final last = _others[c.key] ?? const CenterSummary(total: 0, online: 0, offline: 0, attention: 0);
        if (mounted) setState(() => _others[c.key] = last.withError(describeError(e)));
      }
    }
    _publishWidget();
  }

  Future<void> _start() async {
    final prefs = await widget.prefs.load();
    final c = await widget.cache.servers(); // 先显示缓存，再联网刷新
    if (!mounted) return;
    setState(() {
      _prefs = prefs;
      if (c != null && _updated == null) {
        _items = c.value;
        _updated = c.at;
      }
    });
    await _refresh();
    _timer = Timer.periodic(const Duration(seconds: 10), (_) => _refresh());
    _syncPush();
    _loadCloud();
    _cloudTimer = Timer.periodic(const Duration(minutes: 5), (_) => _loadCloud());
    if (widget.centers.length > 1) {
      _refreshOthers();
      _othersTimer = Timer.periodic(const Duration(minutes: 1), (_) => _refreshOthers());
    }
  }

  Future<void> _refresh() async {
    try {
      final items = await widget.api.servers();
      final now = DateTime.now();
      await widget.cache.saveServers(items, now);
      if (!mounted) return;
      setState(() {
        _items = items;
        _error = null;
        _updated = now;
      });
      _publishWidget();
    } on DeviceRevoked {
      _revoked();
    } catch (e) {
      if (mounted) setState(() => _error = describeError(e)); // 保留上次的数据
    }
  }

  Future<void> _loadCloud() async {
    try {
      final c = await widget.api.cloud();
      if (mounted) setState(() => _cloud = c);
    } on DeviceRevoked {
      _revoked();
    } catch (_) {
      // 旧版面板（404）或网络失败：首页不显示云账户行，保留上次的结果
    }
  }

  void _switchCenter(int i) {
    _timer?.cancel();
    _othersTimer?.cancel();
    widget.onSwitch?.call(i);
  }

  // 事件（设计 1.5.17）：从“服务器”右上角的铃铛进入
  void _openEvents() => Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => EventsPage(api: widget.api, onRevoked: _revoked, centers: widget.centers, onCentersChanged: widget.onCentersChanged,
            onSwitch: (i) {
          Navigator.of(context).popUntil((r) => r.isFirst);
          _switchCenter(i);
        }, onOpenServer: (id) {
          final s = _items.where((x) => x.id == id).firstOrNull;
          if (s != null) _open(s);
        }),
      ));

  Future<void> _syncPush() async {
    try {
      final st = await syncPush(widget.api, widget.pushSource);
      if (mounted) setState(() => _push = st);
    } on DeviceRevoked {
      _revoked();
    } catch (_) {
      // 网络失败：下次启动再试
    }
  }

  void _revoked() {
    _timer?.cancel();
    widget.onRevoked();
  }

  Future<void> _savePrefs() => widget.prefs.save(_prefs);

  void _toggleFavorite(ServerView s) {
    setState(() => _prefs.favorites.contains(s.id) ? _prefs.favorites.remove(s.id) : _prefs.favorites.add(s.id));
    _savePrefs();
  }

  Future<void> _open(ServerView s) async {
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => ServerDetailPage(api: widget.api, cache: widget.cache, initial: s, onRevoked: widget.onRevoked, cloud: _cloud,
          privacy: _prefs.privacy),
    ));
    _refresh(); // 详情页中可能静音或开启了维护
  }

  // 调整顺序（修订第 149 条）：从当前显示的顺序开始拖动，完成后切换为自定义顺序
  void _reorder() => Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => ReorderPage(
          items: sortServers(_items, _prefs.sortBy, _prefs.favorites, order: _prefs.order),
          onSave: (ids) {
            setState(() {
              _prefs.order = ids;
              _prefs.sortBy = SortBy.custom;
            });
            _savePrefs();
          },
        ),
      ));

  // 测速对比（设计 42.3.5）：点节点打开详情
  void _openCompare() => Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => LatencyComparePage(api: widget.api, onRevoked: _revoked, onOpenServer: (id) {
          final s = _items.where((x) => x.id == id).firstOrNull;
          if (s != null) _open(s);
        }),
      ));

  Widget _card(ServerView s) => ServerCard(s, onTap: () => _open(s), favorite: _prefs.favorites.contains(s.id),
      onFavorite: () => _toggleFavorite(s), privacy: _prefs.privacy, stale: _error != null);

  Widget _offlineBanner() => StaleBanner(error: _error!, updated: _updated, onRetry: _refresh);

  @override
  Widget build(BuildContext context) {
    final attention = _items.where((s) => issues(s).isNotEmpty).length;
    final pages = [
      _ServersTab(items: _items, prefs: _prefs, updated: _updated, error: _error, host: centerLabel(widget.api.session.server),
          onRefresh: () async {
            await Future.wait([_refresh(), _refreshOthers()]);
          },
          card: _card, offline: _offlineBanner, onCompare: _openCompare, onEvents: _openEvents,
          attention: attention, onReorder: _reorder,
          centers: [
            if (widget.centers.length > 1)
              for (final (i, c) in widget.centers.indexed)
                (c, identical(c.api, widget.api) ? CenterSummary.of(_items) : _others[c.key], identical(c.api, widget.api), i),
          ],
          onSwitch: _switchCenter,
          onSort: (v) {
            // 第一次选“自定义顺序”且本机与面板都还没有顺序：直接打开调整页
            if (v == SortBy.custom && _prefs.order.isEmpty && !_items.any((s) => s.sortOrder > 0)) return _reorder();
            setState(() => _prefs.sortBy = v);
            _savePrefs();
          }),
      // 第一次切到这个标签时才创建：打开 App 不自动查询 IP 或开始测试
      _visited.contains(1) ? NetworkPage(privacy: _prefs.privacy) : const SizedBox.shrink(),
      ToolsPage(api: widget.api, onRevoked: _revoked, privacy: _prefs.privacy, cloud: _cloud,
          subGrouped: _prefs.subGrouped, onSubGrouped: (v) {
        setState(() => _prefs.subGrouped = v);
        _savePrefs();
      },
          serverNames: {for (final s in _items) s.id: s.name}, onOpenServer: (id) {
        final s = _items.where((x) => x.id == id).firstOrNull;
        if (s != null) _open(s);
      }),
      MePage(api: widget.api, push: _push, privacy: _prefs.privacy, centers: [for (final c in widget.centers) c.server], onSwitch: (i) {
        _timer?.cancel();
        widget.onSwitch?.call(i);
      }, onAdd: widget.onAdd, onPrivacy: (v) {
        setState(() => _prefs.privacy = v);
        _savePrefs();
      }, onUnpair: () async {
        _timer?.cancel();
        await widget.onUnpair();
      }),
    ];
    return Scaffold(
      body: IndexedStack(index: _tab, children: pages),
      bottomNavigationBar: NavigationBar(
        selectedIndex: _tab,
        height: 64,
        onDestinationSelected: (i) => setState(() {
          _tab = i;
          _visited.add(i);
        }),
        destinations: [
          NavigationDestination(
            icon: Badge(isLabelVisible: attention > 0, label: Text('$attention'), child: const Icon(Icons.dns_outlined)),
            selectedIcon: Badge(isLabelVisible: attention > 0, label: Text('$attention'), child: const Icon(Icons.dns)),
            label: '服务器',
          ),
          const NavigationDestination(icon: Icon(Icons.public_outlined), selectedIcon: Icon(Icons.public), label: '网络'),
          const NavigationDestination(icon: Icon(Icons.handyman_outlined), selectedIcon: Icon(Icons.handyman), label: '工具'),
          const NavigationDestination(icon: Icon(Icons.settings_outlined), selectedIcon: Icon(Icons.settings), label: '设置'),
        ],
      ),
    );
  }
}

// ---- 服务器：概况 + 全部节点 + 分组 + 搜索 + 排序（设计 1.5.6、1.5.16、1.5.18） ----

class _ServersTab extends StatefulWidget {
  const _ServersTab({required this.items, required this.prefs, required this.updated, required this.error, required this.host,
      required this.onRefresh, required this.card, required this.offline, required this.onSort, required this.onCompare,
      required this.onReorder, required this.onEvents, required this.attention,
      this.centers = const [], this.onSwitch});
  final List<ServerView> items;
  final Prefs prefs;
  final DateTime? updated;
  final String? error;
  final String host;
  final Future<void> Function() onRefresh;
  final Widget Function(ServerView) card;
  final Widget Function() offline;
  final ValueChanged<SortBy> onSort;
  final VoidCallback onCompare;
  final VoidCallback onReorder;
  final VoidCallback onEvents;

  /// 需要关注的节点数（铃铛上的角标）
  final int attention;

  /// 多个监控中心时的聚合视图：（中心，汇总，是否当前，下标）
  final List<(CenterHandle, CenterSummary?, bool, int)> centers;
  final ValueChanged<int>? onSwitch;

  @override
  State<_ServersTab> createState() => _ServersTabState();
}

class _ServersTabState extends State<_ServersTab> {
  final _search = TextEditingController();
  String _group = '';

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  Widget _aggregate(BuildContext context) {
    final colors = StatusColors.of(context);
    final muted = TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant);
    final total = CenterSummary.sum(widget.centers.map((c) => c.$2).whereType<CenterSummary>());
    return Card(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 10, 14, 2),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            const Text('全部监控中心', style: TextStyle(fontWeight: FontWeight.w600)),
            const Spacer(),
            Text('${total.total} 台 · 在线 ${total.online} · 离线 ${total.offline}', style: muted),
          ]),
          for (final (c, sum, current, i) in widget.centers)
            ListTile(
              contentPadding: EdgeInsets.zero,
              dense: true,
              visualDensity: VisualDensity.compact,
              leading: Icon(current ? Icons.radio_button_checked : Icons.radio_button_unchecked,
                  color: current ? Theme.of(context).colorScheme.primary : null, size: 20),
              title: Text(centerLabel(c.server), overflow: TextOverflow.ellipsis),
              subtitle: Text(sum == null ? '读取中…' : '${sum.total} 台 · 在线 ${sum.online}${sum.error != null ? ' · 暂时无法连接' : ''}'),
              trailing: sum == null
                  ? null
                  : Row(mainAxisSize: MainAxisSize.min, children: [
                      if (sum.offline > 0) Text('离线 ${sum.offline}  ', style: TextStyle(color: colors.bad, fontSize: 12)),
                      if (sum.attention > 0) Text('关注 ${sum.attention}', style: TextStyle(color: colors.warn, fontSize: 12)),
                    ]),
              onTap: current || widget.onSwitch == null ? null : () => widget.onSwitch!(i),
            ),
        ]),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final colors = StatusColors.of(context);
    final c = appColors(context);
    final items = widget.items;
    final groups = {for (final s in items) if (s.group.isNotEmpty) s.group}.toList()..sort();
    if (_group.isNotEmpty && !groups.contains(_group)) _group = '';
    final shown = sortServers(
      items.where((s) => (_group.isEmpty || s.group == _group) && matchesQuery(s, _search.text)).toList(),
      widget.prefs.sortBy,
      widget.prefs.favorites,
      order: widget.prefs.order,
    );
    final online = items.where((s) => s.status == 'online').length;
    final offlineN = items.where((s) => s.status == 'offline').length;
    final heavy = items.where((s) => (trafficPct(s) ?? 0) >= 80).length;
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    return Scaffold(
      appBar: AppBar(
        title: const Text('服务器'),
        actions: [
          IconButton(
            tooltip: '事件',
            onPressed: widget.onEvents,
            icon: Badge(isLabelVisible: widget.attention > 0, label: Text('${widget.attention}'), child: const Icon(Icons.notifications_outlined)),
          ),
          // 排序方式、“调整顺序…”（拖动排列，保存为自定义顺序）与测速对比
          PopupMenuButton<Object>(
            icon: const Icon(Icons.more_vert),
            tooltip: '更多',
            onSelected: (v) => v is SortBy ? widget.onSort(v) : (v == 'compare' ? widget.onCompare() : widget.onReorder()),
            itemBuilder: (_) => [
              for (final v in SortBy.values)
                CheckedPopupMenuItem(value: v, checked: widget.prefs.sortBy == v, child: Text('按${sortNames[v]!}')),
              const PopupMenuDivider(),
              const PopupMenuItem(value: 'reorder', child: Row(children: [Icon(Icons.drag_handle, size: 18), SizedBox(width: 8), Text('调整顺序…')])),
              const PopupMenuItem(value: 'compare', child: Row(children: [Icon(Icons.speed, size: 18), SizedBox(width: 8), Text('三网延迟对比')])),
            ],
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: widget.onRefresh,
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 24), children: [
          if (widget.centers.length > 1) _aggregate(context),
          // 与 Web 仪表板的统计一致：全部 / 在线 / 离线 / 需要关注 / 流量≥80%，数字只在非零时着色
          Row(children: [
            _Count('全部', items.where((s) => s.status != 'pending').length, null),
            _Count('在线', online, online > 0 ? colors.ok : null),
            _Count('离线', offlineN, offlineN > 0 ? colors.bad : null),
            _Count('需关注', widget.attention, widget.attention > 0 ? colors.warn : null),
            _Count('流量≥80%', heavy, heavy > 0 ? colors.warn : null),
          ]),
          if (widget.error != null) widget.offline(),
          if (items.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 4), child: FleetSummaryCard(items: items, allOk: widget.attention == 0)),
          const SizedBox(height: 8),
          TextField(
            controller: _search,
            decoration: InputDecoration(
              hintText: '搜索名称、IP、供应商、地区、备注',
              prefixIcon: const Icon(Icons.search),
              suffixIcon: _search.text.isEmpty ? null : IconButton(icon: const Icon(Icons.clear), onPressed: () => setState(_search.clear)),
              isDense: true,
              filled: true,
              fillColor: c.surface,
              contentPadding: const EdgeInsets.symmetric(vertical: 10),
              border: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide(color: c.border)),
              enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide(color: c.border)),
            ),
            onChanged: (_) => setState(() {}),
          ),
          if (groups.isNotEmpty)
            SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.only(top: 8),
              child: Row(children: [
                for (final g in ['', ...groups])
                  Padding(
                    padding: const EdgeInsets.only(right: 6),
                    child: ChoiceChip(
                      label: Text(g.isEmpty ? '全部' : g),
                      selected: _group == g,
                      visualDensity: VisualDensity.compact,
                      onSelected: (_) => setState(() => _group = g),
                    ),
                  ),
              ]),
            ),
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 8, 4, 4),
            child: Row(children: [
              Expanded(child: Text('${shown.length} 台 · 按${sortNames[widget.prefs.sortBy]}', style: muted)),
              if (widget.prefs.sortBy == SortBy.custom)
                TextButton.icon(
                  onPressed: widget.onReorder,
                  icon: const Icon(Icons.drag_handle, size: 16),
                  label: const Text('调整顺序'),
                  style: TextButton.styleFrom(visualDensity: VisualDensity.compact, textStyle: const TextStyle(fontSize: 12)),
                ),
            ]),
          ),
          if (items.isEmpty && widget.updated != null) const Padding(padding: EdgeInsets.all(32), child: Center(child: Text('授权范围内还没有节点'))),
          ...shown.map(widget.card),
          if (widget.updated != null && widget.error == null)
            Padding(padding: const EdgeInsets.only(top: 8), child: Center(child: Text('${widget.host} · 更新于 ${fmtClock(widget.updated!)}', style: muted))),
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
  // 与 Web 的统计格一致：上方小号说明，下方数字
  Widget build(BuildContext context) => Expanded(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 3),
          child: Card(
            margin: const EdgeInsets.symmetric(vertical: 4),
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 6),
              child: Column(children: [
                Text(label, style: TextStyle(fontSize: 11, color: Theme.of(context).colorScheme.onSurfaceVariant), maxLines: 1),
                Text('$value', style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600, color: color,
                    fontFeatures: const [FontFeature.tabularFigures()])),
              ]),
            ),
          ),
        ),
      );
}
