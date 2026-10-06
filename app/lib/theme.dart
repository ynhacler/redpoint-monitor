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
    // 与 Web 一致（设计 41.3）：卡片为表面色 + 1px 边框 + 12px 圆角，没有阴影
    cardTheme: CardThemeData(
      color: c.surface,
      elevation: 0,
      margin: const EdgeInsets.symmetric(vertical: 5),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12), side: BorderSide(color: c.border)),
    ),
    // 顶栏同 Web：表面色、底部分隔线、滚动时不变色
    appBarTheme: AppBarTheme(backgroundColor: c.surface, surfaceTintColor: Colors.transparent, scrolledUnderElevation: 0,
        shape: Border(bottom: BorderSide(color: c.border))),
    navigationBarTheme: NavigationBarThemeData(backgroundColor: c.surface, surfaceTintColor: Colors.transparent),
    dividerTheme: DividerThemeData(color: c.border, space: 1),
    textTheme: Typography.material2021(platform: TargetPlatform.android).black.apply(bodyColor: c.text, displayColor: c.text),
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
