// 服务标志（修订第 213 条）：与 Web 的 SubAvatar 相同——上传的图标 > 图片标志 > 品牌色方块上的单色标志 > 色块加首字母。
// 品牌色很浅时（相对亮度高于 0.6）标志与文字用深色，避免看不清。
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';

import '../catalog.gen.dart';
import '../svg_path.dart';

final _paths = <String, Path>{};
final _pngs = <String, Uint8List>{};

/// 方块标志
class BrandAvatar extends StatelessWidget {
  /// 创建标志；[image] 为上传的图标（优先），其次 [logo]、[glyph]，都没有时用 [color] 与 [letter]
  const BrandAvatar({super.key, this.image, this.logo, this.glyph, required this.color, required this.letter, this.size = 40, this.tinted = false});

  /// 上传的图标
  final ImageProvider? image;

  /// 图片标志
  final ImageLogo? logo;

  /// 单色标志
  final GlyphIcon? glyph;

  /// 背景色（没有单色标志时）
  final Color color;

  /// 首字母
  final String letter;

  /// 边长
  final double size;

  /// 单色标志用品牌色画在浅色底上（连通性测试，与 Web 相同）；否则白色标志画在品牌色底上（订阅）
  final bool tinted;

  @override
  Widget build(BuildContext context) {
    final radius = BorderRadius.circular(size / 4);
    final border = Border.all(color: Theme.of(context).colorScheme.onSurface.withValues(alpha: 0.08));
    if (image != null) {
      return ClipRRect(borderRadius: radius, child: Image(image: image!, width: size, height: size, fit: BoxFit.cover));
    }
    if (logo != null) {
      final bytes = _pngs.putIfAbsent(logo!.png, () => base64Decode(logo!.png));
      return Container(
        width: size,
        height: size,
        decoration: BoxDecoration(color: logo!.bg ?? Colors.white, borderRadius: radius, border: border),
        clipBehavior: Clip.antiAlias,
        alignment: Alignment.center,
        child: logo!.fill
            ? Image.memory(bytes, width: size, height: size, fit: BoxFit.cover)
            : Image.memory(bytes, width: size * 0.74, height: size * 0.74, fit: BoxFit.contain),
      );
    }
    var bg = glyph?.color ?? color;
    var fg = bg.computeLuminance() > 0.6 ? const Color(0xFF111827) : Colors.white;
    if (tinted && glyph != null) {
      final scheme = Theme.of(context).colorScheme;
      // 很深的品牌色（黑色标志）用正文色，放在次级表面上
      fg = glyph!.color.computeLuminance() < 0.02 ? scheme.onSurface : glyph!.color;
      bg = glyph!.color.computeLuminance() < 0.02 ? scheme.surfaceContainerHighest : Color.alphaBlend(glyph!.color.withValues(alpha: 0.14), scheme.surface);
    }
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(color: bg, borderRadius: radius, border: border),
      alignment: Alignment.center,
      child: glyph != null
          ? CustomPaint(size: Size.square(size * 0.56), painter: _GlyphPainter(_paths.putIfAbsent(glyph!.path, () => parseSvgPath(glyph!.path)), fg))
          : Text(letter, maxLines: 1, style: TextStyle(color: fg, fontWeight: FontWeight.w700, fontSize: size * (letter.length > 2 ? 0.3 : 0.45))),
    );
  }
}

class _GlyphPainter extends CustomPainter {
  _GlyphPainter(this.path, this.color);
  final Path path;
  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.scale(size.width / 24, size.height / 24); // 标志的视图框为 24×24
    canvas.drawPath(path, Paint()..color = color);
  }

  @override
  bool shouldRepaint(_GlyphPainter old) => old.path != path || old.color != color;
}

/// 首字母：第一个字母、数字或汉字，大写
String firstLetter(String name) {
  final m = RegExp(r'[A-Za-z0-9一-鿿]').firstMatch(name.trim());
  return (m?.group(0) ?? '?').toUpperCase();
}

/// 没有品牌色时按名称取一个稳定的颜色（与 Web 的 avatarOf 相同）
Color paletteColor(String name) {
  const palette = [0xFF2563EB, 0xFF16A34A, 0xFF7C3AED, 0xFF0891B2, 0xFFD97706, 0xFFDB2777, 0xFF4B5563];
  var h = 0;
  for (final c in name.codeUnits) {
    h = (h * 31 + c) & 0xFFFFFFFF;
  }
  return Color(palette[h % palette.length]);
}

/// 连通性测试目标的标志
Widget siteAvatar(SiteTarget s, {double size = 36}) => BrandAvatar(
      logo: siteLogos[s.id],
      glyph: s.icon != null ? siteIcons[s.icon] : null,
      color: s.color,
      letter: s.letter ?? firstLetter(s.name),
      size: size,
      tinted: true,
    );
