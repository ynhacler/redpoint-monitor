import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/app_lock.dart';

/// 按顺序返回预设结果的验证器
class FakeVerifier implements Verifier {
  FakeVerifier(this.results);
  final List<String?> results;
  int calls = 0;
  @override
  Future<String?> verify(String reason) async => results[calls++ % results.length];
}

void main() {
  test('打开开关前先验证：失败不改设置；成功保存', () async {
    final store = MemoryLockStore();
    final c = AppLockController(store: store, verifier: FakeVerifier(['本机还没有设置锁屏密码或生物识别', null]));
    await c.load();
    expect(await c.set(onOpen: true), '本机还没有设置锁屏密码或生物识别');
    expect(c.settings.onOpen, isFalse);
    expect(await c.set(onOpen: true), isNull);
    expect(store.value.onOpen, isTrue);
    // 关闭不需要验证
    expect(await c.set(onOpen: false), isNull);
    expect(store.value.onOpen, isFalse);
  });

  test('启动时锁定；后台不足 60 秒回来不锁，超过则锁', () async {
    var now = DateTime(2026, 10, 13, 9);
    final store = MemoryLockStore()..value = LockSettings(onOpen: true);
    final c = AppLockController(store: store, verifier: FakeVerifier([null]), now: () => now);
    await c.load();
    expect(c.locked, isTrue);
    expect(await c.unlock(), isNull);
    expect(c.locked, isFalse);
    c.paused();
    now = now.add(const Duration(seconds: 30));
    c.resumed();
    expect(c.locked, isFalse);
    c.paused();
    now = now.add(const Duration(seconds: 61));
    c.resumed();
    expect(c.locked, isTrue);
  });

  test('敏感操作：未开启时直接通过；开启后按验证结果', () async {
    final v = FakeVerifier(['', null]);
    final c = AppLockController(store: MemoryLockStore(), verifier: v);
    await c.load();
    expect(await c.guard('静音'), isTrue);
    expect(v.calls, 0);
    c.settings.sensitive = true;
    expect(await c.guard('静音'), isFalse); // 用户取消
    expect(await c.guard('静音'), isTrue);
  });

  testWidgets('锁定时盖住内容，解锁后显示；验证失败显示原因', (tester) async {
    final store = MemoryLockStore()..value = LockSettings(onOpen: true);
    final v = FakeVerifier(['验证失败：lockout', null]);
    final c = AppLockController(store: store, verifier: v);
    await tester.pumpWidget(MaterialApp(
      builder: (context, child) => AppLockGate(controller: c, child: child!),
      home: const Scaffold(body: Text('秘密内容')),
    ));
    await tester.pumpAndSettle();
    // 加载后自动弹出验证（第一次失败）：仍锁定并显示原因
    expect(find.text('VPS Monitor 已锁定'), findsOneWidget);
    expect(find.text('验证失败：lockout'), findsOneWidget);
    await tester.tap(find.text('解锁'));
    await tester.pumpAndSettle();
    expect(find.text('VPS Monitor 已锁定'), findsNothing);
    expect(find.text('秘密内容'), findsOneWidget);
  });
}
