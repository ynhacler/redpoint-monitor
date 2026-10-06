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
    // 桌面小组件的概览快照写入 App Group（设计 1.5.4）
    if let registrar = engineBridge.pluginRegistry.registrar(forPlugin: "VpsmonWidget") {
      let channel = FlutterMethodChannel(name: "dev.vpsmon/widget", binaryMessenger: registrar.messenger())
      channel.setMethodCallHandler { call, result in
        switch call.method {
        case "update":
          guard let json = call.arguments as? String, WidgetShared.saveSnapshot(json) else {
            result(FlutterError(code: "app_group", message: "小组件数据写入失败", details: nil))
            return
          }
          result(nil)
        case "clear":
          WidgetShared.clear()
          result(nil)
        default:
          result(FlutterMethodNotImplemented)
        }
      }
    }
  }
}
