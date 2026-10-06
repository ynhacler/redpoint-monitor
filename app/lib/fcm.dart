// Android 系统推送（设计 30）：FCM 只传递密文，App 用各监控中心的私钥在本机解密后创建通知。
//
// 流程：面板启用推送 → App 取得 FCM Token 并登记到面板（push.dart）→ 面板加密告警、签名后交给官方 Relay →
// Relay 发 FCM data message（只有字段 c，设计 30.3.3）→ 本文件的处理器解密 → vpsmon_native 显示通知。
//
// 【安全】
//   - 解密只在本机进行；私钥只在系统安全存储中（各中心一把），处理器读取后不写入任何文件
//   - 推送明文不写日志；解不开时只显示“服务器告警”，不显示密文
//   - 构建里没有 google-services.json 时 Firebase 初始化失败：不启用推送，App 其余功能不受影响
//   - FCM 不自动初始化（AndroidManifest 中 firebase_messaging_auto_init_enabled=false），只在登记推送时取得 Token
import 'dart:convert';
import 'dart:io' show Platform;

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart';
import 'package:vpsmon_native/vpsmon_native.dart';

import 'centers.dart';
import 'push.dart';
import 'push_crypto.dart';
import 'session.dart';

/// 依次尝试各中心的私钥解密（推送里不带明文的中心标识，避免泄露）；都解不开时返回 null。
Future<PushMessage?> openWithAnyKey(Iterable<Session> sessions, String ciphertextB64) async {
  for (final s in sessions) {
    if (s.pushPrivateKey.isEmpty) continue;
    try {
      return await decodePush(base64.decode(s.pushPrivateKey), ciphertextB64);
    } catch (_) {
      // 不是这个中心的密钥，或密文损坏
    }
  }
  return null;
}

/// 通知编号：同一中心、同一节点、同一类通知（kind）相互替换，重复的告警不会堆叠。
int notificationId(PushMessage m) => Object.hash(m.centerId, m.serverId, m.kind) & 0x7fffffff;

/// 处理一条推送：解密并显示通知，同时记录到桌面小组件的“最近告警”。
Future<void> handleEncryptedPush(String? ciphertextB64, CentersStore store) async {
  if (ciphertextB64 == null || ciphertextB64.isEmpty) return;
  final centers = await store.load();
  final m = await openWithAnyKey(centers.sessions, ciphertextB64);
  if (m == null) {
    // 设计 30.3.3：解不开（旧密钥、已解除的中心）时只提示有告警
    await showAlertNotification(id: 1, title: '服务器告警', body: '打开 App 查看详情', severity: 'warning');
    return;
  }
  await showAlertNotification(
    id: notificationId(m),
    title: m.title,
    body: m.body,
    severity: m.severity,
    centerId: m.centerId,
    serverId: m.serverId,
  );
  if (m.severity == 'critical' || m.severity == 'warning') {
    await saveWidgetAlert(title: m.title, severity: m.severity, ts: m.ts);
  }
}

/// FCM 后台处理器：App 不在前台（或进程已结束）时由系统唤起，在独立的 Flutter 引擎中运行。
@pragma('vm:entry-point')
Future<void> fcmBackgroundHandler(RemoteMessage message) async {
  await Firebase.initializeApp();
  await handleEncryptedPush(message.data['c'] as String?, const SecureCentersStore());
}

/// 初始化 Android 推送；不可用（非 Android、没有 Firebase 配置）时返回 [NoPushTokenSource]。
Future<PushTokenSource> initPush(CentersStore store) async {
  if (!Platform.isAndroid) return const NoPushTokenSource();
  try {
    await Firebase.initializeApp();
  } catch (e) {
    debugPrint('未接入 Firebase（没有 google-services.json），推送不可用');
    return const NoPushTokenSource();
  }
  FirebaseMessaging.onBackgroundMessage(fcmBackgroundHandler);
  // 前台：data message 不会自动显示，同样解密后显示通知
  FirebaseMessaging.onMessage.listen((m) => handleEncryptedPush(m.data['c'] as String?, store));
  return const FcmTokenSource();
}

/// FCM Token：首次登记时请求通知权限（Android 13+）。
class FcmTokenSource implements PushTokenSource {
  const FcmTokenSource();

  @override
  bool get available => true;

  @override
  Future<(String, String)?> token() async {
    final fm = FirebaseMessaging.instance;
    final perm = await fm.requestPermission();
    if (perm.authorizationStatus == AuthorizationStatus.denied) return null;
    final t = await fm.getToken();
    if (t == null || t.isEmpty) return null;
    return ('fcm', t);
  }
}
