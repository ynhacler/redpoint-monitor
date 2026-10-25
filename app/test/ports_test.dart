import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/ports.dart';

void main() {
  test('监听端口（修订第 268 条，与 Web 同一规则）：范围、风险与排序', () {
    const redis = ListenPort(6379, 'tcp', ['0.0.0.0']);
    const ssh = ListenPort(22, 'tcp', ['0.0.0.0', '::']);
    const mysql = ListenPort(3306, 'tcp', ['127.0.0.1']);
    const wg = ListenPort(51820, 'udp', ['10.0.0.1']);
    const pub = ListenPort(8443, 'tcp', ['203.0.113.9']);
    expect([exposure(redis), exposure(mysql), exposure(wg), exposure(pub)], ['public', 'local', 'private', 'public']);
    expect([scopeText(ssh), scopeText(pub), scopeText(wg), scopeText(mysql)], ['全部地址', '公网地址', '内网', '仅本机']);
    expect([isRisky(redis), isRisky(mysql), isRisky(ssh)], [true, false, false]);
    expect(serviceName(ssh), 'SSH');
    expect([for (final p in sortPorts([mysql, ssh, wg, redis, pub])) p.port], [6379, 22, 8443, 51820, 3306]);
  });
}
