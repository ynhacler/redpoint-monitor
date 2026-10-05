import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/push_crypto.dart';

// 与面板（Go 标准库 crypto/hpke）的互通：解密面板生成的测试向量（internal/push/testdata/vectors.json）
void main() {
  final vectors = (jsonDecode(File('../internal/push/testdata/vectors.json').readAsStringSync()) as List).cast<Map<String, dynamic>>();

  test('解密面板生成的全部测试向量（含空消息与跨档位的长消息）', () async {
    expect(vectors, isNotEmpty);
    for (final v in vectors) {
      final plain = await openPush(base64.decode(v['private_key'] as String), base64.decode(v['sealed'] as String));
      expect(utf8.decode(plain), v['plaintext']);
    }
  });

  test('解析通知内容', () async {
    final v = vectors.first;
    final m = await decodePush(base64.decode(v['private_key'] as String), v['sealed'] as String);
    expect(m.title, '🔴 DMIT-HK 已离线');
    expect(m.body, '超过 120 秒未收到上报');
  });

  test('【安全】篡改或错误的私钥无法解密', () async {
    final v = vectors.first;
    final sealed = base64.decode(v['sealed'] as String);
    final bad = [...sealed]..[sealed.length - 1] ^= 1;
    await expectLater(openPush(base64.decode(v['private_key'] as String), bad), throwsA(anything));
    final (other, _) = await generatePushKeyPair();
    await expectLater(openPush(other, sealed), throwsA(anything));
  });

  test('生成的公钥可以由私钥推出（32 字节）', () async {
    final (priv, pub) = await generatePushKeyPair();
    expect(priv.length, 32);
    expect(pub.length, 32);
  });
}
