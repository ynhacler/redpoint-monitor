import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/ip_lookup.dart';
import 'package:vpsmon_app/pages/ip_page.dart';

// 各服务的真实响应格式（IP 换成文档地址）
const _cf = '{"hostname":"speed.cloudflare.com","clientIp":"203.0.113.55","asn":906,"asOrganization":"Private Customer","country":"HK",'
    '"city":"Kwai Chung","colo":{"iata":"HKG"}}';
const _ipinfo = '{"ip":"203.0.113.55","city":"Kwai Chung","region":"Kwai Tsing District","country":"HK","org":"AS906 DMIT Cloud Services"}';
const _ipsb = '{"region":"Kwai Tsing District","isp":"DMIT","city":"Kwai Chung","asn_organization":"DMIT Cloud Services","asn":906,'
    '"ip":"203.0.113.55","country":"Hong Kong","country_code":"HK"}';
const _ipip = '{"ret":"ok","data":{"ip":"198.51.100.7","location":["中国","广东","深圳","","电信"]}}';
const _bili = '{"code":0,"message":"OK","data":{"addr":"198.51.100.7","country":"中国","province":"广东","city":"深圳","isp":"电信"}}';

void main() {
  test('解析各服务的响应', () {
    final cf = parseCloudflare(_cf);
    expect([cf.ip, cf.countryCode, cf.asn, cf.isp, cf.summary], ['203.0.113.55', 'HK', 'AS906', 'Private Customer', 'HK Kwai Chung AS906 Private Customer']);
    final info = parseIpinfo(_ipinfo);
    expect([info.asn, info.isp, info.place], ['AS906', 'DMIT Cloud Services', 'HK Kwai Tsing District Kwai Chung']);
    expect(parseIpSb(_ipsb).asn, 'AS906');
    final ipip = parseIpip(_ipip);
    expect([ipip.ip, ipip.place, ipip.isp, ipip.countryCode], ['198.51.100.7', '中国 广东 深圳', '电信', '']);
    expect(parseBilibili(_bili).summary, '中国 广东 深圳 电信');
    // 格式不对、不是 IP：抛出 FormatException
    expect(() => parseCloudflare('{}'), throwsFormatException);
    expect(() => parseIpip('{"ret":"err"}'), throwsFormatException);
    expect(() => parseIpinfo('<html>'), throwsFormatException);
  });

  test('IPv6：探测地址不可达时为 null；查到地址但位置失败时只有地址', () async {
    expect(await lookupIpv6((url, _) async => throw Exception('no v6')), isNull);
    final r = await lookupIpv6((url, _) async {
      if (url == ipv6Probe) return '{"ip":"2001:db8::1"}';
      throw Exception('lookup failed');
    });
    expect(r?.ip, '2001:db8::1');
  });

  testWidgets('本机 IP 页：IPv4 卡片、各服务结果、国内外 IP 不同时提示分流生效、没有 IPv6', (tester) async {
    tester.view.physicalSize = const Size(375 * 3, 812 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    final asked = <String, Map<String, String>>{};
    Future<String> fetch(String url, Map<String, String> headers) async {
      asked[url] = headers;
      if (url.contains('cloudflare')) return _cf;
      if (url.contains('ipinfo')) return _ipinfo;
      if (url.contains('ip.sb')) return _ipsb;
      if (url.contains('ipip')) return _ipip;
      if (url.contains('bilibili')) return jsonEncode({'code': -1});
      throw Exception('unreachable'); // IPv6 探测
    }

    await tester.pumpWidget(MaterialApp(home: Scaffold(body: IpPage(fetch: fetch))));
    await tester.pumpAndSettle();
    expect(asked['https://speed.cloudflare.com/meta']?['Referer'], 'https://speed.cloudflare.com/');
    expect(find.text('203.0.113.55'), findsOneWidget); // 主卡片取国际服务（Cloudflare）
    expect(find.text('AS906 Private Customer'), findsOneWidget);
    expect(find.textContaining('分流已生效'), findsOneWidget);
    expect(find.text('你的网络可能不支持 IPv6'), findsOneWidget);
    expect(find.textContaining('查询失败'), findsOneWidget); // 哔哩哔哩返回错误
    await tester.scrollUntilVisible(find.text('IP.SB'), 200, scrollable: find.byType(Scrollable).first);
    expect(find.text('国内'), findsNWidgets(2));
    expect(tester.takeException(), isNull);
  });

  testWidgets('隐私模式下 IP 打码', (tester) async {
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: IpPage(privacy: true, fetch: (url, _) async => url.contains('cloudflare') ? _cf : throw Exception('x')))));
    await tester.pumpAndSettle();
    expect(find.text('203.***.***.55'), findsWidgets);
    expect(find.text('203.0.113.55'), findsNothing);
  });
}
