package server

import (
	"strings"
	"testing"
	"time"
)

func TestMaskIPs(t *testing.T) {
	cases := map[string]string{
		`"ip":"203.0.113.45"`:                   `"ip":"203.0.x.x"`,
		"from 10.1.2.3 and 2001:db8:85a3::8a2e": "from 10.1.x.x and 2001:…",
		"fe80::1%eth0":                          "fe80:…%eth0",
		"2026-10-04T01:20:47.123+08:00 ok":      "2026-10-04T01:20:47.123+08:00 ok", // 时间不是 IPv6
		"v0.2.0-31-g68aee58":                    "v0.2.0-31-g68aee58",
	}
	for in, want := range cases {
		if got := MaskIPs(in); got != want {
			t.Errorf("MaskIPs(%q) = %q，应为 %q", in, got, want)
		}
	}
}

// 诊断包只含汇总：不含节点名、IP、通知渠道地址与凭证（设计 24.10、24.7）。
func TestDiagnose(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	id, _, _ := st.CreateServer("secret-node-name", 0, 1)
	st.DB.Exec(`UPDATE servers SET ipv4 = '198.51.100.7', hostname = 'secret-host' WHERE id = ?`, id)
	st.DB.Exec(`INSERT INTO notification_channels (name, type, config, enabled, min_severity, notify_resolved, created_at, updated_at)
		VALUES ('tg', 'telegram', '{"bot_token":"123456:ABCdefSECRET","chat_id":"42"}', 1, 'info', 1, 0, 0)`)
	logs := `{"msg":"login ok","ip":"198.51.100.7","token":"agt_abcdefghijklmnopqrst"}` + "\n"
	files := Diagnose(dir, "v-test", logs, time.Now())
	var all strings.Builder
	names := map[string]bool{}
	for _, f := range files {
		names[f.Name] = true
		all.Write(f.Data)
	}
	for _, n := range []string{"README.txt", "version.txt", "database.txt", "summary.txt", "logs.txt"} {
		if !names[n] {
			t.Errorf("缺少 %s", n)
		}
	}
	text := all.String()
	for _, leak := range []string{"secret-node-name", "secret-host", "198.51.100.7", "ABCdefSECRET", "agt_abcdefghijklmnopqrst"} {
		if strings.Contains(text, leak) {
			t.Errorf("【安全】诊断包中不应出现 %q", leak)
		}
	}
	for _, want := range []string{"节点                           1", "通知渠道 telegram（启用）", "quick_check  ok", "198.51.x.x"} {
		if !strings.Contains(text, want) {
			t.Errorf("诊断包应包含 %q：\n%s", want, text)
		}
	}
}
