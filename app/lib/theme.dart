// 状态颜色：取自 design/tokens.json（设计 41.2），与 Web 一致。
// TODO(C): 由 tokens.json 生成 tokens.dart（与 Web 的 tokens.css 同源），替换这里的手写值。
import 'package:flutter/material.dart';

import 'metrics.dart';

class StatusColors {
  const StatusColors({required this.ok, required this.warn, required this.bad, required this.muted});
  final Color ok;
  final Color warn;
  final Color bad;
  final Color muted;

  static const light = StatusColors(ok: Color(0xFF16A34A), warn: Color(0xFFD97706), bad: Color(0xFFDC2626), muted: Color(0xFF9CA3AF));
  static const dark = StatusColors(ok: Color(0xFF22C55E), warn: Color(0xFFF59E0B), bad: Color(0xFFEF4444), muted: Color(0xFF6B7280));

  static StatusColors of(BuildContext context) => Theme.of(context).brightness == Brightness.dark ? dark : light;

  Color level(Level l) => switch (l) { Level.ok => ok, Level.warn => warn, Level.bad => bad };

  /// 在线状态的颜色：在线绿、未知橙、离线红、待安装灰（设计 22）
  Color status(String s) => switch (s) { 'online' => ok, 'unknown' => warn, 'offline' => bad, _ => muted };
}

const statusText = {'online': '在线', 'unknown': '上报延迟', 'offline': '离线', 'pending': '待安装'};

/// 图表的系列颜色（tokens.json 的 accent、series-2）
Color seriesColor(BuildContext context, int i) {
  final dark = Theme.of(context).brightness == Brightness.dark;
  const light = [Color(0xFF2563EB), Color(0xFF7C3AED)];
  const darkC = [Color(0xFF3B82F6), Color(0xFFA78BFA)];
  return (dark ? darkC : light)[i % 2];
}
