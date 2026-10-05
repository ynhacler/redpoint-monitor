// App 主框架（设计 1.5.18）：底部导航 首页 / 服务器 / 事件 / 我的。
// 节点数据在这里统一获取：每 10 秒刷新（1.5.9），先显示离线缓存，网络失败时保留最近数据并显示最后更新时间（1.5.10）。
import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../cache.dart';
import '../format.dart';
import '../metrics.dart';
import '../models.dart';
import '../prefs.dart';
import '../push.dart';
import '../sorting.dart';
import '../theme.dart';
import '../widgets/server_card.dart';
import 'events_page.dart';
import 'me_page.dart';
import 'server_detail_page.dart';

class AppShell extends StatefulWidget {
  const AppShell({super.key, required this.api, required this.cache, required this.prefs, required this.onUnpair,
      required this.onRevoked, this.pushSource = const NoPushTokenSource(), this.centers = const [], this.onSwitch, this.onAdd});
  final ApiClient api;
  final CacheStore cache;
  final PrefsStore prefs;
  final PushTokenSource pushSource;
  final Future<void> Function() onUnpair;
  final VoidCallback onRevoked;

  /// 多监控中心（设计 1.5.2）
  final List<Uri> centers;
  final ValueChanged<int>? onSwitch;
  final VoidCallback? onAdd;

  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> {
  int _tab = 0;
  List<ServerView> _items = [];
  DateTime? _updated;
  String? _error;
  Timer? _timer;
  Prefs _prefs = Prefs();
  PushState? _push;

  @override
  void initState() {
    super.initState();
    _start();
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
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
    } on DeviceRevoked {
      _revoked();
    } catch (e) {
      if (mounted) setState(() => _error = '$e'); // 保留上次的数据
    }
  }

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
      builder: (_) => ServerDetailPage(api: widget.api, cache: widget.cache, initial: s, onRevoked: widget.onRevoked,
          privacy: _prefs.privacy),
    ));
    _refresh(); // 详情页中可能静音或开启了维护
  }

  Widget _card(ServerView s) => ServerCard(s, onTap: () => _open(s), favorite: _prefs.favorites.contains(s.id),
      onFavorite: () => _toggleFavorite(s), privacy: _prefs.privacy);

  Widget _offlineBanner() => Card(
        color: Theme.of(context).colorScheme.errorContainer,
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Text('当前离线${_updated != null ? '\n最后更新：${fmtClock(_updated!)}' : ''}\n$_error'),
        ),
      );

  @override
  Widget build(BuildContext context) {
    final pages = [
      _OverviewTab(items: _items, prefs: _prefs, updated: _updated, error: _error, host: widget.api.session.server.host,
          onRefresh: _refresh, card: _card, offline: _offlineBanner),
      _ServersTab(items: _items, prefs: _prefs, error: _error, onRefresh: _refresh, card: _card, offline: _offlineBanner,
          onSort: (v) {
            setState(() => _prefs.sortBy = v);
            _savePrefs();
          }),
      EventsPage(api: widget.api, onRevoked: _revoked, onOpenServer: (id) {
        final s = _items.where((x) => x.id == id).firstOrNull;
        if (s != null) _open(s);
      }),
      MePage(api: widget.api, push: _push, privacy: _prefs.privacy, centers: widget.centers, onSwitch: (i) {
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
    final attention = _items.where((s) => issues(s).isNotEmpty).length;
    return Scaffold(
      body: IndexedStack(index: _tab, children: pages),
      bottomNavigationBar: NavigationBar(
        selectedIndex: _tab,
        onDestinationSelected: (i) => setState(() => _tab = i),
        destinations: [
          NavigationDestination(
            icon: Badge(isLabelVisible: attention > 0, label: Text('$attention'), child: const Icon(Icons.dashboard_outlined)),
            selectedIcon: const Icon(Icons.dashboard),
            label: '首页',
          ),
          const NavigationDestination(icon: Icon(Icons.dns_outlined), selectedIcon: Icon(Icons.dns), label: '服务器'),
          const NavigationDestination(icon: Icon(Icons.notifications_outlined), selectedIcon: Icon(Icons.notifications), label: '事件'),
          const NavigationDestination(icon: Icon(Icons.person_outline), selectedIcon: Icon(Icons.person), label: '我的'),
        ],
      ),
    );
  }
}

// ---- 首页：状态总览 + 需要关注 + 收藏（设计 1.5.18、13） ----

class _OverviewTab extends StatelessWidget {
  const _OverviewTab({required this.items, required this.prefs, required this.updated, required this.error, required this.host,
      required this.onRefresh, required this.card, required this.offline});
  final List<ServerView> items;
  final Prefs prefs;
  final DateTime? updated;
  final String? error;
  final String host;
  final Future<void> Function() onRefresh;
  final Widget Function(ServerView) card;
  final Widget Function() offline;

  @override
  Widget build(BuildContext context) {
    final colors = StatusColors.of(context);
    final online = items.where((s) => s.status == 'online').length;
    final offlineN = items.where((s) => s.status == 'offline').length;
    final attention = sortServers(items.where((s) => issues(s).isNotEmpty).toList(), SortBy.smart, prefs.favorites);
    final favs = sortServers(items.where((s) => prefs.favorites.contains(s.id) && issues(s).isEmpty).toList(), SortBy.name, prefs.favorites);
    final muted = TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant);
    Widget heading(String t) => Padding(padding: const EdgeInsets.fromLTRB(4, 16, 4, 6), child: Text(t, style: const TextStyle(fontWeight: FontWeight.w600)));
    return Scaffold(
      appBar: AppBar(title: const Text('我的服务器')),
      body: RefreshIndicator(
        onRefresh: onRefresh,
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 24), children: [
          Row(children: [
            _Count('在线', online, colors.ok),
            _Count('离线', offlineN, offlineN > 0 ? colors.bad : null),
            _Count('需要关注', attention.length, attention.isNotEmpty ? colors.warn : null), // 与 Web“需要关注”同一规则（设计 9）
          ]),
          if (error != null) offline(),
          if (items.isEmpty && updated != null) const Padding(padding: EdgeInsets.all(32), child: Center(child: Text('授权范围内还没有节点'))),
          if (attention.isNotEmpty) ...[heading('需要关注'), ...attention.map(card)],
          if (favs.isNotEmpty) ...[heading('收藏'), ...favs.map(card)],
          if (items.isNotEmpty && attention.isEmpty && favs.isEmpty)
            Padding(
              padding: const EdgeInsets.all(24),
              child: Text('全部正常。点节点卡片右上角的星标可以把常看的服务器放到首页。', style: muted, textAlign: TextAlign.center),
            ),
          if (updated != null && error == null)
            Padding(padding: const EdgeInsets.only(top: 12), child: Center(child: Text('$host · 更新于 ${fmtClock(updated!)}', style: muted))),
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
  Widget build(BuildContext context) => Expanded(
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

// ---- 服务器：全部节点 + 分组 + 搜索 + 排序（设计 1.5.6、1.5.16） ----

class _ServersTab extends StatefulWidget {
  const _ServersTab({required this.items, required this.prefs, required this.error, required this.onRefresh, required this.card,
      required this.offline, required this.onSort});
  final List<ServerView> items;
  final Prefs prefs;
  final String? error;
  final Future<void> Function() onRefresh;
  final Widget Function(ServerView) card;
  final Widget Function() offline;
  final ValueChanged<SortBy> onSort;

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

  @override
  Widget build(BuildContext context) {
    final groups = {for (final s in widget.items) if (s.group.isNotEmpty) s.group}.toList()..sort();
    if (_group.isNotEmpty && !groups.contains(_group)) _group = '';
    final shown = sortServers(
      widget.items.where((s) => (_group.isEmpty || s.group == _group) && matchesQuery(s, _search.text)).toList(),
      widget.prefs.sortBy,
      widget.prefs.favorites,
    );
    return Scaffold(
      appBar: AppBar(
        title: const Text('服务器'),
        actions: [
          PopupMenuButton<SortBy>(
            icon: const Icon(Icons.sort),
            tooltip: '排序',
            initialValue: widget.prefs.sortBy,
            onSelected: widget.onSort,
            itemBuilder: (_) => [for (final v in SortBy.values) PopupMenuItem(value: v, child: Text(sortNames[v]!))],
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: widget.onRefresh,
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 24), children: [
          TextField(
            controller: _search,
            decoration: InputDecoration(
              hintText: '搜索名称、IP、供应商、地区、备注',
              prefixIcon: const Icon(Icons.search),
              suffixIcon: _search.text.isEmpty
                  ? null
                  : IconButton(icon: const Icon(Icons.clear), onPressed: () => setState(_search.clear)),
              isDense: true,
              border: const OutlineInputBorder(),
            ),
            onChanged: (_) => setState(() {}),
          ),
          if (widget.error != null) widget.offline(),
          if (groups.isNotEmpty)
            SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Row(children: [
                for (final g in ['', ...groups])
                  Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: ChoiceChip(label: Text(g.isEmpty ? '全部' : g), selected: _group == g, onSelected: (_) => setState(() => _group = g)),
                  ),
              ]),
            ),
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 4, 4, 4),
            child: Text('${shown.length} 台 · 按${sortNames[widget.prefs.sortBy]}',
                style: TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant)),
          ),
          ...shown.map(widget.card),
        ]),
      ),
    );
  }
}
