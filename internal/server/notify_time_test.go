package server

import (
	"strings"
	"testing"
	"time"
)

// 推送正文带北京时间的发送时间（修订第 321 条）；另存为系统事件时不带。
func TestNotifyBeijingTime(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 5, 9, 0, time.UTC) // 北京时间 14:05:09
	start := now.Add(-12 * time.Minute)
	m := notifyMessage{Kind: NotifyFiring, Severity: SeverityWarning, ServerName: "hk", Message: "磁盘 / 使用率 96%",
		StartedAt: start, Link: "https://m.example.com/servers/1"}
	got := m.text(now)
	lines := strings.Split(got, "\n")
	if lines[len(lines)-1] != m.Link || lines[len(lines)-2] != "🕒 2026-11-08 14:05:09（北京时间）" {
		t.Fatalf("time line before link: %q", got)
	}
	if !strings.Contains(got, "开始时间 2026-11-08 13:53") {
		t.Fatalf("started at in Beijing time: %q", got)
	}
	if strings.Contains(m.render(now, false), "🕒") {
		t.Fatal("system event body must not carry the send time")
	}
	en := m.withLang(healthLang("en")).text(now)
	if !strings.Contains(en, "🕒 2026-11-08 14:05:09 (Beijing time, UTC+8)") {
		t.Fatalf("english: %q", en)
	}
	// 批量离线、提醒同样带时间
	for _, x := range []notifyMessage{
		{Kind: NotifyFiring, Severity: SeverityCritical, Servers: []string{"a", "b"}, Count: 2},
		{Kind: NotifyReminder, Severity: SeverityInfo, ServerName: "Netflix", Message: "明天扣费", Lines: []string{"付款人：A"}},
		{Kind: NotifyTest, Severity: SeverityInfo},
	} {
		if !strings.Contains(x.text(now), "🕒 2026-11-08 14:05:09（北京时间）") {
			t.Fatalf("%s: %q", x.Kind, x.text(now))
		}
	}
}
