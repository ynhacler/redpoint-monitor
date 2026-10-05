// App 推送的登记（设计 15、30）：取得系统的推送 Token，登记到面板；收到推送后由 push_crypto.dart 解密。
//
// Token 来源与平台通道隔离在 PushTokenSource 后面：接入 APNs / FCM 需要开发者的 Apple 推送密钥与 Firebase 项目
// （GoogleService-Info.plist、google-services.json），未配置的构建使用 [NoPushTokenSource]，界面说明原因。
// TODO(C): 接入 firebase_messaging（iOS 取 APNs Token、Android 取 FCM Token），iOS 增加 Notification Service Extension
// 用 CryptoKit HPKE 解密（与 push_crypto.dart 同一参数），Android 在后台处理器中解密并创建本地通知。
import 'api.dart';

/// 推送 Token 的来源：返回（平台 apns / fcm，Token），不可用时返回 null。
abstract class PushTokenSource {
  /// 本构建是否接入了推送服务
  bool get available;
  Future<(String, String)?> token();
}

/// 未接入推送服务的构建。
class NoPushTokenSource implements PushTokenSource {
  const NoPushTokenSource();
  @override
  bool get available => false;
  @override
  Future<(String, String)?> token() async => null;
}

/// 推送状态（设置页显示）。
enum PushState {
  /// 面板未配置 Push Relay（--push-relay）
  panelDisabled,

  /// 本构建没有接入推送服务
  appUnavailable,

  /// 系统未给出 Token（未授权通知等）
  noToken,

  /// 已登记
  enabled,
}

const pushStateText = {
  PushState.panelDisabled: '面板未启用推送：需要在面板启动参数中设置 --push-relay',
  PushState.appUnavailable: '此版本的 App 尚未接入系统推送服务；告警仍可在 App 中查看，也可配置 Telegram 等渠道',
  PushState.noToken: '没有取得推送权限：请在系统设置中允许通知',
  PushState.enabled: '已开启：告警内容端到端加密，推送服务只能看到密文',
};

/// syncPush 在 App 启动、回到前台时调用：面板支持推送且取得 Token 时登记（Token 变化时面板覆盖旧值）。
Future<PushState> syncPush(ApiClient api, PushTokenSource source) async {
  final me = await api.me();
  if (me['push_available'] != true) return PushState.panelDisabled;
  if (!source.available) return PushState.appUnavailable;
  final t = await source.token();
  if (t == null) return PushState.noToken;
  await api.registerPush(t.$1, t.$2);
  return PushState.enabled;
}
