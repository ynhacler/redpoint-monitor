// 由 design/tokens.json 生成 web/src/styles/tokens.css、app/lib/tokens.dart、iOS 小组件的 Tokens.swift 与 Android 原生资源（设计 41.2），Web 与 App 同源。只用 Node 标准库。
//
// 深色模式：默认跟随系统（prefers-color-scheme）；<html data-theme="light|dark"> 可固定（设计 41.2.1）。
// 用法：npm run tokens（dev 与 build 前自动执行）
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
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

// ---- iOS 原生（桌面小组件，设计 1.5.4）：颜色随系统浅色 / 深色切换 ----
const rgb = (hex) => [1, 3, 5].map((i) => (parseInt(hex.slice(i, i + 2), 16) / 255).toFixed(3)).join(', ')
const swift = `// 自动生成，请勿手工编辑：修改 design/tokens.json 后在 web/ 中运行 npm run tokens（设计 41.2）
import SwiftUI
import UIKit

private func dynamic(_ light: (Double, Double, Double), _ dark: (Double, Double, Double)) -> Color {
    Color(UIColor { t in
        let c = t.userInterfaceStyle == .dark ? dark : light
        return UIColor(red: c.0, green: c.1, blue: c.2, alpha: 1)
    })
}

enum Tokens {
${names.map((n) => `    /// ${tokens.color[n].use}\n    static let ${camel(n)} = dynamic((${rgb(tokens.color[n].light)}), (${rgb(tokens.color[n].dark)}))`).join('\n')}
${tokens.space.map((v, i) => `    static let space${i + 1}: CGFloat = ${v}`).join('\n')}
${['xs', 'sm', 'md', 'lg', 'xl', 'num'].map((k) => `    static let font${k[0].toUpperCase() + k.slice(1)}: CGFloat = ${f[k].size}`).join('\n')}
}
`
writeFileSync(join(here, '../../app/ios/VpsmonWidget/Tokens.swift'), swift)

// ---- Android 原生（通知与桌面小组件，设计 1.5.4）：values / values-night 两套颜色，尺寸为 dp / sp ----
const snake = (k) => k.replace(/-/g, '_')
const androidRes = (mode, withDims) => `<?xml version="1.0" encoding="utf-8"?>
<!-- 自动生成，请勿手工编辑：修改 design/tokens.json 后在 web/ 中运行 npm run tokens（设计 41.2） -->
<resources>
${names.map((n) => `    <color name="vpsmon_${snake(n)}">${tokens.color[n][mode]}</color>`).join('\n')}
${withDims ? tokens.space.map((v, i) => `    <dimen name="vpsmon_space_${i + 1}">${v}dp</dimen>`).join('\n') + '\n' +
  Object.entries(tokens.radius).map(([k, v]) => `    <dimen name="vpsmon_radius_${snake(k)}">${v}dp</dimen>`).join('\n') + '\n' +
  ['xs', 'sm', 'md', 'lg', 'xl', 'num'].map((k) => `    <dimen name="vpsmon_font_${k}">${f[k].size}sp</dimen>`).join('\n') + '\n' : ''}</resources>
`
const resDir = join(here, '../../app/packages/vpsmon_native/android/src/main/res')
for (const d of ['values', 'values-night']) mkdirSync(join(resDir, d), { recursive: true })
writeFileSync(join(resDir, 'values/vpsmon_tokens.xml'), androidRes('light', true))
writeFileSync(join(resDir, 'values-night/vpsmon_tokens.xml'), androidRes('dark', false))
console.log('tokens.css, tokens.dart, Tokens.swift and Android resources generated')
