#!/usr/bin/env python3
"""设计文档编号与引用校验（设计 40.9.3）。

检查 docs/design.md 的章节编号、文档内交叉引用、修订记录的“涉及章节”、目录，
以及代码注释、TODO.md、CLAUDE.md、README.md 中对设计章节的引用。
只依赖 Python 3 标准库，供本地提交前与 GitHub Actions（设计 40.8.3）共用。

用法：
  python3 scripts/check_design_refs.py           # 只检查，失败时退出码为 1
  python3 scripts/check_design_refs.py --write   # 重新生成目录后检查

文档中故意写错的示例行，在行尾加 <!-- refcheck:ignore --> 即可跳过引用检查。
"""
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DESIGN = os.path.join(ROOT, "docs", "design.md")
IGNORE = "refcheck:ignore"
TOC_START, TOC_END = "<!-- TOC:START -->", "<!-- TOC:END -->"

# 编号：1、27.5、27.5.4 …；章节标题形如 “# 27. Agent 安装” 或 “## 27.5 安装脚本”
NUM = r"\d+(?:\.\d+)*"
HEADING = re.compile(r"^(#{1,6}) (.+?)\s*$")
NUMBERED = re.compile(r"^(" + NUM + r")\.?(?:\s|$)")

# 文档内引用（设计 40.9.2）：（设计 5.5）、（设计 27.6.1、27.10）、见 29.1、第 27 章、第 41.7 节、5.5～5.8
# 中文列表只用“、”与“～”分隔；逗号后面通常是正文（如“设计 43.5，401 时…”），不算引用
LIST = NUM + r"(?:\s*[、～~]\s*" + NUM + r")*"
REF_CN = re.compile(r"(?:设计|见)\s*(" + LIST + r")")
REF_CHAPTER = re.compile(r"第\s*(" + NUM + r")\s*[章节]")
# 代码与 TODO.md 中的英文写法：design 5.5、design 21, 18.4、design ch.21
REF_EN = re.compile(r"design\s+(?:ch\.?\s*)?(" + NUM + r"(?:\s*,\s*(?:ch\.?\s*)?" + NUM + r")*)", re.I)

# 需要检查引用的仓库文件（docs/design.md 单独检查）
SCAN_EXT = {".go", ".ts", ".vue", ".dart", ".sh", ".sql", ".py", ".service", ".yaml", ".yml"}
SCAN_FILES = {"TODO.md", "CLAUDE.md", "README.md", "Makefile"}
SKIP_DIRS = {".git", "node_modules", "dist", "bin", ".dev", "build", ".dart_tool", "Pods"}


def split_refs(s):
    """把 “27.6.1、27.10” “5.5～5.8” “21, ch.18” 拆成单个编号。"""
    return [p for p in re.split(r"\s*(?:[、，,～~]|ch\.?)\s*", s) if p]


def parse_headings(lines):
    """返回代码块之外的标题：[(行号, 层级, 编号或 None, 标题文字)]。"""
    out, in_code = [], False
    for i, line in enumerate(lines, 1):
        if line.lstrip().startswith("```"):
            in_code = not in_code
            continue
        if in_code:
            continue
        m = HEADING.match(line)
        if not m:
            continue
        level, text = len(m.group(1)), m.group(2)
        n = NUMBERED.match(text)
        out.append((i, level, n.group(1) if n else None, text))
    return out


def check_numbering(headings):
    """编号重复、层级与编号深度不符、不在所属章下、同一章内倒序。"""
    errors, seen = [], {}
    last_child = {}  # 父编号 → 上一个子编号（整数元组）
    for line, level, num, text in headings:
        if num is None:
            continue
        if num in seen:
            errors.append(f"design.md:{line}: 编号重复 {num}（首次出现在第 {seen[num]} 行）")
        seen.setdefault(num, line)
        parts = num.split(".")
        if len(parts) != level:
            errors.append(f"design.md:{line}: 标题层级（{'#' * level}）与编号 {num} 的深度不一致")
            continue
        if level > 1:
            parent = ".".join(parts[:-1])
            if parent not in seen:
                errors.append(f"design.md:{line}: {num} 不在所属的 {parent} 之下")
            key = tuple(int(p) for p in parts)
            prev = last_child.get(parent)
            if prev is not None and key <= prev:
                errors.append(f"design.md:{line}: {num} 出现在 {'.'.join(map(str, prev))} 之后（编号倒序）")
            last_child[parent] = key
    return errors, set(seen)


