// VPS Monitor 的 Android 原生部分（设计 1.5.4、30.3.3）。其他平台调用时直接返回。
import 'dart:io' show Platform;

import 'package:flutter/services.dart';

const _notify = MethodChannel('dev.vpsmon/notify');
const _widget = MethodChannel('dev.vpsmon/widget');

/// 显示一条告警通知。[id] 相同的通知相互替换。
/// [severity]：critical / warning / info，决定通知渠道（重要程度）。
Future<void> showAlertNotification({
  required int id,
  required String title,
  required String body,
  required String severity,
  String centerId = '',
  int? serverId,
}) async {
  if (!Platform.isAndroid) return;
  await _notify.invokeMethod<void>('show', {
    'id': id,
    'title': title,
    'body': body,
    'severity': severity,
    'center_id': centerId,
    'server_id': serverId,
  });
}

/// 记录最近一条告警，桌面小组件（大尺寸）显示 24 小时内的。
Future<void> saveWidgetAlert({required String title, required String severity, required int ts}) async {
  if (!Platform.isAndroid) return;
  await _widget.invokeMethod<void>('alert', {'title': title, 'severity': severity, 'ts': ts});
}
