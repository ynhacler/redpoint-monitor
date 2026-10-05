// iOS 推送解密的互通检查（设计 30.3）：用 CryptoKit 解密面板（Go）生成的测试向量，并检查多中心与篡改。
// 用法：make ios-push-check（只在 macOS 上运行，需要 Xcode 命令行工具）
import CryptoKit
import Foundation

let path = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "internal/push/testdata/vectors.json"
struct V: Decodable { let private_key: String; let sealed: String; let plaintext: String }
let vs = try JSONDecoder().decode([V].self, from: Data(contentsOf: URL(fileURLWithPath: path)))
var bad = 0
func fail(_ m: String) { bad += 1; print("✗ " + m) }
for v in vs {
    let p = try PushCrypto.open(privateKey: Data(base64Encoded: v.private_key)!, sealed: Data(base64Encoded: v.sealed)!)
    if String(data: p, encoding: .utf8) != v.plaintext { fail("向量解密结果不一致") }
}
let v0 = vs[0]
let key = Data(base64Encoded: v0.private_key)!, sealed = Data(base64Encoded: v0.sealed)!
let other = Curve25519.KeyAgreement.PrivateKey().rawRepresentation
if PushCrypto.open(privateKeys: [other, key], sealed: sealed).flatMap({ String(data: $0, encoding: .utf8) }) != v0.plaintext {
    fail("多监控中心：应依次尝试各私钥")
}
var tampered = sealed
tampered[tampered.count - 1] ^= 1
if PushCrypto.open(privateKeys: [key], sealed: tampered) != nil { fail("篡改后不应能解密") }
if (try? JSONDecoder().decode(PushMessage.self, from: PushCrypto.open(privateKey: key, sealed: sealed)))?.title != "🔴 DMIT-HK 已离线" {
    fail("通知内容解析")
}
if bad > 0 { exit(1) }
print("✓ iOS 推送解密与面板互通（\(vs.count) 个向量）")
