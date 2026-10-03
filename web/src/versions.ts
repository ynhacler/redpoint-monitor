// Agent 版本比较（设计 29.7.4），只用于界面提示；是否允许升级由面板、Agent 与 updater 按签名清单判定。
// git describe 形式（0.2.0-3-gabc1234[-dirty]）视为该版本之后的开发构建，-rc.1 等视为该版本之前的预发布。

function parse(v: string): [number, number, number, number] | null {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:-(.+))?$/.exec(v.trim())
  if (!m) return null
  const suffix = m[4]
  const rank = !suffix ? 0 : /^\d+-g[0-9a-f]+(-dirty)?$/.test(suffix) ? 1 : -1
  return [Number(m[1]), Number(m[2]), Number(m[3]), rank]
}

/** a < b 返回负数，相等 0，a > b 正数；无法解析时返回 null */
export function compareVersions(a: string, b: string): number | null {
  const x = parse(a)
  const y = parse(b)
  if (!x || !y) return null
  for (let i = 0; i < 4; i++) if (x[i] !== y[i]) return x[i] - y[i]
  return 0
}
