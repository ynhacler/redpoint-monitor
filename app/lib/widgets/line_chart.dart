// 简单折线图（设计 14“趋势”）：自绘，不引入图表库。一到两条系列，纵轴从 0 开始，
// 上限按数据取整（留约 15% 余量，百分比不超过 100%），左侧标出上限与一半处的数值，第一条系列下方浅色填充；
// 标出本区间平均与峰值（修订第 243 条）与起止时间；数据为空时显示提示。
import 'dart:math' as math;

import 'package:flutter/material.dart';

/// 折线图的一条系列
class ChartSeries {
  const ChartSeries(this.label, this.values, this.color);
  final String label;
  final List<double> values;
  final Color color;
}

/// 折线图：一到两条系列，纵轴按数据取整
class LineChart extends StatelessWidget {
  const LineChart({super.key, required this.times, required this.series, required this.formatY, this.fixedMax, this.capMax, this.minMax,
      this.height = 160});

  /// 每个点的时间（Unix 秒），与各系列等长
  final List<int> times;
  final List<ChartSeries> series;
  final String Function(double) formatY;

  /// 固定的纵轴上限（如百分比为 100）；为空时按数据最大值
  final double? fixedMax;

  /// 按数据取整后的上限不超过 capMax（百分比为 100）、不低于 minMax（百分比为 10，低负载时不把 1% 的波动放大成满屏）
  final double? capMax;
  final double? minMax;
  final double height;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final muted = theme.colorScheme.onSurfaceVariant;
    if (times.length < 2) {
      return SizedBox(height: height, child: Center(child: Text('暂无数据', style: TextStyle(color: muted))));
    }
    // 值为 NaN 表示该时刻没有数据（如测速全部失败），折线在此断开
    final dataMax = series.expand((s) => s.values).where((v) => v.isFinite).fold<double>(0, math.max);
    var max = fixedMax ?? chartMax(dataMax, cap: capMax, floor: minMax);
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
        // 本区间的平均与峰值（修订第 243 条，与 Web 图表相同）：只算有数据的点；多条系列时按第一条；窄屏省略
        Expanded(
          child: Text(_stats(series.first.values), style: labelStyle, textAlign: TextAlign.end, maxLines: 1, overflow: TextOverflow.ellipsis),
        ),
      ]),
      const SizedBox(height: 6),
      SizedBox(
        height: height,
        width: double.infinity,
        child: Stack(children: [
          Positioned.fill(child: CustomPaint(painter: _Painter(times, series, max, theme.colorScheme.outlineVariant))),
          // 纵轴刻度：上限与一半
          Positioned(left: 0, top: 2, child: Text(formatY(max), style: labelStyle.copyWith(fontSize: 10))),
          Positioned(left: 0, top: height / 2 + 2, child: Text(formatY(max / 2), style: labelStyle.copyWith(fontSize: 10))),
        ]),
      ),
      const SizedBox(height: 4),
      Row(children: [
        Text(_label(times.first, times.last - times.first), style: labelStyle),
        const Spacer(),
        Text(_label(times.last, times.last - times.first), style: labelStyle),
      ]),
    ]);
  }

  String _stats(List<double> values) {
    final v = values.where((x) => x.isFinite).toList();
    if (v.isEmpty) return '';
    final avg = v.reduce((a, b) => a + b) / v.length;
    final peak = v.fold<double>(0, math.max);
    return '${series.length > 1 ? '${series.first.label} ' : ''}平均 ${formatY(avg)} · 峰值 ${formatY(peak)}';
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
      var broken = true;
      for (var i = 0; i < n; i++) {
        if (!s.values[i].isFinite) {
          broken = true;
          continue;
        }
        final x = size.width * (times[i] - t0) / span;
        final y = size.height * (1 - (s.values[i] / max).clamp(0.0, 1.0));
        final gap = i > 0 && times[i] - times[i - 1] > step * 3;
        broken || gap ? path.moveTo(x, y) : path.lineTo(x, y);
        broken = false;
      }
      // 第一条系列下方浅色填充（只在连续的一段下面填充）
      // 只有一条系列时浅色填充；多条为纯折线（修订第 251 条，UI-010）
      if (series.length == 1) _fill(canvas, size, s, t0, span, step);
      canvas.drawPath(
          path,
          Paint()
            ..color = s.color
            ..style = PaintingStyle.stroke
            ..strokeWidth = 1.5
            ..strokeJoin = StrokeJoin.round);
    }
  }

  void _fill(Canvas canvas, Size size, ChartSeries s, int t0, int span, double step) {
    final n = math.min(s.values.length, times.length);
    final paint = Paint()
      ..shader = LinearGradient(begin: Alignment.topCenter, end: Alignment.bottomCenter,
              colors: [s.color.withValues(alpha: 0.18), s.color.withValues(alpha: 0.02)])
          .createShader(Offset.zero & size);
    Path? area;
    double? lastX;
    void close() {
      if (area != null && lastX != null) {
        area!.lineTo(lastX!, size.height);
        area!.close();
        canvas.drawPath(area!, paint);
      }
      area = null;
      lastX = null;
    }

    for (var i = 0; i < n; i++) {
      final v = s.values[i];
      final gap = i > 0 && times[i] - times[i - 1] > step * 3;
      if (!v.isFinite || gap) close();
      if (!v.isFinite) continue;
      final x = size.width * (times[i] - t0) / span;
      final y = size.height * (1 - (v / max).clamp(0.0, 1.0));
      if (area == null) {
        area = Path()
          ..moveTo(x, size.height)
          ..lineTo(x, y);
      } else {
        area!.lineTo(x, y);
      }
      lastX = x;
    }
    close();
  }

  @override
  bool shouldRepaint(_Painter old) => old.times != times || old.series != series || old.max != max || old.grid != grid;
}

/// 纵轴上限：数据最大值留约 15% 余量后取整到 1、2、2.5、5 × 10ⁿ；cap 为上限（百分比 100），floor 为下限。
double chartMax(double dataMax, {double? cap, double? floor}) {
  final target = dataMax * 1.15;
  var nice = 1.0;
  if (target > 0) {
    final mag = math.pow(10, (math.log(target) / math.ln10).floor()).toDouble();
    nice = [1.0, 2.0, 2.5, 5.0, 10.0].map((m) => m * mag).firstWhere((v) => v >= target);
  }
  if (floor != null && nice < floor) nice = floor;
  if (cap != null && nice > cap) nice = cap;
  return nice;
}
