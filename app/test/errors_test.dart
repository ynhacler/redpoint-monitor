import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:vpsmon_app/api.dart';
import 'package:vpsmon_app/centers.dart';
import 'package:vpsmon_app/errors.dart';

void main() {
  test('describeError：网络错误转成一句话，不显示异常原文', () {
    final refused = http.ClientException(
        'SocketException: Connection refused (OS Error: Connection refused, errno = 111), address = 10.0.2.2, port = 51060',
        Uri.parse('http://10.0.2.2:8094/api/v1/app/token/refresh'));
    expect(describeError(refused), '无法连接面板：面板没有运行，或地址、端口不对');
    expect(describeError(http.ClientException('Failed host lookup: monitor.example.com')), contains('域名解析失败'));
    expect(describeError(const SocketException('Network is unreachable')), contains('网络不可用'));
    expect(describeError(const SocketException('x', osError: OSError('', 111))), contains('无法连接面板'));
    expect(describeError(TimeoutException('t')), contains('超时'));
    expect(describeError(const HandshakeException('CERTIFICATE_VERIFY_FAILED')), contains('证书无效'));
    expect(describeError(const FormatException('bad json')), contains('无法识别'));
    expect(describeError(StateError('?')), '请求失败，请稍后再试');
    for (final e in [refused, http.ClientException('weird')]) {
      expect(describeError(e), isNot(contains('Exception')));
    }
  });

  test('describeError：面板返回的中文说明原样显示', () {
    expect(describeError(ApiException(429, 'rate_limited', '请求过于频繁')), '请求过于频繁');
    expect(describeError(DeviceRevoked()), contains('重新'));
  });

  test('centerLabel：默认端口只显示主机，其他端口带上端口', () {
    expect(centerLabel(Uri.parse('https://monitor.example.com')), 'monitor.example.com');
    expect(centerLabel(Uri.parse('https://monitor.example.com:8443')), 'monitor.example.com:8443');
    expect(centerLabel(Uri.parse('http://10.0.2.2:8080')), '10.0.2.2:8080');
    expect(centerLabel(Uri.parse('http://10.0.2.2')), '10.0.2.2');
  });
}
