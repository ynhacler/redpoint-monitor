// 生物识别锁（设计 1.5.11，修订第 153 条）：Face ID / Touch ID / Android 生物识别（无生物识别时可用设备密码）。
//
// 两个开关，保存在本机（所有监控中心共用，不含凭证）：
//   打开 App 时验证：启动时与从后台回来超过 60 秒时显示锁屏，验证后才显示内容
//   敏感操作前验证：静音 / 维护、解除配对前先验证
// 打开开关前先验证一次：设备不支持或验证失败时不打开，避免把自己锁在外面。
// 【安全】这是本机的二次保护：设备凭证仍只在 flutter_secure_storage 中，锁屏不改变凭证的存储方式。
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:local_auth/local_auth.dart';
import 'package:path_provider/path_provider.dart';

/// 锁屏设置
class LockSettings {
  LockSettings({this.onOpen = false, this.sensitive = false});
  bool onOpen;
  bool sensitive;

  Map<String, dynamic> toJson() => {'on_open': onOpen, 'sensitive': sensitive};

  factory LockSettings.fromJson(Map<String, dynamic> j) =>
      LockSettings(onOpen: (j['on_open'] as bool?) ?? false, sensitive: (j['sensitive'] as bool?) ?? false);
}

/// 锁屏设置的存储（测试中用内存实现）
abstract class LockStore {
  Future<LockSettings> load();
  Future<void> save(LockSettings s);
}

class FileLockStore implements LockStore {
  const FileLockStore();

  Future<File> _file() async => File('${(await getApplicationSupportDirectory()).path}/lock_v1.json');

  @override
  Future<LockSettings> load() async {
    try {
      return LockSettings.fromJson(jsonDecode(await (await _file()).readAsString()) as Map<String, dynamic>);
    } catch (_) {
      return LockSettings();
    }
  }

  @override
  Future<void> save(LockSettings s) async {
    final f = await _file();
    await f.parent.create(recursive: true);
    await f.writeAsString(jsonEncode(s.toJson()));
  }
}

class MemoryLockStore implements LockStore {
  LockSettings value = LockSettings();
  @override
  Future<LockSettings> load() async => value;
  @override
  Future<void> save(LockSettings s) async => value = s;
}

/// 本机验证（测试中替换）
abstract class Verifier {
  /// 验证成功返回 null；失败返回说明（用户取消时为空字符串，不提示）
  Future<String?> verify(String reason);
}

class LocalVerifier implements Verifier {
  LocalVerifier();
  final _auth = LocalAuthentication();

  @override
  Future<String?> verify(String reason) async {
    try {
      if (!await _auth.isDeviceSupported()) return '本机不支持生物识别或设备密码';
      // biometricOnly 为 false：没有录入生物识别时可用设备密码
      return await _auth.authenticate(localizedReason: reason, persistAcrossBackgrounding: true) ? null : '';
    } on LocalAuthException catch (e) {
      return switch (e.code) {
        LocalAuthExceptionCode.userCanceled || LocalAuthExceptionCode.systemCanceled || LocalAuthExceptionCode.timeout => '',
        LocalAuthExceptionCode.noCredentialsSet => '本机还没有设置锁屏密码或生物识别',
        _ => '验证失败：${e.description ?? e.code.name}',
      };
    } catch (e) {
      return '验证失败：$e';
    }
  }
}

/// 锁屏状态：设置、是否锁定、后台计时；页面通过 [AppLockScope] 取得。
class AppLockController extends ChangeNotifier {
  AppLockController({required this.store, required this.verifier, DateTime Function()? now}) : _now = now ?? DateTime.now;
  final LockStore store;
  final Verifier verifier;
  final DateTime Function() _now;

  /// 从后台回来超过这个时间才重新锁定（短暂切出去看验证码等不打扰）
  static const relockAfter = Duration(seconds: 60);

  LockSettings settings = LockSettings();
  bool locked = false;
  bool loaded = false;
  DateTime? _backgroundAt;

