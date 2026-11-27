// 主题与状态颜色：取自 design/tokens.json 生成的 tokens.dart（设计 41.2），与 Web 同源。
import 'package:flutter/material.dart';

import 'metrics.dart';
import 'tokens.dart';

/// 当前明暗模式下的设计令牌颜色（设计 41.2）
AppColors appColors(BuildContext context) => Theme.of(context).brightness == Brightness.dark ? AppColors.dark : AppColors.light;

/// Material 3 主题：主色、表面、背景与错误色来自令牌，其余由主色派生。
ThemeData appTheme(Brightness b) {
  final c = b == Brightness.dark ? AppColors.dark : AppColors.light;
  return ThemeData(
    useMaterial3: true,
    colorScheme: ColorScheme.fromSeed(seedColor: c.accent, brightness: b, primary: c.accent, onPrimary: c.onAccent,
        surface: c.surface, error: c.bad, outlineVariant: c.border),
    scaffoldBackgroundColor: c.bg,
    // 与 Web 一致（设计 41.3，修订第 340 条）：卡片为表面色 + 1px 边框 + 12px 圆角 + 柔和阴影
    cardTheme: CardThemeData(
      color: c.surface,
      surfaceTintColor: Colors.transparent,
      elevation: 1.5,
      shadowColor: c.scrim.withValues(alpha: b == Brightness.dark ? 0.4 : 0.12),
      margin: const EdgeInsets.symmetric(vertical: 5),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12), side: BorderSide(color: c.border)),
    ),
    // 输入框（修订第 340 条）：圆角 10、表面色底，聚焦时强调色描边，与 Web 一致
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: c.surface,
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(10), borderSide: BorderSide(color: c.border)),
      enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(10), borderSide: BorderSide(color: c.border)),
      focusedBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(10), borderSide: BorderSide(color: c.accent, width: 1.6)),
      errorBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(10), borderSide: BorderSide(color: c.bad)),
      focusedErrorBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(10), borderSide: BorderSide(color: c.bad, width: 1.6)),
    ),
    // 按钮圆角 10（修订第 340 条）
    filledButtonTheme: FilledButtonThemeData(style: FilledButton.styleFrom(shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)))),
    outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)), side: BorderSide(color: c.border))),
    // 顶栏同 Web：表面色、底部分隔线、滚动时不变色
    appBarTheme: AppBarTheme(backgroundColor: c.surface, surfaceTintColor: Colors.transparent, scrolledUnderElevation: 0,
        shape: Border(bottom: BorderSide(color: c.border))),
    // 底部导航（修订第 340 条）：选中项为浅强调色药丸，图标与文字为强调色
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: c.surface,
      surfaceTintColor: Colors.transparent,
      indicatorColor: c.accent.withValues(alpha: b == Brightness.dark ? 0.22 : 0.12),
      iconTheme: WidgetStateProperty.resolveWith((s) => IconThemeData(color: s.contains(WidgetState.selected) ? c.accent : c.textMuted)),
      labelTextStyle: WidgetStateProperty.resolveWith((s) => TextStyle(
          fontSize: 12,
          color: s.contains(WidgetState.selected) ? c.accent : c.textMuted,
          fontWeight: s.contains(WidgetState.selected) ? FontWeight.w600 : FontWeight.w400)),
    ),
    dividerTheme: DividerThemeData(color: c.border, space: 1),
    // 开关（修订第 341 条）：与 Web 一致，打开为强调色 + 白色圆钮，关闭为浅灰轨道，没有描边
    switchTheme: SwitchThemeData(
      thumbColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? c.onAccent : c.surface),
      trackColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? c.accent : c.track),
      trackOutlineColor: const WidgetStatePropertyAll(Colors.transparent),
      thumbIcon: const WidgetStatePropertyAll(null),
    ),
    textTheme: Typography.material2021(platform: TargetPlatform.android).black.apply(bodyColor: c.text, displayColor: c.text),
  );
}

/// 状态颜色：正常、警告、严重、离线等（设计 22）
class StatusColors {
  const StatusColors(this.c);
  final AppColors c;

  static StatusColors of(BuildContext context) => StatusColors(appColors(context));

  Color get ok => c.ok;
  Color get warn => c.warn;
  Color get bad => c.bad;
  Color get muted => c.mutedState;

  Color level(Level l) => switch (l) { Level.ok => c.ok, Level.warn => c.warn, Level.bad => c.bad };

  /// 数字的颜色（修订第 249 条，UI-008）：正常时为 null（正文色），接近 / 超过阈值时为警告 / 严重色；绿色只用于状态
  Color? text(Level l) => l == Level.ok ? null : level(l);

  /// 在线状态的颜色：在线绿、未知橙、离线红、待安装灰（设计 22）
  Color status(String s) => switch (s) { 'online' => c.ok, 'unknown' => c.warn, 'offline' => c.bad, _ => c.mutedState };
}

const statusText = {'online': '在线', 'unknown': '上报延迟', 'offline': '离线', 'pending': '待安装', 'maintenance': '维护中'};

/// 图表的系列颜色（accent、series-2）
Color seriesColor(BuildContext context, int i) {
  final c = appColors(context);
  return i % 2 == 0 ? c.accent : c.series2;
}
