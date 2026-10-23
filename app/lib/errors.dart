// 把请求失败的原因转成用户看得懂的一句话（修订第 211 条）：不直接显示 ClientException、SocketException 等原文，
// 只说明是哪一类问题与怎么排查。面板返回的错误（ApiException）本身就是中文说明，原样显示。
import 'dart:async';
import 'dart:io';

import 'package:http/http.dart' as http;

import 'api.dart';

/// 一句话说明失败原因。
String describeError(Object e) {
  if (e is ApiException) return e.message;
  if (e is DeviceRevoked) return e.toString();
  if (e is TimeoutException) return '连接面板超时：网络较慢或面板没有响应';
  if (e is HandshakeException || e is TlsException) return '面板的 HTTPS 证书无效，已拒绝连接';
  if (e is SocketException) return _socket(e.message, e.osError?.errorCode);
  if (e is http.ClientException) return _socket(e.message, null);
  if (e is FormatException) return '面板返回的数据无法识别，可能需要升级面板或 App';
  return '请求失败，请稍后再试';
}

// http 包把底层错误放在 message 中（如 “SocketException: Connection refused …”），按关键词归类
String _socket(String msg, int? code) {
  final m = msg.toLowerCase();
  if (m.contains('failed host lookup') || m.contains('no address associated')) return '找不到面板地址：域名解析失败，请检查网络或面板地址';
  if (m.contains('connection refused') || code == 111 || code == 61) return '无法连接面板：面板没有运行，或地址、端口不对';
  if (m.contains('network is unreachable') || m.contains('no route to host')) return '网络不可用，请检查手机的网络连接';
  if (m.contains('timed out')) return '连接面板超时：网络较慢或面板没有响应';
  if (m.contains('handshake') || m.contains('certificate')) return '面板的 HTTPS 证书无效，已拒绝连接';
  if (m.contains('connection reset') || m.contains('connection closed')) return '连接被中断，请稍后再试';
  return '无法连接面板，请检查网络';
}
