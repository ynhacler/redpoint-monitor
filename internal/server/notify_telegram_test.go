package server

import (
	"strings"
	"testing"
	"time"
)

// Telegram 正文为 HTML（修订第 322 条）：标题加粗、链接写成“查看详情”，用户内容一律转义。
func TestTelegramHTML(t *testing.T) {
	now := time.Date(2026, 11, 9, 1, 32, 1, 0, time.UTC)
	m := notifyMessage{Kind: NotifyFiring, Severity: SeverityCritical, ServerName: "<b>hk</b> & co", Message: "超过 120 秒未收到上报",
		StartedAt: now, Link: "https://m.example.com/servers/1?a=1&b=2"}
	got := m.telegramHTML(now)
	lines := strings.Split(got, "\n")
	if !strings.HasPrefix(lines[0], "<b>🔴 &lt;b&gt;hk&lt;/b&gt; &amp; co ") || !strings.HasSuffix(lines[0], "</b>") {
		t.Fatalf("title: %q", lines[0])
	}
	if lines[len(lines)-1] != `<a href="https://m.example.com/servers/1?a=1&amp;b=2">查看详情</a>` {
		t.Fatalf("link: %q", lines[len(lines)-1])
	}
	if !strings.Contains(got, "🕒 2026-11-09 09:32:01（北京时间）") {
		t.Fatalf("time: %q", got)
	}
	if en := m.withLang(healthLang("en")).telegramHTML(now); !strings.Contains(en, ">View details</a>") {
		t.Fatalf("english: %q", en)
	}
	// 没有链接时最后一行照常转义，不生成 <a>
	m.Link = ""
	if strings.Contains(m.telegramHTML(now), "<a ") {
		t.Fatal("no link expected")
	}
}
