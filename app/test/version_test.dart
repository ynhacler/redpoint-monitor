import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/version.dart';

void main() {
  test('appVersion 与 pubspec.yaml 的版本一致（修订第 341 条：此前停在 0.3.6）', () {
    final m = RegExp(r'^version:\s*([0-9.]+)\+', multiLine: true).firstMatch(File('pubspec.yaml').readAsStringSync());
    expect(m, isNotNull);
    expect(appVersion, m!.group(1));
  });
}