def check_doc_refs(lines, valid):
    """文档正文与修订记录“涉及章节”列中的引用必须指向存在的章节。"""
    errors, in_rev = [], False
    for i, line in enumerate(lines, 1):
        if IGNORE in line:
            continue
        if line.startswith("## 修订记录"):
            in_rev = True
        elif in_rev and line.startswith("# "):
            in_rev = False
        refs = []
        for m in REF_CN.finditer(line):
            refs += split_refs(m.group(1))
        refs += [m.group(1) for m in REF_CHAPTER.finditer(line)]
        # 修订记录表格的最后一列是“涉及章节”，内容是不带“设计”前缀的编号列表
        if in_rev and line.startswith("|") and not line.startswith("|---"):
            cells = [c.strip() for c in line.strip().strip("|").split("|")]
            if cells and re.fullmatch(LIST, cells[-1]):
                refs += split_refs(cells[-1])
        for r in refs:
            if r not in valid:
                errors.append(f"design.md:{i}: 引用了不存在的章节 {r}")
    return errors


def check_repo_refs(valid):
    """代码注释、TODO.md、CLAUDE.md、README.md 中的“设计 x.y / design x.y”引用。"""
    errors = []
    for dirpath, dirnames, filenames in os.walk(ROOT):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for name in filenames:
            path = os.path.join(dirpath, name)
            rel = os.path.relpath(path, ROOT)
            if rel == os.path.join("docs", "design.md"):
                continue
            if os.path.splitext(name)[1] not in SCAN_EXT and name not in SCAN_FILES:
                continue
            try:
                with open(path, encoding="utf-8") as f:
                    text = f.read().splitlines()
            except (UnicodeDecodeError, OSError):
                continue
            for i, line in enumerate(text, 1):
                if IGNORE in line:
                    continue
                refs = []
                for m in REF_EN.finditer(line):
                    refs += split_refs(m.group(1))
                for m in REF_CN.finditer(line):
                    refs += split_refs(m.group(1))
                refs += [m.group(1) for m in REF_CHAPTER.finditer(line)]
                for r in refs:
                    if r not in valid:
                        errors.append(f"{rel}:{i}: 引用了不存在的设计章节 {r}")
    return errors


def slug(text, used):
    """按 GitHub 的规则生成标题锚点：小写、去掉标点、空格变连字符，重复时追加 -1、-2。"""
    s = re.sub(r"[^\w\- ]", "", text.lower()).replace(" ", "-")
    base, n = s, 0
    while s in used:
        n += 1
        s = f"{base}-{n}"
    used.add(s)
    return s


def build_toc(headings):
    """目录只列章（# N.）与其下的 ## 小节，以及第 1 章之前除“目录”外的无编号 ## 标题。"""
    used, out, before_ch1 = set(), [], True
    for _, level, num, text in headings:
        anchor = slug(text, used)  # 所有标题都参与锚点去重，与 GitHub 渲染一致
        if level == 1 and num is not None:
            before_ch1 = False
            out.append(f"- [{text}](#{anchor})")
        elif level == 2 and num is not None:
            out.append(f"  - [{text}](#{anchor})")
        elif level == 2 and num is None and before_ch1 and text != "目录":
            out.append(f"- [{text}](#{anchor})")
    return out


def check_toc(lines, headings, write):
    try:
        start, end = lines.index(TOC_START), lines.index(TOC_END)
    except ValueError:
        return [f"design.md: 缺少 {TOC_START} / {TOC_END} 标记"], lines
    want = [""] + build_toc(headings) + [""]
    if lines[start + 1:end] == want:
        return [], lines
    if write:
        return [], lines[:start + 1] + want + lines[end:]
    return ["design.md: 目录与标题不一致，请运行 python3 scripts/check_design_refs.py --write"], lines


def main():
    write = "--write" in sys.argv[1:]
    with open(DESIGN, encoding="utf-8") as f:
        lines = f.read().split("\n")
    headings = parse_headings(lines)
    errors, valid = check_numbering(headings)
    toc_errors, new_lines = check_toc(lines, headings, write)
    if new_lines is not lines:
        with open(DESIGN, "w", encoding="utf-8") as f:
            f.write("\n".join(new_lines))
        print("已重新生成目录")
    errors += toc_errors + check_doc_refs(lines, valid) + check_repo_refs(valid)
    for e in errors:
        print(e)
    if errors:
        print(f"\n共 {len(errors)} 处问题")
        return 1
    print(f"设计文档校验通过（{len(valid)} 个编号章节）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
