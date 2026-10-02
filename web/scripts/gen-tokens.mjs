// 由 design/tokens.json 生成 web/src/styles/tokens.css（设计 41.2）。只用 Node 标准库。
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
console.log('tokens.css generated')
