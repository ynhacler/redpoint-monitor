// 本机 IP 查询（修订第 219 条）：从手机当前网络请求几个公开的 IP 查询服务，看国内、国外网站分别看到的出口 IP 与位置，
// 用来判断代理分流是否生效（国内直连、国外走代理时两边的 IP 不同）。
// 【隐私】查询在手机上直接进行，不经过面板；只在打开“网络 → 本机 IP”或点刷新时请求，结果不保存（约束 7）。
// 服务都无需 API Key、走 HTTPS；解析为纯函数，便于测试。
import 'dart:async';
import 'dart:convert';
import 'dart:io';

/// 一个查询服务的结果
class IpResult {
  /// 创建结果
  const IpResult({required this.ip, this.country = '', this.region = '', this.city = '', this.isp = '', this.asn = ''});

  /// 出口 IP
  final String ip;

  /// 国家 / 地区：两位代码（国际服务）或中文名（国内服务）
  final String country;

  /// 省 / 州
  final String region;

  /// 城市
  final String city;

  /// 运营商 / 组织
  final String isp;

  /// 如 AS906
  final String asn;

  /// 位置：国家 地区 城市（去掉空项与重复）
  String get place {
    final out = <String>[];
    for (final x in [country, region, city]) {
      if (x.isNotEmpty && !out.contains(x)) out.add(x);
    }
    return out.join(' ');
  }

  /// 一行说明：位置 ASN 运营商
  String get summary => [place, asn, isp].where((x) => x.isNotEmpty).join(' ');

  /// 两位国家代码（没有时为空）
  String get countryCode => RegExp(r'^[A-Za-z]{2}$').hasMatch(country) ? country.toUpperCase() : '';
}

/// 一个查询服务
class IpSource {
  /// 创建
  const IpSource(this.id, this.name, this.url, this.domestic, this.parse, {this.headers = const {}});

  /// 标识
  final String id;

  /// 名称
  final String name;

  /// 查询地址
  final String url;

  /// 国内服务（国内网站看到的 IP）
  final bool domestic;

  /// 解析响应正文；格式不对时抛出 FormatException
  final IpResult Function(String body) parse;

  /// 额外的请求头
  final Map<String, String> headers;
}

Map<String, dynamic> _obj(String body) {
  final j = jsonDecode(body);
  if (j is! Map<String, dynamic>) throw const FormatException('不是 JSON 对象');
  return j;
}

String _s(Object? v) => v == null ? '' : '$v'.trim();

String _ip(String ip) {
  if (InternetAddress.tryParse(ip) == null) throw FormatException('不是 IP 地址：$ip');
  return ip;
}

/// Cloudflare（speed.cloudflare.com/meta）：{clientIp, country, region, city, asn, asOrganization}
IpResult parseCloudflare(String body) {
  final j = _obj(body);
  final asn = _s(j['asn']);
  return IpResult(ip: _ip(_s(j['clientIp'])), country: _s(j['country']), region: _s(j['region']), city: _s(j['city']),
      isp: _s(j['asOrganization']), asn: asn.isEmpty ? '' : 'AS$asn');
}

/// IPinfo.io：{ip, city, region, country, org: "AS906 DMIT Cloud Services"}
IpResult parseIpinfo(String body) {
  final j = _obj(body);
  final org = _s(j['org']);
  final m = RegExp(r'^(AS\d+)\s*(.*)$').firstMatch(org);
  return IpResult(ip: _ip(_s(j['ip'])), country: _s(j['country']), region: _s(j['region']), city: _s(j['city']),
      asn: m?.group(1) ?? '', isp: m?.group(2) ?? org);
}

/// IP.SB（api.ip.sb/geoip）：{ip, country_code, region, city, asn, isp, asn_organization}
IpResult parseIpSb(String body) {
  final j = _obj(body);
  final asn = _s(j['asn']);
  return IpResult(ip: _ip(_s(j['ip'])), country: _s(j['country_code']), region: _s(j['region']), city: _s(j['city']),
      isp: _s(j['isp']).isNotEmpty ? _s(j['isp']) : _s(j['asn_organization']), asn: asn.isEmpty ? '' : 'AS$asn');
}

