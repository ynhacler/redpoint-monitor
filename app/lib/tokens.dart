// 自动生成，请勿手工编辑：修改 design/tokens.json 后在 web/ 中运行 npm run tokens（设计 41.2）
import 'package:flutter/painting.dart';

class AppColors {
  const AppColors({
    required this.bg,
    required this.surface,
    required this.surface2,
    required this.border,
    required this.text,
    required this.textMuted,
    required this.accent,
    required this.onAccent,
    required this.ok,
    required this.warn,
    required this.bad,
    required this.mutedState,
    required this.series2,
    required this.series3,
    required this.track,
    required this.qrDark,
    required this.qrLight,
    required this.catVideo,
    required this.catTools,
    required this.catReading,
    required this.catSocial,
    required this.catHealth,
    required this.catGame,
    required this.catCloud,
    required this.catSim,
    required this.catVps,
    required this.catOther,
  });

  /// 页面背景
  final Color bg;
  /// 卡片、面板
  final Color surface;
  /// 代码块、次级区域（41.2.1 之外的补充，用于命令块与说明框）
  final Color surface2;
  /// 分隔线、卡片边框
  final Color border;
  /// 主要文字
  final Color text;
  /// 次要文字、单位、说明
  final Color textMuted;
  /// 主按钮、链接、选中态
  final Color accent;
  /// 主按钮、危险按钮上的文字
  final Color onAccent;
  /// 在线、正常（修订第 257 条：浅色 #15803D，白底对比度 5:1，满足 WCAG AA 小字）
  final Color ok;
  /// 告警、未知、接近阈值（修订第 257 条：浅色 #B45309，白底对比度 5:1）
  final Color warn;
  /// 离线、超限、失败
  final Color bad;
  /// 待安装、维护中、已静音
  final Color mutedState;
  /// 图表与分段环的第二类数据：CPU 系统时间、内存缓存
  final Color series2;
  /// 图表与分段环的第三类数据：软中断等次要分类
  final Color series3;
  /// 环形图、每核条的底轨
  final Color track;
  /// 二维码模块：两种主题都是黑底白边，深色模式下也能被扫码识别
  final Color qrDark;
  /// 二维码背景与静区
  final Color qrLight;
  /// 订阅类别“影音娱乐”的固定颜色（修订第 233 条）
  final Color catVideo;
  /// 订阅类别“效率工具”的固定颜色（修订第 233 条）
  final Color catTools;
  /// 订阅类别“阅读学习”的固定颜色（修订第 233 条）
  final Color catReading;
  /// 订阅类别“社交通讯”的固定颜色（修订第 233 条）
  final Color catSocial;
  /// 订阅类别“健康运动”的固定颜色（修订第 233 条）
  final Color catHealth;
  /// 订阅类别“游戏”的固定颜色（修订第 233 条）
  final Color catGame;
  /// 订阅类别“云服务与开发”的固定颜色（修订第 233 条）
  final Color catCloud;
  /// 订阅类别“SIM 电话卡”的固定颜色（修订第 233 条）
  final Color catSim;
  /// 订阅类别“VPS”的固定颜色（修订第 233 条）
  final Color catVps;
  /// 订阅类别“其他”的固定颜色（修订第 233 条）
  final Color catOther;

  static const light = AppColors(
    bg: Color(0xFFF8FAFC),
    surface: Color(0xFFFFFFFF),
    surface2: Color(0xFFF1F5F9),
    border: Color(0xFFE5E7EB),
    text: Color(0xFF0F172A),
    textMuted: Color(0xFF64748B),
    accent: Color(0xFF2563EB),
    onAccent: Color(0xFFFFFFFF),
    ok: Color(0xFF15803D),
    warn: Color(0xFFB45309),
    bad: Color(0xFFDC2626),
    mutedState: Color(0xFF9CA3AF),
    series2: Color(0xFF7C3AED),
    series3: Color(0xFF0891B2),
    track: Color(0xFFE5E7EB),
    qrDark: Color(0xFF000000),
    qrLight: Color(0xFFFFFFFF),
    catVideo: Color(0xFFEA580C),
    catTools: Color(0xFF16A34A),
    catReading: Color(0xFFCA8A04),
    catSocial: Color(0xFFC026D3),
    catHealth: Color(0xFF65A30D),
    catGame: Color(0xFFE11D48),
    catCloud: Color(0xFF0891B2),
    catSim: Color(0xFF7C3AED),
    catVps: Color(0xFF2563EB),
    catOther: Color(0xFF6B7280),
  );

  static const dark = AppColors(
    bg: Color(0xFF0F1115),
    surface: Color(0xFF171A21),
    surface2: Color(0xFF12151B),
    border: Color(0xFF262A33),
    text: Color(0xFFE6E8EC),
    textMuted: Color(0xFF8B93A1),
    accent: Color(0xFF3B82F6),
    onAccent: Color(0xFFFFFFFF),
    ok: Color(0xFF22C55E),
    warn: Color(0xFFF59E0B),
    bad: Color(0xFFEF4444),
    mutedState: Color(0xFF9CA3AF),
    series2: Color(0xFFA78BFA),
    series3: Color(0xFF22D3EE),
    track: Color(0xFF2A2F3A),
    qrDark: Color(0xFF000000),
    qrLight: Color(0xFFFFFFFF),
    catVideo: Color(0xFFFB923C),
    catTools: Color(0xFF22C55E),
    catReading: Color(0xFFFACC15),
    catSocial: Color(0xFFE879F9),
    catHealth: Color(0xFFA3E635),
    catGame: Color(0xFFFB7185),
    catCloud: Color(0xFF22D3EE),
    catSim: Color(0xFFA78BFA),
    catVps: Color(0xFF3B82F6),
    catOther: Color(0xFF9CA3AF),
  );
}

/// 间距（逻辑像素）：space1 = 4 … space8 = 48
abstract final class AppSpace {
  static const space1 = 4.0;
  static const space2 = 8.0;
  static const space3 = 12.0;
  static const space4 = 16.0;
  static const space5 = 20.0;
  static const space6 = 24.0;
  static const space7 = 32.0;
  static const space8 = 48.0;
}

abstract final class AppRadius {
  static const sm = 6.0;
  static const md = 12.0;
  static const full = 9999.0;
}

/// 字号与行高（逻辑像素）
abstract final class AppFont {
  static const xs = 11.0;
  static const xsLine = 16.0;
  static const sm = 12.0;
  static const smLine = 18.0;
  static const md = 14.0;
  static const mdLine = 22.0;
  static const lg = 16.0;
  static const lgLine = 24.0;
  static const xl = 20.0;
  static const xlLine = 28.0;
  static const num = 28.0;
  static const numLine = 32.0;
}
