#!/usr/bin/env python3
"""注释检查（设计 39.7，修订第 157 条）：导出的标识符必须有注释。

- TypeScript（web/src/**/*.ts，不含生成的 api.gen.ts）：export function / class / const / let / enum 前一行必须是注释（/** … */ 或 //）
- Dart（app/lib/**/*.dart，不含生成的 tokens.dart）：公开的顶层 class / enum / mixin / extension / typedef / 函数前必须有注释（/// 或 //）
- Go 由 revive 的 exported 规则检查（Makefile lint、CI）

只用标准库，不引入 ESLint 等依赖。用法：python3 scripts/check_comments.py（有问题时列出并以 1 退出）。
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent

TS_EXPORT = re.compile(r'^export (?:default )?(?:async )?(?:function|class|const|let|enum)\s+([A-Za-z_$][\w$]*)')
# Dart 顶层声明：不以下划线开头的类型与函数（变量与 getter 不要求）
DART_DECL = re.compile(
    r'^(?:abstract |sealed |final |base |interface )*(?:class|enum|mixin|extension|typedef)\s+([A-Za-z]\w*)'
    r'|^(?:Future<[^>]*>|[A-Z][\w<>?, ]*|void|bool|int|double|String|num)\s+([a-z]\w*)\s*\(')


def commented(lines, i):
    """第 i 行之前（跳过注解与空行以外的装饰）是否有注释。"""
    j = i - 1
    while j >= 0 and lines[j].strip().startswith('@'):
        j -= 1
    if j < 0:
        return False
    prev = lines[j].strip()
    return prev.startswith('//') or prev.endswith('*/') or prev.startswith('*')


def check_ts():
    bad = []
    for f in sorted((ROOT / 'web/src').rglob('*.ts')):
        if f.name == 'api.gen.ts' or f.name.endswith('.d.ts'):
            continue
        lines = f.read_text(encoding='utf-8').split('\n')
        for i, line in enumerate(lines):
            m = TS_EXPORT.match(line)
            if m and not commented(lines, i):
                bad.append(f'{f.relative_to(ROOT)}:{i + 1}: 导出的 {m.group(1)} 缺少注释')
    return bad


def check_dart():
    bad = []
    for f in sorted((ROOT / 'app/lib').rglob('*.dart')):
        if f.name == 'tokens.dart':
            continue
        lines = f.read_text(encoding='utf-8').split('\n')
        for i, line in enumerate(lines):
            m = DART_DECL.match(line)
            if not m:
                continue
            name = m.group(1) or m.group(2)
            if name == 'main' or name.startswith('_'):
                continue
            if not commented(lines, i):
                bad.append(f'{f.relative_to(ROOT)}:{i + 1}: 公开的 {name} 缺少注释')
    return bad


def main():
    bad = check_ts() + check_dart()
    for b in bad:
        print(b)
    if bad:
        print(f'\n共 {len(bad)} 处导出标识符缺少注释（设计 39.7）', file=sys.stderr)
        return 1
    print('注释检查通过')
    return 0


if __name__ == '__main__':
    sys.exit(main())
