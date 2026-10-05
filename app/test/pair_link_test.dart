import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/pair_link.dart';

const ak = 'MNT-7K2Q-ABCD-EFGH-JKMN-PQRS-TVWX-YZ01';

void main() {
  test('解析面板生成的二维码（参数顺序不限）', () {
    final t = parsePairLink('monitor://pair?ak=$ak&server=https%3A%2F%2Fmonitor.example.com', allowDevHttp: false);
    expect(t.server.toString(), 'https://monitor.example.com');
    expect(t.accessKey, ak);
    final t2 = parsePairLink('monitor://pair?server=https%3A%2F%2Fm.example.com%2Fpanel%2F&ak=${ak.toLowerCase()}', allowDevHttp: false);
    expect(t2.server.toString(), 'https://m.example.com/panel');
    expect(t2.accessKey, ak);
  });

  test('【安全】只接受 https；明文只在调试构建中允许本地开发地址', () {
    expect(() => parseServer('http://monitor.example.com', allowDevHttp: true), throwsFormatException);
    expect(() => parseServer('http://127.0.0.1:8080', allowDevHttp: false), throwsFormatException);
    expect(parseServer('http://10.0.2.2:8080', allowDevHttp: true).toString(), 'http://10.0.2.2:8080');
    expect(() => parseServer('https://user:pw@monitor.example.com', allowDevHttp: false), throwsFormatException);
    expect(() => parseServer('ftp://monitor.example.com', allowDevHttp: false), throwsFormatException);
    expect(parseServer(' monitor.example.com/ ', allowDevHttp: false).toString(), 'https://monitor.example.com');
  });

  test('拒绝其他二维码与不完整的配对信息', () {
    expect(() => parsePairLink('https://example.com', allowDevHttp: false), throwsFormatException);
    expect(() => parsePairLink('monitor://pair?ak=$ak', allowDevHttp: false), throwsFormatException);
    expect(() => parsePairLink('monitor://pair?server=https%3A%2F%2Fa.example.com&ak=dev_abc', allowDevHttp: false),
        throwsFormatException);
  });

  test('AK 规范化：忽略空白与大小写，检查格式', () {
    expect(parseAccessKey(' mnt-7k2q-abcd-efgh-jkmn-pqrs-tvwx-yz01 '), ak);
    expect(() => parseAccessKey('MNT-7K2Q'), throwsFormatException);
    expect(() => parseAccessKey(''), throwsFormatException);
  });
}
