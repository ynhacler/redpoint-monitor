// Package logging 提供面板与 Agent 共用的结构化日志（设计 24.3）与凭证脱敏（设计 24.7）。
//
// 日志只写到本机（stdout → journald），不上传任何地方（设计 1.6.16、24.1）。
// 不负责：审计日志与事件，它们写入面板数据库，与运行日志分开（设计 24.2）。
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

// 【安全】按前缀识别凭证（设计 24.7）：
//
//	adm_ / agt_ / dev_ / rt_   小写 base32 Token（auth.go 的 NewToken）
//	MNT- / ENR-                AK 与注册码，大写分组格式（设计 8.4、27.4）
//
// 前缀前要求不是字母或数字，避免误伤 “redemption_” 之类的普通单词。
var credential = regexp.MustCompile(`(^|[^A-Za-z0-9])((?:adm|agt|dev|rt)_[a-z0-9]{8,}|(?:MNT|ENR)-[A-Z0-9][A-Z0-9-]{7,})`)

// Redact 把字符串中出现的凭证替换为“前缀 + … + 末 4 位”，例如 agt_…r5sx（设计 24.7）。
// 保留前缀便于判断是哪类凭证，保留末 4 位便于与用户手里的凭证对照，其余部分不可恢复。
func Redact(s string) string {
	idx := credential.FindAllStringSubmatchIndex(s, -1)
	if idx == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range idx {
		start, end := m[4], m[5] // 第 2 个分组：凭证本身（不含前导分隔符）
		tok := s[start:end]
		cut := strings.IndexAny(tok, "_-") + 1
		b.WriteString(s[last:start])
		b.WriteString(tok[:cut] + "…" + tok[len(tok)-4:])
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// New 创建日志器。format 为 "json"（默认，便于检索）或 "text"（开发时易读）。
//
// 【安全】所有字符串属性、错误与消息在输出前统一经过 Redact，调用方即使误把凭证
// 放进日志字段，也不会完整落盘（设计 24.7）。
func New(w io.Writer, format string, level slog.Level) (*slog.Logger, error) {
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr}
	switch format {
	case "", "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	}
	return nil, fmt.Errorf("unknown log format %q (want json or text)", format)
}

// redactAttr 是 slog 的 ReplaceAttr 钩子：对字符串与 error 类型的值做脱敏；
// 内置的 msg 字段同样会经过这里。
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(Redact(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(Redact(err.Error()))
		}
	}
	return a
}

// ParseLevel 解析 debug / info / warn / error（设计 24.4，默认 info）。
func ParseLevel(s string) (slog.Level, error) {
	var l slog.Level
	err := l.UnmarshalText([]byte(s))
	return l, err
}
