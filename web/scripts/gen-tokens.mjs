// 由 design/tokens.json 生成 web/src/styles/tokens.css 与 app/lib/tokens.dart（设计 41.2），Web 与 App 同源。只用 Node 标准库。
//
// 深色模式：默认跟随系统（prefers-color-scheme）；<html data-theme="light|dark"> 可固定（设计 41.2.1）。
// 用法：npm run tokens（dev 与 build 前自动执行）
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const tokens = JSON.parse(readFileSync(join(here, '../../design/tokens.json'), 'utf8'))

const colors = (mode) =>
  Object.entries(tokens.color)
    .map(([name, c]) => `  --${name}: ${c[mode]};`)
    .join('\n')

const lines = []
const f = tokens.font
for (const k of ['xs', 'sm', 'md', 'lg', 'xl', 'num']) {
  lines.push(`  --font-${k}: ${f[k].size}px;`, `  --line-${k}: ${f[k].line}px;`)
}
lines.push(`  --font-family: ${f.family};`, `  --font-mono: ${f.mono};`)
lines.push(`  --weight-regular: ${f['weight-regular']};`, `  --weight-strong: ${f['weight-strong']};`)
tokens.space.forEach((v, i) => lines.push(`  --space-${i + 1}: ${v}px;`))
for (const [k, v] of Object.entries(tokens.radius)) lines.push(`  --radius-${k}: ${v}px;`)
lines.push(`  --max-width: ${tokens.layout['max-width']}px;`, `  --card-min: ${tokens.layout['card-min']}px;`)

const css = `/* 自动生成，请勿手工编辑：修改 design/tokens.json 后运行 npm run tokens（设计 41.2） */
:root {
${lines.join('\n')}
${colors('light')}
  color-scheme: light;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme='light']) {
${colors('dark').replace(/^/gm, '  ')}
    color-scheme: dark;
  }
}
:root[data-theme='dark'] {
${colors('dark')}
  color-scheme: dark;
}
`
writeFileSync(join(here, '../src/styles/tokens.css'), css)

// ---- App（Flutter）：颜色按浅色 / 深色两套常量，尺寸为逻辑像素 ----
const camel = (k) => k.replace(/-(\w)/g, (_, c) => c.toUpperCase())
const argb = (hex) => `Color(0xFF${hex.slice(1).toUpperCase()})`
const names = Object.keys(tokens.color)
const colorSet = (mode) => names.map((n) => `${camel(n)}: ${argb(tokens.color[n][mode])}`).join(',\n    ')
const dart = `// 自动生成，请勿手工编辑：修改 design/tokens.json 后在 web/ 中运行 npm run tokens（设计 41.2）
import 'package:flutter/painting.dart';

class AppColors {
  const AppColors({
${names.map((n) => `    required this.${camel(n)},`).join('\n')}
  });

${names.map((n) => `  /// ${tokens.color[n].use}\n  final Color ${camel(n)};`).join('\n')}

  static const light = AppColors(
    ${colorSet('light')},
  );

  static const dark = AppColors(
    ${colorSet('dark')},
  );
}

/// 间距（逻辑像素）：space1 = ${tokens.space[0]} … space${tokens.space.length} = ${tokens.space.at(-1)}
abstract final class AppSpace {
${tokens.space.map((v, i) => `  static const space${i + 1} = ${v}.0;`).join('\n')}
}

abstract final class AppRadius {
${Object.entries(tokens.radius).map(([k, v]) => `  static const ${k} = ${v}.0;`).join('\n')}
}

/// 字号与行高（逻辑像素）
abstract final class AppFont {
${['xs', 'sm', 'md', 'lg', 'xl', 'num'].map((k) => `  static const ${k} = ${f[k].size}.0;\n  static const ${k}Line = ${f[k].line}.0;`).join('\n')}
}
`
writeFileSync(join(here, '../../app/lib/tokens.dart'), dart)
console.log('tokens.css and tokens.dart generated')
