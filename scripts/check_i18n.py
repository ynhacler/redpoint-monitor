#!/usr/bin/env python3
"""界面翻译检查（设计 52.2）：
1. web/src 中每个 t('中文原文') 都要在 locales/en.ts 中有译文，译文中的 {变量} 与原文一致。
   t() 第一个参数里的字面量都算（包括 t(a ? '甲' : '乙')）；t(变量) 的翻译由提供变量的地方负责。
2. .vue 模板中不能有 t() 之外的中文（文字、属性值、表达式里的字符串都算，注释除外），
   否则英文界面下这些文字仍是中文（修订第 296 条）。确实要显示中文的写进 TEMPLATE_ALLOW。
3. internal/server 写给客户端的中文错误（errorf、APIError / FieldError 的 Message、bad / fail 校验闭包、
   codeInfo 默认提示）都要在 en.ts 或 enServer.ts 中有译文；带 %d、%s 或拼接变量的文案要能被 enServerPatterns
   匹配，且匹配后不再含中文（修订第 297 条）。错误文案来自变量时要在 ERROR_SOURCES 中写明出处，
   只发给 Agent / App、Web 不会显示的写进 SERVER_ALLOW；两张表中不再出现的条目同样报错。

用法：python3 scripts/check_i18n.py          检查，有缺漏时退出码为 1
      python3 scripts/check_i18n.py --list   只列出缺少译文的原文（每行一个，便于补充）
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
SRC = ROOT / "web" / "src"
EN = SRC / "locales" / "en.ts"
EN_SERVER = SRC / "locales" / "enServer.ts"
SERVER = ROOT / "internal" / "server"

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

# 写给客户端的错误文案出现的位置（设计 43.4）。bad / fail 是校验闭包，排除 loginLimit.fail(ip) 与它的方法定义。
ERROR_ARG = re.compile(r"\berrorf\(\s*[\w.]+\s*,\s*"
                       r"|\bAPIError\{[^{}]*?\bMessage:\s*"
                       r"|\bField:\s*[^,{}]+,\s*Message:\s*")
ERROR_CALL = re.compile(r"(?<![\w.])(?<!\) )(?:bad|fail)\(")
CODE_INFO = re.compile(r"\{http\.Status\w+,\s*(?=\")")
GO_STR = re.compile(r'"((?:[^"\\\n]|\\.)*)"')
GO_VERB = re.compile(r"%[-+# 0]*[\d.]*[a-zA-Z%]")
SPRINTF = re.compile(r"^(?:fmt\.Sprintf|fmt\.Errorf|errors\.New)\(")
REPLACE = re.compile(r'strings\.Replace\(\w+, "(.+?)", "(.+?)", 1\)')
# 变量形式的错误文案 → 文案的出处：(文件名, 表达式) → 产生它的函数（方法写成 类型.方法）。这些函数中的中文返回值、
# errors.New、fmt.Errorf、赋值与表格行都按错误文案检查；strings.Replace 的替换同样代入。
# 空元组表示这里不是新文案。新增变量形式的错误时必须登记，否则检查看不到它的内容。
ERROR_SOURCES: dict[tuple[str, str], tuple[str, ...]] = {
    ("apierror.go", "message"): (),  # errorf 自身
    ("batch.go", "e.Message"): (),  # 转写 validate 已产出的字段错误
    ("listen.go", "err"): (),  # fail(field, addr, err) 的最后一个参数不是文案，文案写在闭包内
    ("cloud_sync.go", "err.Error()"): ("ParseOCIKey",),
    ("heartbeat.go", "msg"): ("checkWebhookURL", "heartbeatInput.validate"),
    ("listen.go", "msg"): ("normalizeAddr",),
    ("nodes.go", "msg"): ("normLineTags",),
    ("notify.go", "msg"): ("checkWebhookURL",),
    ("notify.go", 'strings.Replace(msg, "Webhook", "ntfy 服务器", 1)'): ("checkWebhookURL",),
    ("notify_chat.go", "msg"): ("checkWebhookURL",),
    ("notify_chat.go", 'strings.Replace(msg, "Webhook", "Bark 服务器", 1)'): ("checkWebhookURL",),
    ("port_baseline.go", "err.Error()"): ("normalizePorts", "mergePortLabels", "handlePortBaseline"),
    ("probe.go", "msg"): ("handleCreateProbeTarget",),
    # 镜像失败的具体原因由 %w 带出，只翻译外层（面板生成的内容）
    ("releases.go", "err.Error()"): ("syncReleases", "fetchLimited"),
    ("service_monitor.go", "msg"): ("normalizeServiceTarget",),
    ("session.go", "msg"): ("validatePassword",),
    ("ssl.go", "msg"): ("parseSSLTarget",),
    ("subscriptions.go", "c.msg"): ("validateSubVPS",),
}
# Web 不会显示的错误：文案（变量写成 %s）→ 原因。enServer.ts 不收录这些内容。
AGENT_ONLY = "Agent 接口的回应，只进 Agent 日志与安装输出"
APP_ONLY = "App 设备凭证与授权检查，只有 App 收到，App 自带中文提示"
WS_ONLY = "WebSocket 握手失败时浏览器拿不到响应内容，Web 只知道连接失败"
SERVER_ALLOW: dict[str, str] = {
    "Agent 凭证无效或已被吊销，请重新注册": AGENT_ONLY,
    "不支持的内容编码：%s": AGENT_ONLY,
    "主机信息与节点设置不一致，已拒绝注册：%s": AGENT_ONLY,
    "面板没有上一份完整上报，请发送完整上报": AGENT_ONLY,
    "设备凭证无效": APP_ONLY,
    "当前设备授权已失效，请重新通过 Web 管理端生成 AK 配对": APP_ONLY,
    "此设备的授权不包含静音与维护操作": APP_ONLY,
    "此设备的授权不包含记录订阅：在 Web 创建配对 AK 时授权全部节点并勾选“允许记录订阅”": APP_ONLY,
    "需要 WebSocket 连接": WS_ONLY,
    "不支持的 WebSocket 版本": WS_ONLY,
    "WebSocket 握手不正确": WS_ONLY,
    "实时连接过多，请稍后再试": WS_ONLY,
}


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


def go_code(text: str) -> str:
    """去掉 Go 注释（换成空格，保留换行与位置）；字符串内的 // 不算注释。"""
    out, i = list(text), 0
    while i < len(text):
        c = text[i]
        if c in "\"'":
            i += 1
            while text[i] != c:
                i += 2 if text[i] == "\\" else 1
            i += 1
        elif c == "`":
            i = text.index("`", i + 1) + 1
        elif text.startswith(("//", "/*"), i):
            end = text.find("\n", i) if text[i + 1] == "/" else text.index("*/", i) + 2
            end = len(text) if end < 0 else end
            out[i:end] = [ch if ch == "\n" else " " for ch in text[i:end]]
            i = end
        else:
            i += 1
    return "".join(out)


