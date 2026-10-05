import 'package:flutter_test/flutter_test.dart';
import 'package:vpsmon_app/api.dart';

void main() {
  test('fmtBytes uses decimal units', () {
    expect(fmtBytes(0), '0 B');
    expect(fmtBytes(1500), '1.5 KB');
    expect(fmtBytes(2000000, perSec: true), '2.0 MB/s');
    expect(fmtBytes(638000000000), '638 GB');
  });
}
