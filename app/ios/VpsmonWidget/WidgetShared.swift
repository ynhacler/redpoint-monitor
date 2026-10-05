// 桌面小组件的共享存储（设计 1.5.4）：App、通知扩展与小组件通过 App Group 的 UserDefaults 交换一份概览快照。
// 同一文件编译进 Runner、NotificationService 与 VpsmonWidget 三个目标。
//
// 【安全】这里只有 App 已格式化好的名称、状态与数字，以及最近一条告警的标题；没有任何凭证或私钥
// （小组件不长期保存 App AK，设计 1.5.4）。凭证与推送私钥只在钥匙串中。
import Foundation
import WidgetKit

enum WidgetShared {
    static let appGroup = "group.dev.vpsmon.vpsmonApp"
    static let snapshotKey = "widget_snapshot_v1"
    static let alertKey = "widget_alert_v1"
    static let kind = "VpsmonWidget"

    static var defaults: UserDefaults? { UserDefaults(suiteName: appGroup) }

    /// App 写入快照（JSON 字符串）并请求刷新小组件；返回是否成功
    static func saveSnapshot(_ json: String) -> Bool {
        guard let d = defaults, json.utf8.count <= 64 * 1024 else { return false }
        d.set(json, forKey: snapshotKey)
        WidgetCenter.shared.reloadTimelines(ofKind: kind)
        return true
    }

    /// 所有中心都已移除：清空快照与最近告警
    static func clear() {
        defaults?.removeObject(forKey: snapshotKey)
        defaults?.removeObject(forKey: alertKey)
        WidgetCenter.shared.reloadTimelines(ofKind: kind)
    }

    /// 通知扩展解密出告警后记录最近一条并刷新小组件（设计 1.5.4：收到告警时顺带刷新）
    static func saveAlert(title: String, severity: String?, ts: Int?) {
        guard let d = defaults else { return }
        let a = WidgetAlert(title: String(title.prefix(120)), severity: severity ?? "warning",
                            ts: ts ?? Int(Date().timeIntervalSince1970))
        if let data = try? JSONEncoder().encode(a) { d.set(data, forKey: alertKey) }
        WidgetCenter.shared.reloadTimelines(ofKind: kind)
    }

    static func loadSnapshot() -> WidgetSnapshot? {
        guard let s = defaults?.string(forKey: snapshotKey) else { return nil }
        return try? JSONDecoder().decode(WidgetSnapshot.self, from: Data(s.utf8))
    }

    static func loadAlert() -> WidgetAlert? {
        guard let data = defaults?.data(forKey: alertKey) else { return nil }
        return try? JSONDecoder().decode(WidgetAlert.self, from: data)
    }
}

/// 与 app/lib/home_widget.dart 的 widgetSnapshot 对应
struct WidgetSnapshot: Codable {
    struct Row: Codable, Identifiable {
        let id: Int
        let name: String
        let status: String // ok / warn / bad / offline / maintenance / pending
        let cpu: String
        let mem: String
        let rx: String
        let tx: String
    }
    let updated_at: Int
    let total: Int
    let online: Int
    let offline: Int
    let attention: Int
    let servers: [Row]
}

struct WidgetAlert: Codable {
    let title: String
    let severity: String
    let ts: Int
}