def go_args(code: str, start: int) -> tuple[list[str], int]:
    """从 start 开始按顶层逗号切分参数，到右括号为止；返回各参数和结束位置。字符串内的符号不算。"""
    args, depth, i, begin, quote = [], 0, start, start, ""
    while i < len(code):
        c = code[i]
        if quote:
            if c == "\\" and quote != "`":
                i += 1
            elif c == quote:
                quote = ""
        elif c in "\"`'":
            quote = c
        elif c in "([{":
            depth += 1
        elif c in ")]}" and depth > 0:
            depth -= 1
        elif c in ")]}" or (c == "," and depth == 0):
            args.append(code[begin:i].strip())
            if c != ",":
                return args, i
            begin = i + 1
        i += 1
    return args, i


def split_plus(expr: str) -> list[str]:
    """按顶层 + 切开字符串表达式。"""
    parts, depth, begin, quote, i = [], 0, 0, "", 0
    while i < len(expr):
        c = expr[i]
        if quote:
            if c == "\\" and quote != "`":
                i += 1
            elif c == quote:
                quote = ""
        elif c in "\"`'":
            quote = c
        elif c in "([{":
            depth += 1
        elif c in ")]}":
            depth -= 1
        elif c == "+" and depth == 0:
            parts.append(expr[begin:i].strip())
            begin = i + 1
        i += 1
    return parts + [expr[begin:].strip()]


