// Package alog 是 Agent 的日志（设计 24.5）：每行都有级别，便于筛选与告警。
//
//   - systemd 下（环境变量 JOURNAL_STREAM）：行首加 <3> / <4> / <6>，journald 据此记录真实优先级
//     （journalctl -u vpsmon-agent -p warning 只看警告与错误）；不写时间，journald 自带
//   - 其他环境（OpenRC 经 logger 写入 syslog、前台运行）：时间 + 级别文字，如 “2026/10/06 21:00:00 WARN …”
//
// 级别由调用方显式选择（Infof / Warnf / Errorf）；Printf 兼容旧的写法，按消息中的 “ERROR” / “WARN” 判断。
// 【安全】日志中不写凭证：调用方只传入已脱敏的内容（设计 24.7）。
package alog

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Level 是日志级别，数值即 syslog 优先级。
type Level int

const (
	Error Level = 3
	Warn  Level = 4
	Info  Level = 6
)

var (
	mu       sync.Mutex
	out      io.Writer = os.Stderr
	journald           = os.Getenv("JOURNAL_STREAM") != ""
	now                = time.Now
)

// SetOutput 替换输出（测试用）；journal 为 true 时按 journald 格式输出。
func SetOutput(w io.Writer, journal bool) {
	mu.Lock()
	defer mu.Unlock()
	out, journald = w, journal
}

func (l Level) String() string {
	switch l {
	case Error:
		return "ERROR"
	case Warn:
		return "WARN"
	}
	return "INFO"
}

// Logf 按级别写一行。消息中已带级别文字（旧写法 “report: WARN …”）时不重复。
func Logf(l Level, format string, args ...any) {
	msg := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	mu.Lock()
	defer mu.Unlock()
	if journald {
		fmt.Fprintf(out, "<%d>%s\n", l, msg)
		return
	}
	tag := l.String() + " "
	if hasLevelWord(msg) {
		tag = ""
	}
	fmt.Fprintf(out, "%s %s%s\n", now().Format("2006/01/02 15:04:05"), tag, msg)
}

func Infof(format string, args ...any)  { Logf(Info, format, args...) }
func Warnf(format string, args ...any)  { Logf(Warn, format, args...) }
func Errorf(format string, args ...any) { Logf(Error, format, args...) }

// Printf 兼容 log.Printf 的旧写法：消息开头部分带 “ERROR” / “WARN” 时用对应级别，否则为 INFO。
func Printf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	Logf(levelOf(msg), "%s", msg)
}

func levelOf(msg string) Level {
	head := msg
	if len(head) > 48 {
		head = head[:48]
	}
	switch {
	case strings.Contains(head, "ERROR"):
		return Error
	case strings.Contains(head, "WARN"):
		return Warn
	}
	return Info
}

func hasLevelWord(msg string) bool { return levelOf(msg) != Info }

// RedirectStd 让仍使用标准库 log 的代码（第三方库等）也按 INFO 级别输出到这里。
func RedirectStd() {
	log.SetFlags(0)
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		Printf("%s", strings.TrimRight(string(p), "\n"))
		return len(p), nil
	}))
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Throttle 限制重复日志：同一 key 在 every 内只记录一次，下次记录时附带重复次数（设计 24.5 防刷屏）。
// 例如升级检查每 5 分钟失败一次时，每小时一行 “upgrade check failed (x12): …”。
type Throttle struct {
	Every    time.Duration
	mu       sync.Mutex
	last     string
	lastAt   time.Time
	repeated int
}

// Logf 记录一行；key 相同且在 Every 内时只计数。返回是否真的写了日志。
func (t *Throttle) Logf(l Level, key, format string, args ...any) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := now()
	if key == t.last && n.Sub(t.lastAt) < t.Every {
		t.repeated++
		return false
	}
	msg := fmt.Sprintf(format, args...)
	if key == t.last && t.repeated > 0 {
		msg = fmt.Sprintf("%s (x%d)", msg, t.repeated+1)
	}
	Logf(l, "%s", msg)
	t.last, t.lastAt, t.repeated = key, n, 0
	return true
}

// Reset 在问题恢复后调用：下次出错立即记录。
func (t *Throttle) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last, t.repeated = "", 0
}
