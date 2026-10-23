#!/usr/bin/env python3
"""界面翻译检查（设计 52.2）：web/src 中每个 t('中文原文') 都要在 locales/en.ts 中有译文，
译文中的 {变量} 与原文一致。只检查字面量参数；t(变量) 的翻译由提供变量的地方负责。

用法：python3 scripts/check_i18n.py          检查，有缺漏时退出码为 1
      python3 scripts/check_i18n.py --list   只列出缺少译文的原文（每行一个，便于补充）
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
SRC = ROOT / "web" / "src"
EN = SRC / "locales" / "en.ts"

# t('…') / t("…")：前面不能是标识符字符（排除 sort('…')、split('…') 等）
CALL = re.compile(r"(?<![\w.$])t\(\s*(?:'((?:[^'\\\n]|\\.)*)'|\"((?:[^\"\\\n]|\\.)*)\")")
# 'key': 'value'，可以分两行（长句时 value 换行缩进）
ENTRY = re.compile(r"'((?:[^'\\\n]|\\.)*)'\s*:\s*'((?:[^'\\\n]|\\.)*)'")
VAR = re.compile(r"\{(\w+)\}")


def unescape(s: str) -> str:
    return re.sub(r"\\(.)", r"\1", s)


def main() -> int:
    entries = {}
    body = re.sub(r"^\s*//.*$", "", EN.read_text(encoding="utf-8"), flags=re.M)  # 去掉整行注释
    for m in ENTRY.finditer(body):
        entries[unescape(m.group(1))] = unescape(m.group(2))

    missing: dict[str, list[str]] = {}
    for f in sorted(SRC.rglob("*")):
        if f.suffix not in (".ts", ".vue") or f == EN or f.name.endswith(".gen.ts"):
            continue
        text = f.read_text(encoding="utf-8")
        for m in CALL.finditer(text):
            key = unescape(m.group(1) if m.group(1) is not None else m.group(2))
            if not re.search(r"[一-鿿]", key):
                continue
            if key not in entries:
                line = text.count("\n", 0, m.start()) + 1
                missing.setdefault(key, []).append(f"{f.relative_to(ROOT)}:{line}")

    bad_vars = [k for k, v in entries.items() if set(VAR.findall(k)) != set(VAR.findall(v))]

    if "--list" in sys.argv:
        for k in missing:
            print(k)
        return 0
    for k, where in missing.items():
        print(f"缺少英文译文：{k!r}（{where[0]}）")
    for k in bad_vars:
        print(f"译文中的变量与原文不一致：{k!r} → {entries[k]!r}")
    if missing or bad_vars:
        print(f"界面翻译检查未通过：缺少 {len(missing)} 条，变量不一致 {len(bad_vars)} 条（web/src/locales/en.ts）")
        return 1
    print(f"界面翻译检查通过（{len(entries)} 条译文）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
