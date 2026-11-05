#!/usr/bin/env python3
"""界面翻译检查（设计 52.2）：
1. web/src 中每个 t('中文原文') 都要在 locales/en.ts 中有译文，译文中的 {变量} 与原文一致。
   t() 第一个参数里的字面量都算（包括 t(a ? '甲' : '乙')）；t(变量) 的翻译由提供变量的地方负责。
2. .vue 模板中不能有 t() 之外的中文（文字、属性值、表达式里的字符串都算，注释除外），
   否则英文界面下这些文字仍是中文（修订第 296 条）。确实要显示中文的写进 TEMPLATE_ALLOW。

用法：python3 scripts/check_i18n.py          检查，有缺漏时退出码为 1
      python3 scripts/check_i18n.py --list   只列出缺少译文的原文（每行一个，便于补充）
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
SRC = ROOT / "web" / "src"
EN = SRC / "locales" / "en.ts"

# t(：前面不能是标识符字符（排除 sort(、split( 等）
CALL = re.compile(r"(?<![\w.$])t\(")
# 字符串字面量（单引号或双引号）
STR = re.compile(r"'((?:[^'\\\n]|\\.)*)'|\"((?:[^\"\\\n]|\\.)*)\"")
CJK = re.compile(r"[一-鿿]")
# 模板中有意保留的中文：相对 web/src 的文件 → 允许出现的完整一行文字（去掉首尾空白后）。
# 只放确实不该翻译的内容，并写明原因；不再出现的条目也会报错，避免名单过期。
TEMPLATE_ALLOW: dict[str, set[str]] = {}
# 'key': 'value'，可以分两行（长句时 value 换行缩进）
ENTRY = re.compile(r"'((?:[^'\\\n]|\\.)*)'\s*:\s*'((?:[^'\\\n]|\\.)*)'")
VAR = re.compile(r"\{(\w+)\}")


def unescape(s: str) -> str:
    return re.sub(r"\\(.)", r"\1", s)


def first_arg_end(text: str, start: int) -> int:
    """从 t( 之后的 start 开始，返回第一个参数的结束位置（顶层逗号或右括号）。
    字符串内的括号和逗号不算；模板字符串按普通字符串跳过。"""
    depth, i, quote = 0, start, ""
    while i < len(text):
        c = text[i]
        if quote:
            if c == "\\":
                i += 1
            elif c == quote:
                quote = ""
        elif c in "'\"`":
            quote = c
        elif c in "([{":
            depth += 1
        elif c in ")]}":
            if depth == 0:
                return i
            depth -= 1
        elif c == "," and depth == 0:
            return i
        i += 1
    return len(text)


def t_keys(text: str):
    """逐个产出 t() 第一个参数中的字符串：(原文, 所在位置)。嵌套在第一个参数里的 t() 不重复产出。"""
    pos = 0
    while (m := CALL.search(text, pos)) is not None:
        end = first_arg_end(text, m.end())
        for s in STR.finditer(text, m.end(), end):
            yield unescape(s.group(1) if s.group(1) is not None else s.group(2)), s.start()
        pos = end


def blank(m: re.Match) -> str:
    """把匹配内容换成空格，保留换行，这样后面报的行号仍然准确。"""
    return re.sub(r"[^\n]", " ", m.group(0))


def template_of(text: str) -> tuple[str, int]:
    """取出 .vue 的模板部分（去掉 HTML 注释）和它的起始位置；没有模板时返回空串。"""
    start = re.search(r"^<template\b[^>]*>", text, re.M)
    ends = list(re.finditer(r"^</template>", text, re.M))
    if not start or not ends:
        return "", 0
    return re.sub(r"<!--.*?-->", blank, text[start.end():ends[-1].start()], flags=re.S), start.end()


def raw_template_text(f: pathlib.Path, text: str, used: set[tuple[str, str]]) -> list[str]:
    """报告模板中 t() 第一个参数之外的中文。t() 的其他参数（变量）里的中文同样要报。"""
    tpl, offset = template_of(text)
    masked, pos = list(tpl), 0
    while (m := CALL.search(tpl, pos)) is not None:
        end = first_arg_end(tpl, m.end())
        for i in range(m.end(), end):
            if masked[i] != "\n":
                masked[i] = " "
        pos = end
    name = f.relative_to(SRC).as_posix()
    allow = TEMPLATE_ALLOW.get(name, set())
    found = []
    first_line = text.count("\n", 0, offset) + 1
    for i, line in enumerate("".join(masked).split("\n")):
        if not CJK.search(line):
            continue
        shown = line.strip()
        if shown in allow:
            used.add((name, shown))
            continue
        found.append(f"{f.relative_to(ROOT)}:{first_line + i}：{shown[:80]}")
    return found


def main() -> int:
    entries = {}
    body = re.sub(r"^\s*//.*$", "", EN.read_text(encoding="utf-8"), flags=re.M)  # 去掉整行注释
    for m in ENTRY.finditer(body):
        entries[unescape(m.group(1))] = unescape(m.group(2))

    missing: dict[str, list[str]] = {}
    raw: list[str] = []
    used: set[tuple[str, str]] = set()
    for f in sorted(SRC.rglob("*")):
        if f.suffix not in (".ts", ".vue") or f == EN or f.name.endswith(".gen.ts"):
            continue
        text = f.read_text(encoding="utf-8")
        for key, pos in t_keys(text):
            if not CJK.search(key):
                continue
            if key not in entries:
                line = text.count("\n", 0, pos) + 1
                missing.setdefault(key, []).append(f"{f.relative_to(ROOT)}:{line}")
        if f.suffix == ".vue":
            raw += raw_template_text(f, text, used)

    bad_vars = [k for k, v in entries.items() if set(VAR.findall(k)) != set(VAR.findall(v))]
    stale = [f"{name}：{line}" for name, lines in TEMPLATE_ALLOW.items() for line in lines if (name, line) not in used]

    if "--list" in sys.argv:
        for k in missing:
            print(k)
        return 0
    for k, where in missing.items():
        print(f"缺少英文译文：{k!r}（{where[0]}）")
    for k in bad_vars:
        print(f"译文中的变量与原文不一致：{k!r} → {entries[k]!r}")
    for where in raw:
        print(f"模板中有未翻译的中文：{where}")
    for line in stale:
        print(f"TEMPLATE_ALLOW 中的条目已不存在，请删除：{line}")
    if missing or bad_vars or raw or stale:
        print(f"界面翻译检查未通过：缺少 {len(missing)} 条，变量不一致 {len(bad_vars)} 条（web/src/locales/en.ts），"
              f"模板未翻译 {len(raw)} 处，名单过期 {len(stale)} 条")
        return 1
    print(f"界面翻译检查通过（{len(entries)} 条译文）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
