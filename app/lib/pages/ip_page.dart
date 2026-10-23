// 本机 IP 查询（修订第 219 条）：上面是本机的 IPv4 / IPv6 出口与位置、运营商、ASN，下面逐个列出国内、国际查询服务看到的结果。
// 国内与国际看到的 IP 不同，说明分流生效（国内直连、国外走代理）。查询在手机上直接进行，不经过面板，结果不保存。
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../errors.dart';
import '../format.dart';
import '../ip_lookup.dart';
import '../privacy.dart';
import '../theme.dart';
import '../tokens.dart';

/// 本机 IP 查询页
class IpPage extends StatefulWidget {
  /// 创建页面；[fetch] 可在测试中替换
  const IpPage({super.key, this.privacy = false, this.fetch = httpFetch});

  /// 隐私模式：IP 打码
  final bool privacy;

  /// 请求实现
  final Fetch fetch;

  @override
  State<IpPage> createState() => _IpPageState();
}

class _IpPageState extends State<IpPage> {
  final _results = <String, IpResult>{};
  final _errors = <String, String>{};
  IpResult? _v6;
  bool _v6Done = false;
  bool _running = false;

  @override
  void initState() {
    super.initState();
    _run();
  }

  Future<void> _run() async {
    if (_running) return;
    setState(() {
      _running = true;
      _results.clear();
      _errors.clear();
      _v6 = null;
      _v6Done = false;
    });
    await Future.wait([
      for (final s in ipSources)
        () async {
          try {
            final r = s.parse(await widget.fetch(s.url, s.headers));
            if (mounted) setState(() => _results[s.id] = r);
          } catch (e) {
            if (mounted) setState(() => _errors[s.id] = e is FormatException ? '返回的数据无法识别' : describeError(e));
          }
        }(),
      () async {
        final r = await lookupIpv6(widget.fetch);
        if (mounted) {
          setState(() {
            _v6 = r;
            _v6Done = true;
          });
        }
      }(),
    ]);
    if (mounted) setState(() => _running = false);
  }

  String _mask(String ip) => widget.privacy ? maskIP(ip) : ip;