  Future<void> load() async {
    settings = await store.load();
    locked = settings.onOpen;
    loaded = true;
    notifyListeners();
  }

  void paused() => _backgroundAt ??= _now();

  void resumed() {
    final at = _backgroundAt;
    _backgroundAt = null;
    if (settings.onOpen && at != null && _now().difference(at) >= relockAfter) {
      locked = true;
      notifyListeners();
    }
  }

  /// 锁屏上的“解锁”
  Future<String?> unlock() async {
    final err = await verifier.verify('解锁 VPS Monitor');
    if (err == null) {
      locked = false;
      notifyListeners();
    }
    return err;
  }

  /// 敏感操作前验证；未开启时直接通过
  Future<bool> guard(String reason) async => !settings.sensitive || await verifier.verify(reason) == null;

  /// 修改开关：打开时先验证一次，失败返回说明，不改设置
  Future<String?> set({bool? onOpen, bool? sensitive}) async {
    if (onOpen == true || sensitive == true) {
      final err = await verifier.verify('确认开启生物识别锁');
      if (err != null) return err;
    }
    settings = LockSettings(onOpen: onOpen ?? settings.onOpen, sensitive: sensitive ?? settings.sensitive);
    await store.save(settings);
    notifyListeners();
    return null;
  }
}

/// 向下提供 [AppLockController]
class AppLockScope extends InheritedNotifier<AppLockController> {
  const AppLockScope({super.key, required AppLockController controller, required super.child}) : super(notifier: controller);

  static AppLockController? of(BuildContext context) => context.dependOnInheritedWidgetOfExactType<AppLockScope>()?.notifier;
}

/// 包住整个 App：锁定时用锁屏盖住内容（内容保留在下面，解锁后状态不丢），监听前后台切换。
class AppLockGate extends StatefulWidget {
  const AppLockGate({super.key, required this.controller, required this.child});
  final AppLockController controller;
  final Widget child;

  @override
  State<AppLockGate> createState() => _AppLockGateState();
}

class _AppLockGateState extends State<AppLockGate> with WidgetsBindingObserver {
  String _error = '';
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    widget.controller.addListener(_changed);
    if (!widget.controller.loaded) widget.controller.load();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    widget.controller.removeListener(_changed);
    super.dispose();
  }

  void _changed() {
    if (!mounted) return;
    setState(() {});
    if (widget.controller.locked && !_busy) _unlock(); // 锁定时自动弹出系统验证
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.paused || state == AppLifecycleState.hidden) widget.controller.paused();
    if (state == AppLifecycleState.resumed) widget.controller.resumed();
  }

  Future<void> _unlock() async {
    setState(() => _busy = true);
    final err = await widget.controller.unlock();
    if (mounted) {
      setState(() {
        _busy = false;
        _error = err ?? '';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = widget.controller;
    return AppLockScope(
      controller: c,
      child: Stack(children: [
        // 设置还没读到时先盖住，避免开启了锁的情况下闪现内容
        widget.child,
        if (!c.loaded || c.locked)
          Positioned.fill(
            child: Material(
              color: Theme.of(context).scaffoldBackgroundColor,
              child: SafeArea(
                child: Center(
                  child: !c.loaded
                      ? const SizedBox.shrink()
                      : Column(mainAxisSize: MainAxisSize.min, children: [
                          Icon(Icons.lock_outline, size: 48, color: Theme.of(context).colorScheme.primary),
                          const SizedBox(height: 12),
                          const Text('VPS Monitor 已锁定', style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600)),
                          const SizedBox(height: 20),
                          FilledButton.icon(
                            onPressed: _busy ? null : _unlock,
                            icon: const Icon(Icons.fingerprint),
                            label: const Text('解锁'),
                          ),
                          if (_error.isNotEmpty)
                            Padding(
                              padding: const EdgeInsets.all(16),
                              child: Text(_error, textAlign: TextAlign.center, style: TextStyle(color: Theme.of(context).colorScheme.error)),
                            ),
                        ]),
                ),
              ),
            ),
          ),
      ]),
    );
  }
}
