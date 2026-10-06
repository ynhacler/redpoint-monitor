package alog

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestJournaldPriority(t *testing.T) {
	var b bytes.Buffer
	SetOutput(&b, true)
	Infof("started %s", "0.4.1")
	Warnf("clock skew")
	Printf("report: ERROR token rejected")
	want := "<6>started 0.4.1\n<4>clock skew\n<3>report: ERROR token rejected\n"
	if b.String() != want {
		t.Fatalf("journald 输出：%q", b.String())
	}
}

func TestTextLevel(t *testing.T) {
	var b bytes.Buffer
	SetOutput(&b, false)
	now = func() time.Time { return time.Date(2026, 10, 6, 21, 0, 0, 0, time.UTC) }
	defer func() { now = time.Now }()
	Warnf("disk full")
	Printf("report: WARN local clock is ahead")
	Printf("collect: ok")
	got := b.String()
	for _, w := range []string{"2026/10/06 21:00:00 WARN disk full\n", "2026/10/06 21:00:00 report: WARN local clock is ahead\n",
		"2026/10/06 21:00:00 INFO collect: ok\n"} {
		if !strings.Contains(got, w) {
			t.Errorf("缺少 %q：\n%s", w, got)
		}
	}
}

func TestThrottle(t *testing.T) {
	var b bytes.Buffer
	SetOutput(&b, true)
	cur := time.Unix(0, 0)
	now = func() time.Time { return cur }
	defer func() { now = time.Now }()
	th := Throttle{Every: time.Hour}
	for i := 0; i < 12; i++ {
		th.Logf(Warn, "e", "upgrade check failed: timeout")
		cur = cur.Add(5 * time.Minute)
	}
	th.Logf(Warn, "e", "upgrade check failed: timeout") // 满一小时：带次数再记一行
	th.Logf(Warn, "other", "upgrade check failed: 502") // 不同的错误立即记录
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[1], "(x12)") || !strings.Contains(lines[2], "502") {
		t.Fatalf("防刷屏：%q", lines)
	}
}