  // 主卡片的 IPv4：优先国际服务（Cloudflare → IP.SB → IPinfo），都失败时用国内服务
  IpResult? get _v4 {
    for (final id in ['cloudflare', 'ipsb', 'ipinfo', 'ipip', 'bilibili']) {
      final r = _results[id];
      if (r != null && !r.ip.contains(':')) return r;
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final domestic = {for (final s in ipSources.where((s) => s.domestic)) if (_results[s.id] != null) _results[s.id]!.ip};
    final intl = {for (final s in ipSources.where((s) => !s.domestic)) if (_results[s.id] != null) _results[s.id]!.ip};
    final split = domestic.isNotEmpty && intl.isNotEmpty && domestic.intersection(intl).isEmpty;
    return RefreshIndicator(
      onRefresh: _run,
      child: ListView(padding: const EdgeInsets.fromLTRB(12, 8, 12, 24), children: [
        Row(children: [
          Expanded(child: Text('查看当前网络的出口 IP、位置与运营商，以及国内、国外网站分别看到的 IP。', style: muted)),
          const SizedBox(width: 8),
          FilledButton.tonalIcon(
            onPressed: _running ? null : _run,
            style: FilledButton.styleFrom(visualDensity: VisualDensity.compact),
            icon: _running ? const SizedBox(width: 14, height: 14, child: CircularProgressIndicator(strokeWidth: 2)) : const Icon(Icons.refresh, size: 18),
            label: Text(_running ? '查询中…' : '重新查询'),
          ),
        ]),
        const SizedBox(height: 8),
        _mainCard(context),
        if (domestic.isNotEmpty && intl.isNotEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 4),
            child: Row(children: [
              Icon(split ? Icons.alt_route : Icons.info_outline, size: 16, color: split ? StatusColors.of(context).ok : c.textMuted),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  split ? '国内与国际网站看到的 IP 不同：分流已生效（国内直连、国外经代理）' : '国内与国际网站看到的是同一个 IP：没有分流，或全部经过同一出口',
                  style: muted,
                ),
              ),
            ]),
          ),
        _sourcesCard(context),
        Padding(
          padding: const EdgeInsets.fromLTRB(4, 8, 4, 0),
          child: Text('查询在本机直接进行，不经过面板与 VPS；会把本机 IP 发给上面这些服务，结果不保存。', style: muted),
        ),
      ]),
    );
  }

  Widget _field(String label, String value, AppColors c) => Padding(
        padding: const EdgeInsets.only(top: 10),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text(label, style: TextStyle(fontSize: 12, color: c.textMuted)),
          const SizedBox(height: 2),
          SelectableText(value.isEmpty ? dash : value, style: const TextStyle(fontSize: 15)),
        ]),
      );

  Widget _ipBlock(BuildContext context, String label, IpResult r) {
    final c = appColors(context);
    final flag = flagEmoji(r.countryCode);
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Row(children: [
        Expanded(child: Text(label, style: TextStyle(fontSize: 12, color: c.textMuted))),
        if (flag.isNotEmpty) Text(flag, style: const TextStyle(fontSize: 26)),
      ]),
      Row(children: [
        Flexible(
          child: Text(_mask(r.ip),
              style: const TextStyle(fontSize: 20, fontWeight: FontWeight.w700, fontFeatures: [FontFeature.tabularFigures()])),
        ),
        IconButton(
          tooltip: '复制',
          visualDensity: VisualDensity.compact,
          icon: Icon(Icons.copy_rounded, size: 18, color: c.textMuted),
          onPressed: () {
            Clipboard.setData(ClipboardData(text: r.ip));
            ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('已复制')));
          },
        ),
      ]),
      _field('位置', r.place, c),
      _field('运营商', r.isp, c),
      _field('ASN', [r.asn, r.isp].where((x) => x.isNotEmpty).join(' '), c),
    ]);
  }

  Widget _mainCard(BuildContext context) {
    final c = appColors(context);
    final v4 = _v4;
    return Card(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          if (v4 != null)
            _ipBlock(context, 'IPv4', v4)
          else
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 12),
              child: Text(_running ? '正在查询 IPv4…' : '没有查到 IPv4 出口：网络不可用或查询服务都无法访问', style: TextStyle(color: c.textMuted)),
            ),
          Padding(padding: const EdgeInsets.symmetric(vertical: 12), child: Divider(height: 1, color: c.border)),
          if (_v6 != null)
            _ipBlock(context, 'IPv6', _v6!)
          else
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Row(children: [
                Expanded(child: Text(_v6Done ? '你的网络可能不支持 IPv6' : '正在检测 IPv6…', style: TextStyle(color: c.textMuted))),
                if (_v6Done && !_running) OutlinedButton(onPressed: _run, child: const Text('重试')),
              ]),
            ),
        ]),
      ),
    );
  }

  Widget _sourcesCard(BuildContext context) {
    final c = appColors(context);
    final colors = StatusColors.of(context);
    Widget badge(bool domestic) {
      final color = domestic ? c.accent : colors.ok;
      return Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 1),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.1),
          border: Border.all(color: color.withValues(alpha: 0.6)),
          borderRadius: BorderRadius.circular(99),
        ),
        child: Text(domestic ? '国内' : '国际', style: TextStyle(fontSize: 12, color: color, fontWeight: FontWeight.w600)),
      );
    }

    return Card(
      child: Column(children: [
        for (final (i, s) in ipSources.indexed) ...[
          if (i > 0) Divider(height: 1, indent: 16, endIndent: 16, color: c.border),
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Row(children: [
                Expanded(child: Text(s.name, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w700))),
                badge(s.domestic),
              ]),
              const SizedBox(height: 6),
              if (_results[s.id] case final r?) ...[
                Text.rich(TextSpan(children: [
                  TextSpan(text: 'IP: ', style: TextStyle(color: c.textMuted)),
                  TextSpan(text: _mask(r.ip)),
                ])),
                if (r.summary.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 2), child: Text(r.summary)),
              ] else if (_errors[s.id] case final e?)
                Text('查询失败：$e', style: TextStyle(color: colors.muted))
              else
                Text('查询中…', style: TextStyle(color: c.textMuted)),
            ]),
          ),
        ],
      ]),
    );
  }
}
