import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/cache.dart';
import 'package:vpsmon_app/models.dart';

void main() {
  late Directory dir;
  setUp(() async => dir = await Directory.systemTemp.createTemp('vpsmon_cache'));
  tearDown(() async => dir.delete(recursive: true));

  final j = {
    'id': 5, 'name': 'hk', 'status': 'online',
    'traffic': {'cycle_start': '2026-10-01', 'cycle_end': '2026-11-01', 'used': 1, 'limit': 0, 'unit': 'decimal'},
  };

  test('节点列表与历史曲线：写入后重新打开可读回（设计 1.5.10）', () async {
    final at = DateTime.fromMillisecondsSinceEpoch(1700000000000);
    final c = FileCacheStore(dir: () async => dir);
    await c.saveServers([ServerView.fromJson(j)], at);
    await c.saveHistory(5, '1h', [{'ts': 1, 'cpu': 3, 'mem_used': 1, 'mem_total': 2, 'disk_used': 0, 'disk_total': 0, 'rx_speed': 0, 'tx_speed': 0}], at);

    final again = FileCacheStore(dir: () async => dir);
    final s = await again.servers();
    expect(s!.value.single.name, 'hk');
    expect(s.at, at);
    expect((await again.history(5, '1h'))!.value.single.mem, 50);
    expect(await again.history(5, '24h'), isNull);

    await again.clear();
    expect(await FileCacheStore(dir: () async => dir).servers(), isNull);
  });

  test('历史缓存有上限，丢弃最早的', () async {
    final c = FileCacheStore(dir: () async => dir);
    for (var i = 0; i < 35; i++) {
      await c.saveHistory(i, '1h', [], DateTime.now());
    }
    expect(await c.history(0, '1h'), isNull);
    expect(await c.history(34, '1h'), isNotNull);
  });

  test('损坏的缓存文件当作没有缓存', () async {
    await File('${dir.path}/cache_v1.json').writeAsString('{not json');
    expect(await FileCacheStore(dir: () async => dir).servers(), isNull);
  });
}
