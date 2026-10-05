// 主题与状态颜色：取自 design/tokens.json 生成的 tokens.dart（设计 41.2），与 Web 同源。
import 'package:flutter/material.dart';

import 'metrics.dart';
import 'tokens.dart';

AppColors appColors(BuildContext context) => Theme.of(context).brightness == Brightness.dark ? AppColors.dark : AppColors.light;

/// Material 3 主题：主色、表面、背景与错误色来自令牌，其余由主色派生。
ThemeData appTheme(Brightness b) {
  final c = b == Brightness.dark ? AppColors.dark : AppColors.light;
  return ThemeData(
    useMaterial3: true,
    colorScheme: ColorScheme.fromSeed(seedColor: c.accent, brightness: b, primary: c.accent, onPrimary: c.onAccent,
        surface: c.surface, error: c.bad, outlineVariant: c.border),
    scaffoldBackgroundColor: c.bg,
  );
}

class StatusColors {
  const StatusColors(this.c);
  final AppColors c;

  static StatusColors of(BuildContext context) => StatusColors(appColors(context));

  Color get ok => c.ok;
  Color get warn => c.warn;
  Color get bad => c.bad;
  Color get muted => c.mutedState;

  Color level(Level l) => switch (l) { Level.ok => c.ok, Level.warn => c.warn, Level.bad => c.bad };

  /// 在线状态的颜色：在线绿、未知橙、离线红、待安装灰（设计 22）
  Color status(String s) => switch (s) { 'online' => c.ok, 'unknown' => c.warn, 'offline' => c.bad, _ => c.mutedState };
}

const statusText = {'online': '在线', 'unknown': '上报延迟', 'offline': '离线', 'pending': '待安装'};

/// 图表的系列颜色（accent、series-2）
Color seriesColor(BuildContext context, int i) {
  final c = appColors(context);
  return i % 2 == 0 ? c.accent : c.series2;
}
