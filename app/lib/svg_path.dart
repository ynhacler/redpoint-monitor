// SVG 路径数据解析（修订第 213 条）：把 Simple Icons 等单色标志的 path d 转成 Flutter 的 Path，不引入 SVG 依赖。
// 支持全部路径命令（M L H V C S Q T A Z，大小写），以及紧凑写法（“-.5.5”“1e-3”、弧线标志不带分隔符的“011”）。
import 'dart:ui';

/// 解析 SVG 路径数据；格式错误时返回已解析的部分（标志显示不完整，但不会崩溃）。
Path parseSvgPath(String d) {
  final path = Path();
  final t = _Tokens(d);
  var cmd = '';
  var cur = Offset.zero, start = Offset.zero;
  Offset? lastCtrl; // 上一条三次 / 二次曲线的控制点，用于 S / T 的反射
  var lastCmd = '';
  try {
    while (true) {
      t.skipSeparators();
      if (t.done) break;
      if (t.isCommand) {
        cmd = t.command();
      } else if (cmd.isEmpty) {
        break;
      } else if (cmd == 'M') {
        cmd = 'L'; // M 之后的坐标对按 L 处理
      } else if (cmd == 'm') {
        cmd = 'l';
      }
      final rel = cmd == cmd.toLowerCase();
      Offset pt(double x, double y) => rel ? cur + Offset(x, y) : Offset(x, y);
      switch (cmd.toUpperCase()) {
        case 'M':
          cur = pt(t.number(), t.number());
          start = cur;
          path.moveTo(cur.dx, cur.dy);
          lastCtrl = null;
        case 'L':
          cur = pt(t.number(), t.number());
          path.lineTo(cur.dx, cur.dy);
          lastCtrl = null;
        case 'H':
          final x = t.number();
          cur = Offset(rel ? cur.dx + x : x, cur.dy);
          path.lineTo(cur.dx, cur.dy);
          lastCtrl = null;
        case 'V':
          final y = t.number();
          cur = Offset(cur.dx, rel ? cur.dy + y : y);
          path.lineTo(cur.dx, cur.dy);
          lastCtrl = null;
        case 'C':
          final c1 = pt(t.number(), t.number()), c2 = pt(t.number(), t.number()), e = pt(t.number(), t.number());
          path.cubicTo(c1.dx, c1.dy, c2.dx, c2.dy, e.dx, e.dy);
          lastCtrl = c2;
          cur = e;
        case 'S':
          final c1 = lastCtrl != null && 'CS'.contains(lastCmd.toUpperCase()) ? cur * 2 - lastCtrl : cur;
          final c2 = pt(t.number(), t.number()), e = pt(t.number(), t.number());
          path.cubicTo(c1.dx, c1.dy, c2.dx, c2.dy, e.dx, e.dy);
          lastCtrl = c2;
          cur = e;
        case 'Q':
          final c = pt(t.number(), t.number()), e = pt(t.number(), t.number());
          path.quadraticBezierTo(c.dx, c.dy, e.dx, e.dy);
          lastCtrl = c;
          cur = e;
        case 'T':
          final c = lastCtrl != null && 'QT'.contains(lastCmd.toUpperCase()) ? cur * 2 - lastCtrl : cur;
          final e = pt(t.number(), t.number());
          path.quadraticBezierTo(c.dx, c.dy, e.dx, e.dy);
          lastCtrl = c;
          cur = e;
        case 'A':
          final rx = t.number(), ry = t.number(), rot = t.number();
          final large = t.flag(), sweep = t.flag();
          final e = pt(t.number(), t.number());
          if (rx == 0 || ry == 0) {
            path.lineTo(e.dx, e.dy);
          } else {
            path.arcToPoint(e, radius: Radius.elliptical(rx.abs(), ry.abs()), rotation: rot, largeArc: large, clockwise: sweep);
          }
          lastCtrl = null;
          cur = e;
        case 'Z':
          path.close();
          cur = start;
          lastCtrl = null;
        default:
          return path;
      }
      lastCmd = cmd;
    }
  } on FormatException {
    // 数据不完整：返回已解析的部分
  }
  return path;
}

class _Tokens {
  _Tokens(this.s);
  final String s;
  int i = 0;

  bool get done => i >= s.length;
  bool get isCommand => !done && 'MmLlHhVvCcSsQqTtAaZz'.contains(s[i]);

  void skipSeparators() {
    while (!done && (s[i] == ' ' || s[i] == ',' || s[i] == '\n' || s[i] == '\t' || s[i] == '\r')) {
      i++;
    }
  }

  String command() => s[i++];

  /// 弧线的标志位：单个 0 或 1，后面可以直接跟数字
  bool flag() {
    skipSeparators();
    if (done || (s[i] != '0' && s[i] != '1')) throw const FormatException('flag');
    return s[i++] == '1';
  }

  double number() {
    skipSeparators();
    final b = i;
    if (!done && (s[i] == '-' || s[i] == '+')) i++;
    var dot = false;
    while (!done) {
      final ch = s[i];
      if (ch.codeUnitAt(0) >= 48 && ch.codeUnitAt(0) <= 57) {
        i++;
      } else if (ch == '.' && !dot) {
        dot = true;
        i++;
      } else if ((ch == 'e' || ch == 'E') && i > b) {
        i++;
        if (!done && (s[i] == '-' || s[i] == '+')) i++;
      } else {
        break;
      }
    }
    if (i == b) throw const FormatException('number');
    return double.parse(s.substring(b, i));
  }
}
