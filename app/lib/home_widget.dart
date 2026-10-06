// 手机桌面小组件（设计 1.5.4）：App 每次刷新后把概览写入与小组件共享的 App Group，小组件只读这份快照。
//
// 【安全】快照里只有名称、状态、CPU / 内存 / 网速与计数：没有 AK、设备凭证、推送私钥、IP、价格与供应商
// （小组件不长期保存 App 凭证，设计 1.5.4；隐私模式口径，设计 1.5.12）。
// 数字在这里按 format.dart 格式化好，原生端只负责排版，Web / App / 小组件显示一致（设计 41.4.1）。
// iOS 由 AppDelegate 写入 App Group（WidgetKit 扩展读取）；Android 由本地插件 vpsmon_native 写入并刷新小组件。
import 'dart:convert';
import 'dart:io' show Platform;

import 'package:flutter/services.dart';

import 'centers.dart';
import 'format.dart';
import 'metrics.dart';
import 'models.dart';

const _channel = MethodChannel('dev.vpsmon/widget');

/// 大组件最多显示的行数
const widgetRows = 8;

/// 小组件里一行的状态：ok / warn / bad / offline / maintenance / pending
String widgetStatus(ServerView s) {
  if (s.status == 'pending') return 'pending';
  if (s.maintenance != null) return 'maintenance';
  if (s.status == 'offline') return 'offline';
  final i = issues(s);
  if (i.isEmpty) return 'ok';
  return i.first.level == Level.bad ? 'bad' : 'warn';
}

/// 生成快照：计数为全部监控中心之和，行来自当前中心，异常优先（设计 1.5.3）。
Map<String, dynamic> widgetSnapshot(List<ServerView> current, Iterable<CenterSummary> others, DateTime now) {
  final total = CenterSummary.sum([CenterSummary.of(current), ...others]);
  final sorted = [...current]..sort(compareServers);
  return {
    'v': 1,
    'updated_at': now.millisecondsSinceEpoch ~/ 1000,
    'total': total.total,
    'online': total.online,
    'offline': total.offline,
    'attention': total.attention,
    'servers': [
      for (final s in sorted.take(widgetRows))
        {
          'id': s.id,
          'name': s.name,
          'status': widgetStatus(s),
          // 离线时不显示旧数值（设计 43.6）
          'cpu': isLive(s) ? fmtPct(s.latest!.cpu) : dash,
          'mem': isLive(s) ? fmtPct(s.latest!.mem) : dash,
          'rx': isLive(s) ? fmtBytes(s.latest!.rx, perSec: true) : dash,
          'tx': isLive(s) ? fmtBytes(s.latest!.tx, perSec: true) : dash,
        }
    ],
  };
}

/// 把快照交给原生端。内容没变时最多每 15 分钟写一次，避免频繁刷新小组件（系统刷新预算，设计 1.5.4）。
class WidgetPublisher {
  WidgetPublisher({this.minInterval = const Duration(minutes: 15)});
  final Duration minInterval;
  String? _last;
  DateTime? _lastAt;

  /// 返回是否真的发送了（测试用）
  Future<bool> publish(Map<String, dynamic> snapshot, {DateTime? now}) async {
    final t = now ?? DateTime.now();
    final key = jsonEncode({...snapshot, 'updated_at': 0});
    if (key == _last && _lastAt != null && t.difference(_lastAt!) < minInterval) return false;
    _last = key;
    _lastAt = t;
    if (!Platform.isIOS && !Platform.isAndroid) return true;
    try {
      await _channel.invokeMethod<void>('update', jsonEncode(snapshot));
    } on MissingPluginException {
      // 测试或旧的原生工程
    } on PlatformException {
      // App Group 不可用：小组件显示“打开 App 以更新”
    }
    return true;
  }
}

/// 所有监控中心都已移除：清空小组件快照，不再显示已解除的面板的数据。
Future<void> clearHomeWidget() async {
  if (!Platform.isIOS && !Platform.isAndroid) return;
  try {
    await _channel.invokeMethod<void>('clear');
  } on MissingPluginException {
    // 测试或旧的原生工程
  } on PlatformException {
    // 忽略
  }
}
