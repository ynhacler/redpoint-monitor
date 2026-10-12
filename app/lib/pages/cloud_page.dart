// 云账户（设计 44.8，修订第 144 条）：各账户的同步状态、本月费用 / 预估 / 余额与预算、实例列表。
// 只读：添加、修改、立即同步都在 Web 中进行（约束 5）。状态说明与 Web 云账户页一致。
import 'package:flutter/material.dart';

import '../api.dart';
import '../format.dart';
import '../models.dart';
import '../theme.dart';
import '../tokens.dart';

class CloudPage extends StatefulWidget {
  const CloudPage({super.key, required this.api, required this.onRevoked, this.serverNames = const {}, this.onOpenServer, this.initial});
  final ApiClient api;
  final VoidCallback onRevoked;

  /// 节点 ID → 名称，用于显示实例关联的节点
  final Map<int, String> serverNames;
  final ValueChanged<int>? onOpenServer;

  /// 首页已读取的摘要：先显示，再刷新
  final CloudSummary? initial;

  @override
  State<CloudPage> createState() => _CloudPageState();
}

class _CloudPageState extends State<CloudPage> {
  CloudSummary? _data;
  String? _error;

  @override
  void initState() {
    super.initState();
    _data = widget.initial;
    _load();
  }

  Future<void> _load() async {
    try {
      final d = await widget.api.cloud();
      if (!mounted) return;
      setState(() {
        _data = d;
        _error = null;
      });
    } on DeviceRevoked {
      widget.onRevoked();
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.status == 404 ? '面板版本较旧，升级面板后可用' : e.message);
    } catch (e) {
      if (mounted) setState(() => _error = '$e');
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final muted = TextStyle(fontSize: 13, color: c.textMuted);
    final d = _data;
    return Scaffold(
      appBar: AppBar(title: const Text('云账户')),
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(padding: const EdgeInsets.fromLTRB(12, 8, 12, 24), children: [
          if (_error != null)
            Card(color: Theme.of(context).colorScheme.errorContainer, child: Padding(padding: const EdgeInsets.all(12), child: Text(_error!))),
          if (d == null && _error == null) const Padding(padding: EdgeInsets.all(32), child: Center(child: CircularProgressIndicator())),
          if (d != null && d.limited)
            Padding(
              padding: const EdgeInsets.all(24),
              child: Text('本机的授权范围不是“全部节点”。云账户覆盖整个面板，只对全部节点授权的设备显示。', style: muted, textAlign: TextAlign.center),
            )
          else if (d != null && d.accounts.isEmpty)
            Padding(
              padding: const EdgeInsets.all(24),
              child: Text('还没有云账户。在 Web 的“云账户”中添加只读凭证后，这里显示费用与实例。', style: muted, textAlign: TextAlign.center),
            )
          else if (d != null)
            for (final a in d.accounts) _AccountCard(a, [for (final i in d.instances) if (i.accountId == a.id) i], widget.serverNames, widget.onOpenServer),
          if (d != null && !d.limited && d.accounts.isNotEmpty)
            Padding(
              padding: const EdgeInsets.all(12),
              child: Text('只读：添加、修改凭证与立即同步请在 Web 的“云账户”中操作。', style: muted.copyWith(fontSize: 12), textAlign: TextAlign.center),
            ),
        ]),
      ),
    );
  }
}

/// 账户状态说明（与 Web 云账户页的 status 一致）
(String, String) cloudStatus(CloudAccountBrief a) {
  if (a.syncing) return ('', '同步中…');
  if (!a.enabled) return ('', '已停用');
  if (a.authFailed) return ('bad', '凭证失效或权限不足，已停止自动同步：${a.lastError}');
  if (a.lastError.isNotEmpty) return ('warn', '同步失败：${a.lastError}（${fmtTime(a.nextTryAt)} 重试）');
  if (a.lastWarning.isNotEmpty) return ('warn', '已同步 ${fmtTime(a.syncedAt)}，部分区域已跳过：${a.lastWarning}');
  return ('', a.syncedAt > 0 ? '已同步 ${fmtTime(a.syncedAt)}' : '等待首次同步');
}

