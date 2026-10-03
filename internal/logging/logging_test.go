package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"Agent Token 只保留前缀与末 4 位（设计 24.7）", "token agt_kza3abcdefghijklmnopr5sx used", "token agt_…r5sx used"},
		{"管理员 Token", "adm_7mfj63xp5eenxx7iapm7n5aad4mie4y2", "adm_…e4y2"},
		{"Refresh Token 前缀为 3 位", "rt_abcdefghijkl9z9z", "rt_…9z9z"},
		{"Device Token", "Bearer dev_abcdefghij1234", "Bearer dev_…1234"},
		{"注册码", "enroll ENR-7KQ2-9XPA-M4TD-H3WC failed", "enroll ENR-…H3WC failed"},
		{"AK", "MNT-ABCD-EFGH-IJKL", "MNT-…IJKL"},
		{"一行中多个凭证", "a=agt_aaaaaaaaaaaa1111 b=adm_bbbbbbbbbbbb2222", "a=agt_…1111 b=adm_…2222"},
		{"普通单词不误伤", "redemption_value admin_panel ENROLL", "redemption_value admin_panel ENROLL"},
		{"过短的不算凭证", "agt_abc", "agt_abc"},
		{"Telegram Bot Token（通知渠道）", "post https://api.telegram.org/bot123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawQ/sendMessage",
			"post https://api.telegram.org/bot123456789:…sawQ/sendMessage"},
		{"时间不是 Bot Token", "at 12:30:45 port 8080:443", "at 12:30:45 port 8080:443"},
	}
	for _, c := range cases {
		if got := Redact(c.in); got != c.want {
			t.Errorf("%s: Redact(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// 构造包含各类凭证的日志，断言输出中不出现完整值（设计 24.7 的单元测试要求）。
func TestLoggerNeverWritesFullCredentials(t *testing.T) {
	secrets := []string{
		"adm_7mfj63xp5eenxx7iapm7n5aad4mie4y2",
		"agt_kza3abcdefghijklmnopqrstuvr5sx",
		"dev_abcdefghijklmnop1234",
		"rt_abcdefghijklmnop5678",
		"MNT-ABCD-EFGH-IJKL-MNOP",
		"ENR-7KQ2-9XPA-M4TD-H3WC",
	}
	for _, format := range []string{"json", "text"} {
		var buf bytes.Buffer
		log, err := New(&buf, format, slog.LevelDebug)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range secrets {
			log.Info("msg with "+s, "field", s, "err", errors.New("wrapped: "+s))
			log.With("ctx", s).Warn("x")
			log.Info("group", slog.Group("g", "inner", s))
		}
		out := buf.String()
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Errorf("%s: 输出中出现完整凭证 %q", format, s)
			}
		}
		if !strings.Contains(out, "agt_…r5sx") {
			t.Errorf("%s: 脱敏后应保留前缀与末 4 位，实际输出：%s", format, out)
		}
	}
}

func TestNewRejectsUnknownFormat(t *testing.T) {
	if _, err := New(&bytes.Buffer{}, "xml", slog.LevelInfo); err == nil {
		t.Fatal("未知格式应返回错误")
	}
}
