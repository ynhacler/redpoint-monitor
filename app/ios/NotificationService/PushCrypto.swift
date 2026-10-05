// 推送的端到端解密（设计 30.3）：与面板 internal/push、App 的 push_crypto.dart 同一套参数。
//
//   HPKE（RFC 9180）Base 模式，DHKEM(X25519, HKDF-SHA256) / HKDF-SHA256 / ChaCha20-Poly1305，info = "vpsmon push v1"
//   密文 = enc（32 字节）‖ AEAD 密文；明文 = 2 字节大端长度 ‖ 内容 ‖ 0 填充
//
// 使用系统 CryptoKit 的 HPKE（iOS 17 起），不自行拼装。私钥由 App 写入与扩展共享的钥匙串（只在本机）。
import CryptoKit
import Foundation

enum PushCrypto {
    static let info = Data("vpsmon push v1".utf8)

    enum Failure: Error { case tooShort, badPadding }

    /// 解密并去补齐，返回明文。
    static func open(privateKey raw: Data, sealed: Data) throws -> Data {
        guard sealed.count >= 32 + 16 else { throw Failure.tooShort }
        let key = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: raw)
        var recipient = try HPKE.Recipient(privateKey: key, ciphersuite: .Curve25519_SHA256_ChachaPoly,
                                           info: info, encapsulatedKey: sealed.prefix(32))
        let padded = try recipient.open(sealed.dropFirst(32))
        guard padded.count >= 2 else { throw Failure.badPadding }
        let bytes = [UInt8](padded)
        let n = Int(bytes[0]) << 8 | Int(bytes[1])
        guard n <= bytes.count - 2 else { throw Failure.badPadding }
        return Data(bytes[2..<(2 + n)])
    }

    /// 依次用本机保存的各监控中心私钥尝试解密（多监控中心，设计 12.8）；都失败时返回 nil，显示兜底文字。
    static func open(privateKeys: [Data], sealed: Data) -> Data? {
        for k in privateKeys {
            if let p = try? open(privateKey: k, sealed: sealed) { return p }
        }
        return nil
    }
}

/// 推送明文（面板的 pushPayload）。
struct PushMessage: Decodable {
    let center_id: String?
    let server_id: Int?
    let server_name: String?
    let kind: String?
    let severity: String?
    let title: String
    let body: String?
    let ts: Int?
}