class _AccountCard extends StatelessWidget {
  const _AccountCard(this.a, this.instances, this.serverNames, this.onOpenServer);
  final CloudAccountBrief a;
  final List<CloudInstanceBrief> instances;
  final Map<int, String> serverNames;
  final ValueChanged<int>? onOpenServer;

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    Color? tone(String l) => switch (l) { 'bad' => c.bad, 'warn' => c.warn, _ => null };
    final (lv, text) = cloudStatus(a);
    final small = TextStyle(fontSize: 12, color: c.textMuted);
    const num = TextStyle(fontSize: 16, fontWeight: FontWeight.w600, fontFeatures: [FontFeature.tabularFigures()]);
    final cost = a.cost;
    final pct = a.budgetPct;
    Widget money(String label, int? cents) => Expanded(
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text(label, style: small),
            Text(cents == null ? dash : fmtMoney(cents, cost!.currency), style: num),
          ]),
        );
    return Card(
      clipBehavior: Clip.antiAlias,
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(14, 12, 14, 8),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Row(children: [
              _ProviderBadge(a.providerName, c),
              const SizedBox(width: 8),
              Expanded(child: Text(a.name, style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16), overflow: TextOverflow.ellipsis)),
              Text('${a.instanceCount} 台实例', style: small),
            ]),
            const SizedBox(height: 6),
            Text(text, style: TextStyle(fontSize: 12, color: tone(lv) ?? c.textMuted)),
            if (cost != null) ...[
              const SizedBox(height: 10),
              Row(children: [
                money('本月已产生', cost.amountCents),
                money('本月预估', cost.forecastCents),
                if (cost.balanceCents != null) money('余额', cost.balanceCents),
              ]),
            ],
            if (pct != null) ...[
              const SizedBox(height: 10),
              Row(children: [
                Text('预算 ${fmtMoney(a.budgetCents, cost!.currency)}', style: small),
                const SizedBox(width: 8),
                Expanded(
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(2),
                    child: LinearProgressIndicator(
                      value: (pct / 100).clamp(0.0, 1.0),
                      minHeight: 4,
                      backgroundColor: c.track,
                      color: pct >= 100 ? c.bad : (pct >= 80 ? c.warn : c.ok),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Text(fmtPct(pct), style: TextStyle(fontSize: 12, color: pct >= 100 ? c.bad : (pct >= 80 ? c.warn : null))),
              ]),
            ],
          ]),
        ),
        if (instances.isNotEmpty)
          Theme(
            // 去掉 ExpansionTile 展开时自带的上下分隔线
            data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
            child: ExpansionTile(
              tilePadding: const EdgeInsets.symmetric(horizontal: 14),
              dense: true,
              title: Text('实例（${instances.length}）', style: const TextStyle(fontSize: 13)),
              children: [
                for (final i in instances) ...[Divider(height: 1, indent: 14, endIndent: 14, color: c.border), _InstanceRow(i, serverNames, onOpenServer)],
              ],
            ),
          ),
      ]),
    );
  }
}

class _ProviderBadge extends StatelessWidget {
  const _ProviderBadge(this.text, this.c);
  final String text;
  final AppColors c;

  @override
  Widget build(BuildContext context) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
        decoration: BoxDecoration(color: c.surface2, borderRadius: BorderRadius.circular(6), border: Border.all(color: c.border)),
        child: Text(text, style: TextStyle(fontSize: 11, color: c.textMuted)),
      );
}

class _InstanceRow extends StatelessWidget {
  const _InstanceRow(this.i, this.serverNames, this.onOpenServer);
  final CloudInstanceBrief i;
  final Map<int, String> serverNames;
  final ValueChanged<int>? onOpenServer;

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final small = TextStyle(fontSize: 12, color: c.textMuted, fontFeatures: const [FontFeature.tabularFigures()]);
    // 状态是服务商原样（Running / RUNNING / running…）：运行中绿，已停止或不适用（Oracle 出站流量为 “-”）灰，其他（启动中、过期等）黄
    final st = i.state.toLowerCase();
    final dot = const {'running', 'active'}.contains(st)
        ? c.ok
        : (const {'stopped', 'terminated', 'shutoff', '-', ''}.contains(st) ? c.mutedState : c.warn);
    String? expiry;
    Color? expColor;
    if (i.expireAt > 0) {
      final d = DateTime.fromMillisecondsSinceEpoch(i.expireAt * 1000);
      final e = fmtExpiry('${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}', '');
      if (e != null) {
        expiry = e.text;
        expColor = switch (e.level) { 'bad' => c.bad, 'warn' => c.warn, _ => null };
      }
    }
    final linked = i.serverId == null ? null : serverNames[i.serverId];
    final sub = [if (i.region.isNotEmpty) i.region, if (i.kind.isNotEmpty) i.kind, if (i.plan.isNotEmpty) i.plan];
    return InkWell(
      onTap: linked == null || onOpenServer == null ? null : () => onOpenServer!(i.serverId!),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        child: Row(children: [
          Tooltip(message: i.state, child: Icon(Icons.circle, size: 8, color: dot)),
          const SizedBox(width: 8),
          Expanded(
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(i.name.isEmpty ? '（未命名）' : i.name, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14)),
              Text(sub.join(' · '), style: small, overflow: TextOverflow.ellipsis),
              if (linked != null)
                Row(children: [
                  Icon(Icons.link, size: 12, color: c.accent),
                  const SizedBox(width: 2),
                  Flexible(child: Text(linked, style: small.copyWith(color: c.accent), overflow: TextOverflow.ellipsis)),
                ]),
            ]),
          ),
          const SizedBox(width: 8),
          Column(crossAxisAlignment: CrossAxisAlignment.end, children: [
            if (expiry != null) Text(expiry, style: small.copyWith(color: expColor)),
            if (i.trafficLimit > 0)
              Text('流量 ${fmtBytes(i.trafficUsed)} / ${fmtBytes(i.trafficLimit)}', style: small)
            else if (i.trafficUsed > 0)
              Text('流量 ${fmtBytes(i.trafficUsed)}', style: small),
          ]),
        ]),
      ),
    );
  }
}