/// IPIP.net（myip.ipip.net/json）：{ret: ok, data: {ip, location: [国家, 省, 市, 机构, 运营商]}}
IpResult parseIpip(String body) {
  final j = _obj(body);
  if (j['ret'] != 'ok') throw const FormatException('查询失败');
  final d = j['data'] as Map<String, dynamic>;
  final loc = [for (final x in (d['location'] as List<dynamic>? ?? const [])) _s(x)];
  String at(int i) => i < loc.length ? loc[i] : '';
  return IpResult(ip: _ip(_s(d['ip'])), country: at(0), region: at(1), city: at(2), isp: [at(3), at(4)].where((x) => x.isNotEmpty).join(' '));
}

/// 哔哩哔哩（api.bilibili.com/x/web-interface/zone）：{code: 0, data: {addr, country, province, city, isp}}
IpResult parseBilibili(String body) {
  final j = _obj(body);
  if (j['code'] != 0) throw const FormatException('查询失败');
  final d = j['data'] as Map<String, dynamic>;
  return IpResult(ip: _ip(_s(d['addr'])), country: _s(d['country']), region: _s(d['province']), city: _s(d['city']), isp: _s(d['isp']));
}

/// 查询服务：国内两个、国际三个（与常见的“本机 IP 查询”页一样分国内 / 国际对比）
final ipSources = <IpSource>[
  const IpSource('ipip', 'IPIP.net', 'https://myip.ipip.net/json', true, parseIpip),
  const IpSource('bilibili', '哔哩哔哩', 'https://api.bilibili.com/x/web-interface/zone', true, parseBilibili),
  const IpSource('cloudflare', 'Cloudflare', 'https://speed.cloudflare.com/meta', false, parseCloudflare,
      headers: {'Referer': 'https://speed.cloudflare.com/'}), // 不带 Referer 时返回空对象
  const IpSource('ipinfo', 'IPinfo.io', 'https://ipinfo.io/json', false, parseIpinfo),
  const IpSource('ipsb', 'IP.SB', 'https://api.ip.sb/geoip', false, parseIpSb),
];

/// 只有 IPv6 地址的查询地址：能访问说明本机有 IPv6 出口
const ipv6Probe = 'https://api6.ipify.org?format=json';

/// 查询某个 IP 的位置（IPv6 用）
String ipSbLookup(String ip) => 'https://api.ip.sb/geoip/$ip';

/// 发一个 GET 请求并返回正文；超时 8 秒。可在测试中替换。
typedef Fetch = Future<String> Function(String url, Map<String, String> headers);

/// 默认的请求实现：每次查询用新的 HttpClient（不复用连接），不带 Cookie
Future<String> httpFetch(String url, Map<String, String> headers) async {
  final client = HttpClient()
    ..connectionTimeout = const Duration(seconds: 8)
    ..userAgent = 'Mozilla/5.0 (Linux; Android) vpsmon-app';
  try {
    final req = await client.getUrl(Uri.parse(url)).timeout(const Duration(seconds: 8));
    headers.forEach(req.headers.set);
    final res = await req.close().timeout(const Duration(seconds: 8));
    final body = await res.transform(utf8.decoder).join().timeout(const Duration(seconds: 8));
    if (res.statusCode != 200) throw HttpException('HTTP ${res.statusCode}');
    return body;
  } finally {
    client.close(force: true);
  }
}

/// 查询本机 IPv6：没有 IPv6 出口时返回 null
Future<IpResult?> lookupIpv6(Fetch fetch) async {
  final String ip;
  try {
    ip = _ip(_s(_obj(await fetch(ipv6Probe, const {}))['ip']));
  } catch (_) {
    return null;
  }
  if (!ip.contains(':')) return null;
  try {
    return parseIpSb(await fetch(ipSbLookup(ip), const {}));
  } catch (_) {
    return IpResult(ip: ip); // 有 IPv6，但位置没有查到
  }
}
