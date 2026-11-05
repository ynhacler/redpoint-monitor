import 'package:flutter/material.dart';

import '../api.dart';
import '../subscriptions.dart';
import '../theme.dart';
import 'line_chart.dart';

/// 汇率走势（修订第 297 条，与 Web 相同）：面板每天记一次订阅用到的外币汇率，这里只看走势，订阅金额的换算仍用最新汇率。
/// 汇率不是用户的金额，隐私模式下照常显示。展开时才请求；旧版面板没有该接口时提示升级。
class FxTrend extends StatefulWidget {
  /// 创建汇率走势
  const FxTrend({super.key, required this.currencies, required this.load});

  /// 订阅用到的外币
  final List<String> currencies;

  /// 读取某币种的历史，按日期从早到晚
  final Future<List<FxDay>> Function(String currency) load;

  @override
  State<FxTrend> createState() => _FxTrendState();
}

class _FxTrendState extends State<FxTrend> {
  late String _cur = widget.currencies.first;
  bool _opened = false;
  bool _loading = false;
  String _error = '';
  List<FxDay> _rows = const [];

  Future<void> _fetch(String c) async {
    setState(() {
      _cur = c;
      _loading = true;
      _error = '';
    });
    try {
      final rows = await widget.load(c);
      if (!mounted || c != _cur) return;
      setState(() {
        _rows = rows;
        _loading = false;
      });
    } catch (e) {
      if (!mounted || c != _cur) return;
      setState(() {
        _error = e is ApiException && e.status == 404 ? '面板版本较旧，升级后可以查看汇率走势。' : '汇率历史加载失败';
        _loading = false;
      });
    }
  }

  String _fmt(double v) => '¥${v.toStringAsFixed(v < 0.1 ? 6 : 4)}';

  Widget _body(BuildContext context, TextStyle muted) {
    if (_loading) return const Padding(padding: EdgeInsets.all(12), child: Center(child: CircularProgressIndicator()));
    if (_error.isNotEmpty) return Text(_error, style: muted);
    if (_rows.length < 2) return Text('记录还不够画出走势。面板每天记一次订阅用到的外币汇率，来源没有更早的数据。', style: muted);
    final values = _rows.map((r) => r.cny).toList();
    final low = values.reduce((a, b) => a < b ? a : b), high = values.reduce((a, b) => a > b ? a : b);
    final change = fxChange30(_rows);
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text('区间 ${_fmt(low)} ～ ${_fmt(high)}${change == null ? '' : ' · 较 30 天前 ${change > 0 ? '+' : ''}${change.toStringAsFixed(2)}%'}', style: muted),
      const SizedBox(height: 8),
      LineChart(
        times: _rows.map((r) => r.unix).toList(),
        series: [ChartSeries('1 $_cur', values, appColors(context).accent)],
        formatY: _fmt,
        scaleY: true,
        dateOnly: true,
      ),
      const SizedBox(height: 6),
      Text('从 ${_rows.first.day} 开始记录，只用于查看走势，订阅金额按最新汇率换算。', style: muted),
    ]);
  }

  @override
  Widget build(BuildContext context) {
    final muted = TextStyle(fontSize: 12, color: appColors(context).textMuted);
    return ExpansionTile(
      tilePadding: const EdgeInsets.symmetric(horizontal: 4),
      childrenPadding: const EdgeInsets.fromLTRB(4, 0, 4, 8),
      expandedCrossAxisAlignment: CrossAxisAlignment.start,
      title: const Text('汇率走势', style: TextStyle(fontSize: 14)),
      onExpansionChanged: (open) {
        if (open && !_opened) {
          _opened = true;
          _fetch(_cur);
        }
      },
      children: [
        if (widget.currencies.length > 1)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Wrap(spacing: 8, children: [
              for (final c in widget.currencies)
                ChoiceChip(label: Text(c), selected: c == _cur, onSelected: (_) => c == _cur ? null : _fetch(c)),
            ]),
          ),
        _body(context, muted),
      ],
    );
  }
}
