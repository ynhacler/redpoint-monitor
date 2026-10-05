// 推送的端到端加密（设计 30.3）：与面板的 internal/push 同一套参数。
//
//   HPKE（RFC 9180）Base 模式，DHKEM(X25519, HKDF-SHA256) / HKDF-SHA256 / ChaCha20-Poly1305，info = "vpsmon push v1"
//   密文 = enc（32 字节）‖ AEAD 密文；明文 = 2 字节大端长度 ‖ 内容 ‖ 0 填充
//
// Dart 没有现成的 HPKE 实现：按 RFC 9180 第 4、5 节用 cryptography 包的 X25519、HMAC-SHA256、ChaCha20-Poly1305
// 组合，只实现接收方的单次解密；正确性由面板生成的测试向量保证（internal/push/testdata/vectors.json）。
// 【安全】设备私钥只保存在系统安全存储中，不离开设备（设计 12.3、30.3.1）。
import 'dart:convert';
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart';

const pushInfo = 'vpsmon push v1';

final _hmac = Hmac.sha256();
final _x25519 = X25519();
final _aead = Chacha20.poly1305Aead();

Uint8List _i2osp(int v, int n) {
  final out = Uint8List(n);
  for (var i = n - 1; i >= 0; i--) {
    out[i] = v & 0xff;
    v >>= 8;
  }
  return out;
}

Uint8List _concat(List<List<int>> parts) => Uint8List.fromList([for (final p in parts) ...p]);

// suite_id（RFC 9180 4.1、5.1）
final _kemSuite = _concat([utf8.encode('KEM'), _i2osp(0x0020, 2)]);
final _hpkeSuite = _concat([utf8.encode('HPKE'), _i2osp(0x0020, 2), _i2osp(0x0001, 2), _i2osp(0x0003, 2)]);

Future<List<int>> _extract(List<int> salt, List<int> ikm) async {
  final key = salt.isEmpty ? Uint8List(32) : salt; // HKDF：空盐等同于 HashLen 个 0
  return (await _hmac.calculateMac(ikm, secretKey: SecretKey(key))).bytes;
}

Future<List<int>> _expand(List<int> prk, List<int> info, int length) async {
  final out = <int>[];
  var t = <int>[];
  for (var i = 1; out.length < length; i++) {
    t = (await _hmac.calculateMac([...t, ...info, i], secretKey: SecretKey(prk))).bytes;
    out.addAll(t);
  }
  return out.sublist(0, length);
}

Future<List<int>> _labeledExtract(List<int> suite, List<int> salt, String label, List<int> ikm) =>
    _extract(salt, _concat([utf8.encode('HPKE-v1'), suite, utf8.encode(label), ikm]));

Future<List<int>> _labeledExpand(List<int> suite, List<int> prk, String label, List<int> info, int length) =>
    _expand(prk, _concat([_i2osp(length, 2), utf8.encode('HPKE-v1'), suite, utf8.encode(label), info]), length);

/// 生成设备的 X25519 密钥对：返回（私钥 32 字节，公钥 32 字节）。
Future<(List<int>, List<int>)> generatePushKeyPair() async {
  final kp = await _x25519.newKeyPair();
  final priv = await kp.extractPrivateKeyBytes();
  final pub = (await kp.extractPublicKey()).bytes;
  return (priv, pub);
}

/// 解密面板发来的推送（enc ‖ 密文），返回去补齐后的明文。失败时抛出异常（显示兜底文字）。
Future<List<int>> openPush(List<int> privateKey, List<int> sealed) async {
  if (sealed.length < 32 + 16) throw const FormatException('密文过短');
  final enc = sealed.sublist(0, 32);
  final ct = sealed.sublist(32);

  // DHKEM Decap（RFC 9180 4.1）
  final kp = await _x25519.newKeyPairFromSeed(privateKey);
  final pkR = (await kp.extractPublicKey()).bytes;
  final dh = await (await _x25519.sharedSecretKey(keyPair: kp, remotePublicKey: SimplePublicKey(enc, type: KeyPairType.x25519)))
      .extractBytes();
  final eaePrk = await _labeledExtract(_kemSuite, const [], 'eae_prk', dh);
  final shared = await _labeledExpand(_kemSuite, eaePrk, 'shared_secret', [...enc, ...pkR], 32);

  // KeySchedule，Base 模式（RFC 9180 5.1）：psk 与 psk_id 为空
  final pskIdHash = await _labeledExtract(_hpkeSuite, const [], 'psk_id_hash', const []);
  final infoHash = await _labeledExtract(_hpkeSuite, const [], 'info_hash', utf8.encode(pushInfo));
  final context = [0x00, ...pskIdHash, ...infoHash];
  final secret = await _labeledExtract(_hpkeSuite, shared, 'secret', const []);
  final key = await _labeledExpand(_hpkeSuite, secret, 'key', context, 32);
  final nonce = await _labeledExpand(_hpkeSuite, secret, 'base_nonce', context, 12); // 第一条消息 seq = 0

  final padded = await _aead.decrypt(
    SecretBox(ct.sublist(0, ct.length - 16), nonce: nonce, mac: Mac(ct.sublist(ct.length - 16))),
    secretKey: SecretKey(key),
  );
  if (padded.length < 2) throw const FormatException('明文格式不正确');
  final n = (padded[0] << 8) | padded[1];
  if (n > padded.length - 2) throw const FormatException('明文格式不正确');
  return padded.sublist(2, 2 + n);
}

/// 推送明文（面板的 pushPayload，设计 30.3.2）。
class PushMessage {
  PushMessage({required this.centerId, required this.title, required this.body, required this.severity, required this.kind,
      this.serverId, this.serverName, this.eventId, required this.ts});
  final String centerId;
  final String title;
  final String body;
  final String severity;
  final String kind;
  final int? serverId;
  final String? serverName;
  final int? eventId;
  final int ts;

  factory PushMessage.fromJson(Map<String, dynamic> j) => PushMessage(
        centerId: (j['center_id'] as String?) ?? '',
        title: (j['title'] as String?) ?? '服务器告警',
        body: (j['body'] as String?) ?? '',
        severity: (j['severity'] as String?) ?? '',
        kind: (j['kind'] as String?) ?? '',
        serverId: (j['server_id'] as num?)?.toInt(),
        serverName: j['server_name'] as String?,
        eventId: (j['event_id'] as num?)?.toInt(),
        ts: (j['ts'] as num?)?.toInt() ?? 0,
      );
}

/// 解密推送数据中的密文字段 c（APNs 自定义字段 / FCM data），得到通知内容。
Future<PushMessage> decodePush(List<int> privateKey, String ciphertextB64) async {
  final plain = await openPush(privateKey, base64.decode(ciphertextB64));
  return PushMessage.fromJson(jsonDecode(utf8.decode(plain)) as Map<String, dynamic>);
}
