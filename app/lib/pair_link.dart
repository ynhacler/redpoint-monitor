// 配对信息的解析与校验（设计 12.2、12.4）：二维码 monitor://pair?server=…&ak=…，或手工填写的地址与 AK。
//
// 【安全】
//   - 只接受 https:// 面板地址；明文 http 只在调试构建中、且仅限本地开发地址（模拟器访问本机面板）
//   - 不接受带用户名密码的地址；只读取 server 与 ak 两个参数，二维码不能携带其他凭证
//   - AK 只做格式检查，有效性由面板在线验证
import 'package:flutter/foundation.dart';

/// 规范化后的 AK：MNT- 加 7 组 4 个 Crockford Base32 字符（与面板一致）。
final _akFormat = RegExp(r'^MNT(-[0-9A-Z]{4}){7}$');

/// 本地开发地址：iOS 模拟器 127.0.0.1，Android 模拟器 10.0.2.2。
const _devHosts = {'127.0.0.1', 'localhost', '10.0.2.2'};

class PairTarget {
  const PairTarget(this.server, this.accessKey);

  /// 面板地址，如 https://monitor.example.com（不以 / 结尾）
  final Uri server;
  final String accessKey;
}

/// 校验并规范化面板地址；不合法时抛出 [FormatException]，message 可直接展示给用户。
/// [allowDevHttp] 默认只在调试构建中为 true。
Uri parseServer(String input, {bool allowDevHttp = kDebugMode}) {
  var s = input.trim();
  if (s.isEmpty) throw const FormatException('请填写面板地址');
  if (!s.contains('://')) s = 'https://$s';
  final uri = Uri.tryParse(s);
  if (uri == null || uri.host.isEmpty) throw const FormatException('面板地址格式不正确');
  if (uri.userInfo.isNotEmpty) throw const FormatException('面板地址不能包含用户名或密码');
  final devHttp = allowDevHttp && uri.scheme == 'http' && _devHosts.contains(uri.host);
  if (uri.scheme != 'https' && !devHttp) throw const FormatException('只支持 https:// 地址');
  if (uri.hasQuery || uri.hasFragment) throw const FormatException('面板地址不能包含 ? 或 #');
  final path = uri.path.replaceAll(RegExp(r'/+$'), '');
  return uri.replace(path: path);
}

/// 规范化 AK：去掉空白、转为大写；格式不对时抛出 [FormatException]。
String parseAccessKey(String input) {
  final ak = input.replaceAll(RegExp(r'\s'), '').toUpperCase();
  if (ak.isEmpty) throw const FormatException('请填写 AK');
  if (!_akFormat.hasMatch(ak)) throw const FormatException('AK 格式不正确，应为 MNT- 开头的 8 段');
  return ak;
}

/// 解析二维码内容 monitor://pair?server=…&ak=…（设计 12.4）。
PairTarget parsePairLink(String raw, {bool allowDevHttp = kDebugMode}) {
  final uri = Uri.tryParse(raw.trim());
  if (uri == null || uri.scheme != 'monitor' || uri.host != 'pair') {
    throw const FormatException('不是 VPS Monitor 的配对二维码');
  }
  final server = uri.queryParameters['server'];
  final ak = uri.queryParameters['ak'];
  if (server == null || ak == null) throw const FormatException('配对二维码缺少面板地址或 AK');
  return PairTarget(parseServer(server, allowDevHttp: allowDevHttp), parseAccessKey(ak));
}