def go_template(expr: str) -> str:
    """把 Go 字符串表达式还原成文案，变量写成 %s：'"节点 " + id + " 不存在"' → '节点 %s 不存在'。"""
    out = ""
    for part in split_plus(expr):
        if (s := GO_STR.fullmatch(part)) is not None:
            out += unescape(s.group(1))
        elif (call := SPRINTF.match(part)) is not None and (s := GO_STR.match(go_args(part, call.end())[0][0])):
            out += unescape(s.group(1))
        else:
            out += "%s"
    return out


def server_sources() -> dict[str, str]:
    """internal/server 与它引用的 cloud 包的 Go 代码（去掉注释），键是相对仓库根目录的路径。"""
    files = [f for d in (SERVER, ROOT / "internal" / "cloud") for f in sorted(d.glob("*.go"))]
    return {f.relative_to(ROOT).as_posix(): go_code(f.read_text(encoding="utf-8"))
            for f in files if not f.name.endswith("_test.go")}


def func_body(sources: dict[str, str], name: str) -> tuple[str, str, int] | None:
    """按函数名找函数体：返回 (文件, 函数代码, 起始位置)。name 是 函数 或 类型.方法。"""
    recv, _, fn = name.rpartition(".")
    head = re.compile(r"^func (?:\([^)]*" + recv + r"\) )?" + fn + r"\(.*?^}", re.M | re.S)
    for path, code in sources.items():
        if (m := head.search(code)) is not None:
            return path, m.group(0), m.start()
    return None


def source_messages(body: str):
    """逐个产出函数体内可能写给客户端的中文表达式：(表达式, 位置)。"""
    for m in re.finditer(r"\b(?:errors\.New|fmt\.Errorf)\(", body):
        yield go_args(body, m.end())[0][0], m.start()
    for m in re.finditer(r"^\s*return\b(?=.*\")(.*)$", body, re.M):
        yield go_args(m.group(1) + ")", 0)[0][-1], m.start()
    for m in re.finditer(r"^\s*\w+\s*:?=\s+(?!append\()(.*\".*)$", body, re.M):
        yield m.group(1), m.start()
    for m in re.finditer(r'^\s*\{".*,\s*("(?:[^"\\\n]|\\.)*")\},$', body, re.M):
        yield m.group(1), m.start()


def closure_param(code: str, pos: int, expr: str) -> bool:
    """校验闭包把参数转写进 FieldError 时，文案在调用处检查。闭包定义在本行或上一行。"""
    start = code.rfind("\n", 0, code.rfind("\n", 0, pos)) + 1
    around = code[start:code.find("\n", pos)]
    return re.search(r"func\([^)]*\b" + re.escape(expr) + r"\b[^)]*\)", around) is not None


def server_errors() -> tuple[dict[str, str], list[str], list[str]]:
    """扫描写给客户端的错误：返回 {文案: 出处}、没有登记出处的变量位置、过期的 ERROR_SOURCES 条目。"""
    sources = server_sources()
    found: dict[str, str] = {}
    unknown: list[str] = []
    used: set[tuple[str, str]] = set()

    def line(path: str, pos: int) -> str:
        return f"{path}:{sources[path].count(chr(10), 0, pos) + 1}"

    for path, code in sources.items():
        if not path.startswith("internal/server/"):
            continue
        name = path.rsplit("/", 1)[1]
        sites = [(go_args(code, m.end())[0][0], m.start()) for m in ERROR_ARG.finditer(code)]
        sites += [(go_args(code, m.end())[0][-1], m.start()) for m in ERROR_CALL.finditer(code)]
        if name == "apierror.go":
            sites += [(go_args(code, m.end())[0][0], m.start()) for m in CODE_INFO.finditer(code)]
        for expr, pos in sites:
            if (name, expr) in ERROR_SOURCES:
                used.add((name, expr))
            elif CJK.search(expr):
                found.setdefault(go_template(expr), line(path, pos))
            elif not expr.startswith('"') and not closure_param(code, pos, expr):
                unknown.append(f"{line(path, pos)}：{expr}")
    for (_, expr), funcs in ERROR_SOURCES.items():
        swap = REPLACE.fullmatch(expr)
        for fn in funcs:
            hit = func_body(sources, fn)
            if hit is None:
                unknown.append(f"ERROR_SOURCES 中的函数不存在：{fn}")
                continue
            path, body, offset = hit
            for msg, pos in source_messages(body):
                if CJK.search(msg):
                    msg = go_template(msg)
                    found.setdefault(msg.replace(swap[1], swap[2]) if swap else msg, line(path, offset + pos))
    stale = [f"{k[0]}：{k[1]}" for k in ERROR_SOURCES if k not in used]
    return found, unknown, stale


