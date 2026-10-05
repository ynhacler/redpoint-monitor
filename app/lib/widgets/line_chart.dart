// 简单折线图（设计 14“趋势”）：自绘，不引入图表库。一到两条系列，纵轴从 0 开始，
// 标出最大值与起止时间；数据为空时显示提示。
import 'dart:math' as math;

import 'package:flutter/material.dart';

class ChartSeries {
  const ChartSeries(this.label, this.values, this.color);
  final String label;
  final List<double> values;
  final Color color;
}

class LineChart extends StatelessWidget {
  const LineChart({super.key, required this.times, required this.series, required this.formatY, this.fixedMax, this.height = 160});

  /// 每个点的时间（Unix 秒），与各系列等长
  final List<int> times;
  final List<ChartSeries> series;
  final String Function(double) formatY;

  /// 固定的纵轴上限（如百分比为 100）；为空时按数据最大值
  final double? fixedMax;
  final double height;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final muted = theme.colorScheme.onSurfaceVariant;
    if (times.length < 2) {
      return SizedBox(height: height, child: Center(child: Text('暂无数据', style: TextStyle(color: muted))));
    }
    var max = fixedMax ?? series.expand((s) => s.values).fold<double>(0, math.max);
    if (max <= 0) max = 1;
    final labelStyle = TextStyle(fontSize: 11, color: muted);
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Row(children: [
        for (final s in series) ...[
          Container(width: 10, height: 3, color: s.color),
          const SizedBox(width: 4),
          Text(s.label, style: labelStyle),
          const SizedBox(width: 12),
        ],
        const Spacer(),
        Text('最高 ${formatY(series.expand((s) => s.values).fold<double>(0, math.max))}', style: labelStyle),
      ]),
      const SizedBox(height: 6),
      SizedBox(
        height: height,
        width: double.infinity,
        child: CustomPaint(painter: _Painter(times, series, max, theme.colorScheme.outlineVariant)),
      ),
      const SizedBox(height: 4),
      Row(children: [
        Text(_label(times.first, times.last - times.first), style: labelStyle),
        const Spacer(),
        Text(_label(times.last, times.last - times.first), style: labelStyle),
      ]),
    ]);
  }

  static String _label(int unix, int span) {
    final d = DateTime.fromMillisecondsSinceEpoch(unix * 1000);
    String p(int n) => n.toString().padLeft(2, '0');
    return span > 86400 ? '${p(d.month)}-${p(d.day)} ${p(d.hour)}:${p(d.minute)}' : '${p(d.hour)}:${p(d.minute)}';
  }
}

class _Painter extends CustomPainter {
  _Painter(this.times, this.series, this.max, this.grid);
  final List<int> times;
  final List<ChartSeries> series;
  final double max;
  final Color grid;

  @override
  void paint(Canvas canvas, Size size) {
    final gridPaint = Paint()
      ..color = grid
      ..strokeWidth = 1;
    for (var i = 0; i <= 2; i++) {
      final y = size.height * i / 2;
      canvas.drawLine(Offset(0, y), Offset(size.width, y), gridPaint);
    }
    // 横轴按时间：离线期间没有点，相隔超过 3 个间隔时断开折线，不把缺口画成直线
    final t0 = times.first, span = math.max(1, times.last - times.first);
    final step = span / math.max(1, times.length - 1);
    for (final s in series) {
      final n = math.min(s.values.length, times.length);
      if (n < 2) continue;
      final path = Path();
      for (var i = 0; i < n; i++) {
        final x = size.width * (times[i] - t0) / span;
        final y = size.height * (1 - (s.values[i] / max).clamp(0.0, 1.0));
        final gap = i > 0 && times[i] - times[i - 1] > step * 3;
        i == 0 || gap ? path.moveTo(x, y) : path.lineTo(x, y);
      }
      canvas.drawPath(
          path,
          Paint()
            ..color = s.color
            ..style = PaintingStyle.stroke
            ..strokeWidth = 1.5
            ..strokeJoin = StrokeJoin.round);
    }
  }

  @override
  bool shouldRepaint(_Painter old) => old.times != times || old.series != series || old.max != max || old.grid != grid;
}
