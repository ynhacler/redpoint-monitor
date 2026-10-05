// 把各监控中心的推送私钥交给 iOS 的 Notification Service Extension（设计 30.3.1、30.3.3）：
// 原生端写入与扩展共享的钥匙串访问组，扩展收到推送时依次尝试这些私钥解密。
// Android 与测试中没有这个通道：忽略（Android 在 App 进程内解密）。
import 'dart:io' show Platform;

import 'package:flutter/services.dart';

import 'session.dart';

const _channel = MethodChannel('dev.vpsmon/push_keys');

/// 同步全部中心的私钥（整体替换）；失败只记录，不影响 App 使用。
Future<void> syncPushKeys(Iterable<Session> sessions) async {
  if (!Platform.isIOS) return;
  final keys = [for (final s in sessions) if (s.pushPrivateKey.isNotEmpty) s.pushPrivateKey];
  try {
    await _channel.invokeMethod<void>('setKeys', keys);
  } on MissingPluginException {
    // 测试或旧的原生工程：没有通道
  } on PlatformException {
    // 钥匙串不可用（如模拟器未签名）：推送仍显示兜底文字
  }
}
