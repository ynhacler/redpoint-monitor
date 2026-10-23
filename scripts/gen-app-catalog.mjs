// 生成 App 的服务目录与标志（修订第 213 条）：把 Web 的订阅目录、SIM 卡目录、连通性测试目标与内置标志
// （web/src/subscriptions.ts、simCatalog.ts、connectivity.ts、brandIcons.ts、brandLogos.ts、siteIcons.ts、siteLogos.ts）
// 转成 app/lib/catalog.gen.dart，App 与 Web 显示同样的名称、品牌色与标志，不各自维护一份。
// 用法：make app-catalog（在 web/ 中用 esbuild 打包读取，再写出 Dart）。
import { createRequire } from 'node:module'
import { writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
// esbuild 是 web/ 的开发依赖（随 Vite 安装）
const { build } = createRequire(path.join(root, 'web/package.json'))('esbuild')
const entry = `
import { catalog, categoryNames, cycleNames, currencies } from './src/subscriptions'
import { siteGroups } from './src/connectivity'
import { brandIcons } from './src/brandIcons'
import { brandLogos } from './src/brandLogos'
import { siteIcons } from './src/siteIcons'
import { siteLogos } from './src/siteLogos'
globalThis.__out = { catalog, categoryNames, cycleNames, currencies, siteGroups, brandIcons, brandLogos, siteIcons, siteLogos }
`
const res = await build({
  stdin: { contents: entry, resolveDir: path.join(root, 'web'), loader: 'ts' },
  bundle: true, write: false, format: 'iife', platform: 'neutral', logLevel: 'error',
})
new Function(res.outputFiles[0].text)()
const d = globalThis.__out

const q = (s) => (s == null ? 'null' : `'${String(s).replace(/\\/g, '\\\\').replace(/'/g, "\\'").replace(/\$/g, '\\$')}'`)
const color = (hex) => `Color(0xFF${hex.replace('#', '').toUpperCase()})`
const b64 = (src) => src.replace(/^data:image\/png;base64,/, '')
const lines = []
const w = (s) => lines.push(s)
w('// 由 scripts/gen-app-catalog.mjs 从 Web 的目录与标志生成（修订第 213 条），不要手工修改；运行 make app-catalog 重新生成。')
w('// 标志图形取自 Simple Icons（CC0-1.0）与各网站自己的图标；商标归各自所有者，这里只用于标识对应的服务。')
w('// ignore_for_file: lines_longer_than_80_chars')
w("import 'dart:ui';")
w('')
w('/// 单色标志：24×24 视图框内的 SVG 路径与品牌色')
w('class GlyphIcon {')
w('  /// 创建标志')
w('  const GlyphIcon(this.path, this.color);')
w('  /// SVG 路径数据')
w('  final String path;')
w('  /// 品牌色')
w('  final Color color;')
w('}')
w('')
w('/// 图片标志：PNG（Base64）；fill 铺满方块，contain 居中放在 bg 色（默认白色）的方块中')
w('class ImageLogo {')
w('  /// 创建图片标志')
w('  const ImageLogo(this.png, {this.fill = false, this.bg});')
w('  /// PNG 的 Base64')
w('  final String png;')
w('  /// 是否铺满')
w('  final bool fill;')
w('  /// 背景色')
w('  final Color? bg;')
w('}')
w('')
w('/// 订阅目录中的一项')
w('class CatalogEntry {')
w('  /// 创建目录项')
w('  const CatalogEntry(this.name, this.category, this.color, {this.letter, this.icon, this.currency, this.dial, this.hot = false});')
w('  /// 名称')
w('  final String name;')
w('  /// 类别')
w('  final String category;')
w('  /// 品牌色')
w('  final Color color;')
w('  /// 图标字（默认取名称的第一个字母或汉字）')
w('  final String? letter;')
w('  /// 标志键（默认与目录键相同）')
w('  final String? icon;')
w('  /// 默认币种')
w('  final String? currency;')
w('  /// SIM 卡的国家 / 地区区号，如 +86')
w('  final String? dial;')
w('  /// 常用（选择服务时排在前面）')
w('  final bool hot;')
w('}')
w('')
w('/// 连通性测试的一个目标')
w('class SiteTarget {')
w('  /// 创建目标')
w('  const SiteTarget(this.id, this.name, this.urls, this.color, {this.letter, this.icon});')
w('  /// 标识')
w('  final String id;')
w('  /// 名称')
w('  final String name;')
w('  /// 测试地址：前一个连接失败时改测下一个')
w('  final List<String> urls;')
w('  /// 品牌色')
w('  final Color color;')
w('  /// 图标字')
w('  final String? letter;')
w('  /// 标志键')
w('  final String? icon;')
w('}')
w('')
w('/// 连通性测试的一组目标')
w('class SiteGroup {')
w('  /// 创建分组')
w('  const SiteGroup(this.key, this.name, this.flag, this.sites);')
w('  /// 键')
w('  final String key;')
w('  /// 名称')
w('  final String name;')
w('  /// 国旗')
w('  final String flag;')
w('  /// 目标')
w('  final List<SiteTarget> sites;')
w('}')
w('')
w('/// 订阅类别名称')
w(`const categoryNames = <String, String>{${Object.entries(d.categoryNames).map(([k, v]) => `${q(k)}: ${q(v)}`).join(', ')}};`)
w('')
w('/// 订阅周期名称')
w(`const cycleNames = <String, String>{${Object.entries(d.cycleNames).map(([k, v]) => `${q(k)}: ${q(v)}`).join(', ')}};`)
w('')
w('/// 可选币种')
w(`const currencies = <String>[${d.currencies.map(q).join(', ')}];`)
w('')
w('/// 订阅目录：键 → 目录项（按 Web 的顺序，热门在前的由 hot 决定）')
w('const catalog = <String, CatalogEntry>{')
for (const c of d.catalog) {
  const opt = [c.letter ? `letter: ${q(c.letter)}` : '', c.icon ? `icon: ${q(c.icon)}` : '',
    c.currency ? `currency: ${q(c.currency)}` : '', c.dial ? `dial: ${q(c.dial)}` : '', c.hot ? 'hot: true' : ''].filter(Boolean)
  w(`  ${q(c.key)}: CatalogEntry(${q(c.name)}, ${q(c.category)}, ${color(c.color)}${opt.length ? ', ' + opt.join(', ') : ''}),`)
}
w('};')
w('')
w('/// 连通性测试目标：中国、美国、全球')
w('const siteGroups = <SiteGroup>[')
for (const g of d.siteGroups) {
  w(`  SiteGroup(${q(g.key)}, ${q(g.name)}, ${q(g.flag)}, [`)
  for (const s of g.sites) {
    const urls = [s.url, ...(s.alt ?? [])].map(q).join(', ')
    const opt = [s.letter ? `letter: ${q(s.letter)}` : '', s.icon ? `icon: ${q(s.icon)}` : ''].filter(Boolean)
    w(`    SiteTarget(${q(s.id)}, ${q(s.name)}, [${urls}], ${color(s.color)}${opt.length ? ', ' + opt.join(', ') : ''}),`)
  }
  w('  ]),')
}
w('];')
const glyphs = (name, doc, m) => {
  w('')
  w(`/// ${doc}`)
  w(`const ${name} = <String, GlyphIcon>{`)
  for (const [k, v] of Object.entries(m)) w(`  ${q(k)}: GlyphIcon(${q(v.path)}, ${color(v.hex)}),`)
  w('};')
}
const logos = (name, doc, m) => {
  w('')
  w(`/// ${doc}`)
  w(`const ${name} = <String, ImageLogo>{`)
  for (const [k, v] of Object.entries(m)) {
    // 只取 PNG：SVG 图片（带渐变、引用等）Flutter 不能直接显示，这几个在 App 中用品牌色加首字母
    if (!v.src.startsWith('data:image/png;base64,')) continue
    const opt = [v.fit === 'fill' ? 'fill: true' : '', v.bg ? `bg: ${color(v.bg)}` : ''].filter(Boolean)
    w(`  ${q(k)}: ImageLogo(${q(b64(v.src))}${opt.length ? ', ' + opt.join(', ') : ''}),`)
  }
  w('};')
}
glyphs('brandIcons', '订阅服务的单色标志', d.brandIcons)
logos('brandLogos', '订阅服务的图片标志', d.brandLogos)
glyphs('siteIcons', '连通性测试网站的单色标志', d.siteIcons)
logos('siteLogos', '连通性测试网站的图片标志', d.siteLogos)
writeFileSync(path.join(root, 'app/lib/catalog.gen.dart'), lines.join('\n') + '\n')
console.log(`已生成 app/lib/catalog.gen.dart：订阅目录 ${d.catalog.length} 项，测试目标 ${d.siteGroups.reduce((a, g) => a + g.sites.length, 0)} 个，标志 ${Object.keys(d.brandIcons).length + Object.keys(d.siteIcons).length + Object.keys(d.brandLogos).length + Object.keys(d.siteLogos).length} 个`)