def server_patterns() -> list[tuple[re.Pattern, str]]:
    """读出 enServerPatterns：[/正则/, '译文'] → (正则, 译文)。"""
    text = EN_SERVER.read_text(encoding="utf-8")
    text = text[text.index("enServerPatterns"):]
    found = re.findall(r"^\s*\[/(.+)/, '((?:[^'\\\n]|\\.)*)'\],$", text, re.M)
    return [(re.compile(p), re.sub(r"\$(\d)", r"\\g<\1>", unescape(v))) for p, v in found]


def translated(msg: str, entries: dict[str, str], patterns: list[tuple[re.Pattern, str]]) -> bool:
    """文案能否译成没有中文的英文。与 i18n.ts 的 tServer() 相同：先查固定条目，再按顺序匹配第一个模式。
    变量用样例值代入：%d 是数字，%s 既可能是文字也可能是拼接的数字，两种都试。"""
    if msg in entries:
        return True
    for word in ("abc", "12"):
        sample = GO_VERB.sub(lambda v: {"%%": "%", "%d": "12", "%f": "1.5"}.get(v.group(0), word), msg)
        if sample in entries:
            return True
        for pattern, template in patterns:
            if pattern.search(sample):
                if not CJK.search(pattern.sub(template, sample)):
                    return True
                break
    return False


def main() -> int:
    entries = {}
    server_entries = {}
    for path, table in ((EN, entries), (EN_SERVER, server_entries)):
        body = re.sub(r"^\s*//.*$", "", path.read_text(encoding="utf-8"), flags=re.M)  # 去掉整行注释
        for m in ENTRY.finditer(body):
            table[unescape(m.group(1))] = unescape(m.group(2))

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

    errors, unknown, stale_sources = server_errors()
    patterns = server_patterns()
    known = entries | server_entries
    untranslated = {k: v for k, v in errors.items() if not translated(k, known, patterns)}
    no_server = {k: v for k, v in untranslated.items() if k not in SERVER_ALLOW}
    stale_allow = [k for k in SERVER_ALLOW if k not in untranslated]

    if "--list" in sys.argv:
        for k in [*missing, *no_server]:
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
    for k, where in no_server.items():
        print(f"接口错误缺少英文译文：{k!r}（{where}）")
    for where in unknown:
        print(f"错误文案来自变量，请在 ERROR_SOURCES 中登记出处：{where}")
    for where in stale_sources:
        print(f"ERROR_SOURCES 中的条目已不存在，请删除：{where}")
    for k in stale_allow:
        print(f"SERVER_ALLOW 中的条目已不存在或已有译文，请删除：{k!r}")
    server_bad = len(no_server) + len(unknown) + len(stale_sources) + len(stale_allow)
    if missing or bad_vars or raw or stale or server_bad:
        print(f"界面翻译检查未通过：缺少 {len(missing)} 条，变量不一致 {len(bad_vars)} 条（web/src/locales/en.ts），"
              f"模板未翻译 {len(raw)} 处，名单过期 {len(stale)} 条，"
              f"接口错误问题 {server_bad} 条（web/src/locales/enServer.ts）")
        return 1
    print(f"界面翻译检查通过（{len(entries)} 条译文，接口错误 {len(errors)} 条）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
