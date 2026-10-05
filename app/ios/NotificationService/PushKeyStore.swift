// 推送私钥的共享钥匙串（设计 12.6、30.3.1）：App 写入各监控中心的 X25519 私钥，Notification Service Extension 读取。
// 访问组 = Info.plist 的 VpsmonKeychainGroup（构建时展开为 $(AppIdentifierPrefix)dev.vpsmon.shared）。
// kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly：锁屏时扩展也能读取；不随备份迁移到其他设备。
import Foundation
import Security

enum PushKeyStore {
    static let service = "dev.vpsmon.push"
    static let account = "push_keys_v1"

    static var accessGroup: String? {
        let g = Bundle.main.object(forInfoDictionaryKey: "VpsmonKeychainGroup") as? String
        return (g?.isEmpty ?? true) ? nil : g
    }

    private static func baseQuery() -> [String: Any] {
        var q: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                kSecAttrService as String: service,
                                kSecAttrAccount as String: account]
        if let g = accessGroup { q[kSecAttrAccessGroup as String] = g }
        return q
    }

    /// 读取全部私钥（每个监控中心一个）；没有时返回空数组。
    static func load() -> [Data] {
        var q = baseQuery()
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data,
              let list = try? JSONDecoder().decode([String].self, from: data) else { return [] }
        return list.compactMap { Data(base64Encoded: $0) }
    }

    /// 整体替换私钥列表（App 在配对、解除、切换中心后调用）。返回 OSStatus。
    @discardableResult
    static func save(_ keysBase64: [String]) -> OSStatus {
        let data = (try? JSONEncoder().encode(keysBase64)) ?? Data("[]".utf8)
        SecItemDelete(baseQuery() as CFDictionary)
        if keysBase64.isEmpty { return errSecSuccess }
        var q = baseQuery()
        q[kSecValueData as String] = data
        q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        return SecItemAdd(q as CFDictionary, nil)
    }
}
