// 连通性测试（设计 48，修订第 213 条）：从手机当前的网络测试到中国 / 美国 / 全球常用网站的连通性与延迟，
// 用来判断代理分流规则是否生效、线路是否通畅。目标与等级与 Web 相同（catalog.gen.dart 由 Web 的目录生成）。
// 测试在手机上直接进行，不经过面板与 VPS：每个目标先请求一次（含 DNS、TLS 握手），再请求一次取较小值；
// 超时（5 秒）不再改测，连接失败时改测备用地址。App 没有浏览器的跨域限制，能连上即视为可达（任何 HTTP 状态码）。
import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';

import '../catalog.gen.dart';
import '../theme.dart';
import '../widgets/brand_avatar.dart';

/// 一次测试的结果：ms 为延迟，null 时 fail 为 'timeout' 或 'error'
typedef ProbeResult = ({int? ms, String? fail});

/// 测量一个目标：依次尝试各地址，返回延迟或失败原因（可在测试中替换 [once]）。
Future<ProbeResult> measureSite(List<String> urls, Future<Object> Function(String url) once) async {
  for (final url in urls) {
    final first = await once(url);
    if (first == 'timeout') return (ms: null, fail: 'timeout');
    if (first is! int) continue; // 连接失败：改测下一个地址
    final second = await once(url);
    return (ms: second is int && second < first ? second : first, fail: null);
  }
  return (ms: null, fail: 'error');
}

/// 历史中表示“无法连接”的值（null 为超时，修订第 226 条）
const probeFail = -1;

/// 延迟等级：优 < 150 ms，良 < 400 ms，慢 ≥ 400 ms，超时，无法连接（与 Web 相同）
String gradeOf(int? ms) => ms == null ? 'timeout' : (ms < 0 ? 'fail' : (ms < 150 ? 'best' : (ms < 400 ? 'good' : 'slow')));

const _gradeNames = {'best': '优', 'good': '良', 'slow': '慢', 'timeout': '超时', 'fail': '无法连接'};

/// 每个目标保留最近的结果数（圆点个数）
const historyLen = 8;

// 结果保存在内存中：切换标签或重新打开页面时仍显示上次的圆点
final _history = <String, List<int?>>{};
final _latest = <String, ProbeResult>{};
final _testedAt = <String, DateTime>{}; // 分组最近一次完成检测的时间

/// 连通性测试页
class ConnectivityPage extends StatefulWidget {
  /// 创建页面；[embedded] 为 true 时放在“工具”页的分页中，不显示自己的标题栏（修订第 216 条）
  const ConnectivityPage({super.key, this.embedded = false});

  /// 嵌在“工具”页中
  final bool embedded;

  @override
  State<ConnectivityPage> createState() => _ConnectivityPageState();
}

class _ConnectivityPageState extends State<ConnectivityPage> {
  final _running = <String>{};
  static const _timeout = Duration(seconds: 5);

  @override
  void initState() {
    super.initState();
    if (_latest.isEmpty) WidgetsBinding.instance.addPostFrameCallback((_) => _run(siteGroups.map((g) => g.key).toSet()));
  }

  Future<void> _run(Set<String> groups) async {
    if (groups.any(_running.contains)) return;
    setState(() {
      _running.addAll(groups);
      for (final g in siteGroups.where((g) => groups.contains(g.key))) {
        for (final s in g.sites) {
          _latest.remove(s.id);
        }
      }
    });
    final client = HttpClient()
      ..connectionTimeout = _timeout
      ..userAgent = 'Mozilla/5.0 (Linux; Android) vpsmon-app connectivity';
    Future<Object> once(String url) async {
      final sw = Stopwatch()..start();
      try {
        final req = await client.getUrl(Uri.parse(url)).timeout(_timeout);
        req.followRedirects = true;
        final res = await req.close().timeout(_timeout - sw.elapsed);
        final ms = sw.elapsedMilliseconds;
        res.listen(null).cancel(); // 只看响应是否到达，不读取内容
        return ms;
      } on TimeoutException {
        return 'timeout';
      } catch (_) {
        return sw.elapsed >= _timeout ? 'timeout' : 'error';
      }
    }

    final queue = [for (final g in siteGroups.where((g) => groups.contains(g.key))) ...g.sites];
    // 同时最多 6 个请求，避免互相拖慢计时
    Future<void> worker() async {
      while (queue.isNotEmpty) {
        final s = queue.removeAt(0);
        final r = await measureSite(s.urls, once);
        if (!mounted) return;
        setState(() {
          _latest[s.id] = r;
          final h = _history.putIfAbsent(s.id, () => []);
          h.add(r.ms ?? (r.fail == 'error' ? probeFail : null));
          if (h.length > historyLen) h.removeAt(0);
        });
      }
    }

    await Future.wait(List.generate(6, (_) => worker()));
    client.close(force: true);
    if (mounted) {
      setState(() {
        _running.removeAll(groups);
        for (final g in groups) {
          _testedAt[g] = DateTime.now();
        }
      });
    }
  }

