// 环形指标（设计 41.3 Ring，与 Web 的 Ring.vue 一致）：节点卡片中的 CPU / 内存 / 磁盘使用率。
// 按健康程度着色，底轨为同色的淡色（tinted），中间显示百分比；从 12 点方向顺时针。
import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../format.dart';

class Ring extends StatelessWidget {
  const Ring({super.key, required this.pct, required this.color, this.size = 52, this.label = ''});

  /// 0～100；没有数据时为 null，显示 “—”
  final double? pct;
  final Color color;
  final double size;

  /// 无障碍名称，如 “CPU”
  final String label;

  @override
  Widget build(BuildContext context) {
    // tinted：粗环（直径 / 8.5），底轨为状态色的 20%
    final stroke = math.max(4.0, (size / 8.5).roundToDouble());
    return Semantics(
      label: '$label ${fmtPct(pct)}',
      child: SizedBox.square(
        dimension: size,
        child: CustomPaint(
          painter: _RingPainter(pct: pct, color: color, stroke: stroke),
          child: Center(
            child: Text(fmtPct(pct),
                style: TextStyle(fontSize: size * 0.25, fontWeight: FontWeight.w600, color: color,
                    fontFeatures: const [FontFeature.tabularFigures()])),
          ),
        ),
      ),
    );
  }
}

class _RingPainter extends CustomPainter {
  _RingPainter({required this.pct, required this.color, required this.stroke});
  final double? pct;
  final Color color;
  final double stroke;

  @override
  void paint(Canvas canvas, Size size) {
    final rect = Rect.fromLTWH(stroke / 2, stroke / 2, size.width - stroke, size.height - stroke);
    final track = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = stroke
      ..color = color.withValues(alpha: 0.2);
    canvas.drawArc(rect, 0, 2 * math.pi, false, track);
    final p = pct;
    if (p == null || p <= 0) return;
    final arc = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = stroke
      ..strokeCap = StrokeCap.butt
      ..color = color;
    canvas.drawArc(rect, -math.pi / 2, 2 * math.pi * (p.clamp(0, 100) / 100), false, arc);
  }

  @override
  bool shouldRepaint(_RingPainter old) => old.pct != pct || old.color != color || old.stroke != stroke;
}
