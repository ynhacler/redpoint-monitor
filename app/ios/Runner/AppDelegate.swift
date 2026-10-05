import Flutter
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    // 推送私钥写入与 Notification Service Extension 共享的钥匙串（设计 30.3.1）
    if let registrar = engineBridge.pluginRegistry.registrar(forPlugin: "VpsmonPushKeys") {
      let channel = FlutterMethodChannel(name: "dev.vpsmon/push_keys", binaryMessenger: registrar.messenger())
      channel.setMethodCallHandler { call, result in
        guard call.method == "setKeys", let keys = call.arguments as? [String] else {
          result(FlutterMethodNotImplemented)
          return
        }
        let status = PushKeyStore.save(keys)
        result(status == errSecSuccess ? nil : FlutterError(code: "keychain", message: "钥匙串写入失败（\(status)）", details: nil))
      }
    }
  }
}