  Color _gradeColor(BuildContext context, String g) {
    final colors = StatusColors.of(context);
    final c = appColors(context);
    return switch (g) {
      'best' => colors.ok,
      'good' => Color.lerp(colors.ok, c.surface, 0.45)!,
      'slow' => colors.warn,
      'timeout' => colors.bad,
      'fail' => Color.lerp(colors.bad, c.surface, 0.45)!,
      _ => colors.muted,
    };
  }

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    final any = _running.isNotEmpty;
    return Scaffold(
      appBar: widget.embedded ? null : AppBar(title: const Text('连通性测试')),
      body: ListView(padding: const EdgeInsets.fromLTRB(12, 4, 12, 24), children: [
        Text('测试手机当前网络到各地常用网站的连通性与延迟，用来判断分流规则是否生效。测试在本机进行，不经过面板与 VPS。', style: muted),
        const SizedBox(height: 2),
        Text('探测源：本机（当前网络与代理）；“中国 / 美国 / 全球”是目标网站所在地区。', style: muted),
        const SizedBox(height: 8),
        Row(children: [
          Expanded(
            child: Wrap(spacing: 10, runSpacing: 4, children: [
              for (final g in ['best', 'good', 'slow', 'timeout', 'fail', 'none'])
                Row(mainAxisSize: MainAxisSize.min, children: [
                  Container(
                      width: 8,
                      height: 8,
                      decoration: BoxDecoration(color: g == 'none' ? c.border : _gradeColor(context, g), shape: BoxShape.circle)),
                  const SizedBox(width: 4),
                  Text(_gradeNames[g] ?? '无记录', style: muted),
                ]),
            ]),
          ),
          const SizedBox(width: 8),
          FilledButton.tonalIcon(
            onPressed: any ? null : () => _run(siteGroups.map((g) => g.key).toSet()),
            style: FilledButton.styleFrom(visualDensity: VisualDensity.compact),
            icon: any ? const SizedBox(width: 14, height: 14, child: CircularProgressIndicator(strokeWidth: 2)) : const Icon(Icons.refresh, size: 18),
            label: Text(any ? '测试中…' : '重新测试'),
          ),
        ]),
        for (final g in siteGroups) _group(context, g),
      ]),
    );
  }

  Widget _group(BuildContext context, SiteGroup g) {
    final c = appColors(context);
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    // 摘要只统计本次已完成的检测；平均延迟只算成功样本（修订第 226 条）
    final done = [for (final s in g.sites) _latest[s.id]].whereType<ProbeResult>().toList();
    final ok = done.where((r) => r.ms != null).toList();
    final timeout = done.where((r) => r.ms == null && r.fail != 'error').length;
    final fail = done.length - ok.length - timeout;
    final avg = ok.isEmpty ? null : (ok.fold<int>(0, (a, r) => a + r.ms!) / ok.length).round();
    final at = _testedAt[g.key];
    final String summary;
    if (_running.contains(g.key)) {
      summary = '检测中 ${done.length}/${g.sites.length}';
    } else if (done.isEmpty) {
      summary = '未检测';
    } else {
      summary = [
        '成功 ${ok.length}/${done.length}',
        if (timeout > 0) '超时 $timeout',
        if (fail > 0) '无法连接 $fail',
        avg != null ? '平均 $avg ms' : '无成功样本',
        if (at != null) '${at.hour.toString().padLeft(2, '0')}:${at.minute.toString().padLeft(2, '0')}',
      ].join(' · ');
    }
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Padding(
        padding: const EdgeInsets.fromLTRB(4, 16, 0, 6),
        child: Row(children: [
          Text(g.flag, style: const TextStyle(fontSize: 20)),
          const SizedBox(width: 8),
          Text(g.name, style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700)),
          const SizedBox(width: 10),
          Expanded(
            child: Text(summary, style: muted, overflow: TextOverflow.ellipsis),
          ),
          TextButton(
            onPressed: _running.contains(g.key) ? null : () => _run({g.key}),
            style: TextButton.styleFrom(visualDensity: VisualDensity.compact),
            child: Text(_running.contains(g.key) ? '测试中…' : '刷新'),
          ),
        ]),
      ),
      LayoutBuilder(builder: (context, box) {
        // 手机默认紧凑单列（修订第 255 条，UI-014）：网站名、延迟与最近 8 次圆点不再挤在半宽的格子里；平板两列、更宽三列
        final cols = box.maxWidth >= 900 ? 3 : (box.maxWidth >= 560 ? 2 : 1);
        final w = (box.maxWidth - (cols - 1) * 8) / cols;
        return Wrap(spacing: 8, runSpacing: 8, children: [for (final s in g.sites) SizedBox(width: w, child: _tile(context, s, g.key))]);
      }),
    ]);
  }

  Widget _tile(BuildContext context, SiteTarget s, String group) {
    final c = appColors(context);
    final r = _latest[s.id];
    final hist = _history[s.id] ?? const [];
    final String text;
    Color? color;
    if (r == null) {
      text = _running.contains(group) ? '检测中' : '未检测';
    } else if (r.ms == null) {
      text = r.fail == 'error' ? '无法连接' : '超时';
      color = StatusColors.of(context).bad;
    } else {
      text = '${r.ms} ms';
      color = _gradeColor(context, gradeOf(r.ms));
    }
    return Container(
      padding: const EdgeInsets.fromLTRB(8, 8, 10, 8),
      decoration: BoxDecoration(color: c.surface, borderRadius: BorderRadius.circular(10), border: Border.all(color: c.border)),
      child: Row(children: [
        siteAvatar(s, size: 30),
        const SizedBox(width: 8),
        Expanded(
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text(s.name, style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600), maxLines: 1, overflow: TextOverflow.ellipsis),
            const SizedBox(height: 3),
            Row(children: [
              for (var i = 0; i < historyLen; i++)
                Container(
                  width: 6,
                  height: 6,
                  margin: const EdgeInsets.only(right: 2),
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    color: i < historyLen - hist.length ? c.border : _gradeColor(context, gradeOf(hist[i - (historyLen - hist.length)])),
                  ),
                ),
            ]),
          ]),
        ),
        Text(text, style: TextStyle(fontSize: 12, fontWeight: FontWeight.w600, color: color ?? c.textMuted, fontFeatures: const [FontFeature.tabularFigures()])),
      ]),
    );
  }
}
