// Notification Service Extension（设计 30.3.3）：APNs 推送的外层文字固定为“服务器告警”，真实内容在自定义字段 c 中加密；
// 扩展用本机私钥解密后替换标题与正文。解密失败（没有私钥、旧版 App、密钥已更换）时保留兜底文字。
//
// 【安全】私钥只在本机钥匙串中（与 App 共享的访问组，App 写入）；解密后的内容只用于显示这条通知，不写入任何文件。
import UserNotifications

class NotificationService: UNNotificationServiceExtension {
    private var contentHandler: ((UNNotificationContent) -> Void)?
    private var fallback: UNMutableNotificationContent?

    override func didReceive(_ request: UNNotificationRequest, withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        self.contentHandler = contentHandler
        guard let content = request.content.mutableCopy() as? UNMutableNotificationContent else {
            contentHandler(request.content)
            return
        }
        fallback = content
        guard let b64 = content.userInfo["c"] as? String, let sealed = Data(base64Encoded: b64),
              let plain = PushCrypto.open(privateKeys: PushKeyStore.load(), sealed: sealed),
              let msg = try? JSONDecoder().decode(PushMessage.self, from: plain) else {
            contentHandler(content)
            return
        }
        content.title = msg.title
        content.body = msg.body ?? ""
        if let center = msg.center_id { content.threadIdentifier = center } // 按监控中心分组
        // 点按时 App 据此打开对应中心的节点（设计 15.1）
        var info = content.userInfo
        info.removeValue(forKey: "c")
        if let center = msg.center_id { info["center_id"] = center }
        if let sid = msg.server_id { info["server_id"] = sid }
        content.userInfo = info
        contentHandler(content)
    }

    override func serviceExtensionTimeWillExpire() {
        if let h = contentHandler, let c = fallback { h(c) }
    }
}
