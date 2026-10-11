package protocol

import (
	"strings"
	"testing"
)

func TestWatchProcNames(t *testing.T) {
	for n, want := range map[string]bool{
		"nginx": true, "php-fpm8.2": true, "systemd-resolved": true, "postgres:": true,
		"": false, "a b": false, "$(id)": false, "x;y": false, "a/b": false, strings.Repeat("a", 65): false,
	} {
		if ValidProcName(n) != want {
			t.Errorf("ValidProcName(%q) 应为 %v", n, want)
		}
	}
	names := make([]string, 25)
	for i := range names {
		names[i] = string(rune('a'+i)) + "d"
	}
	if got := ParseWatchProcs(strings.Join(names, ",")); len(got) != MaxWatchProcs {
		t.Errorf("最多 %d 个：%d", MaxWatchProcs, len(got))
	}
	// comm 最多 15 个字符：长名称按前 15 个字符比较
	for _, c := range []struct {
		comm, name string
		want       bool
	}{
		{"nginx", "nginx", true},
		{"nginx", "ngin", false},
		{"systemd-resolve", "systemd-resolved", true},
		{"systemd-resolve", "systemd-resolveX", true},
		{"systemd-resolv", "systemd-resolved", false},
	} {
		if ProcNameMatches(c.comm, c.name) != c.want {
			t.Errorf("ProcNameMatches(%q, %q) 应为 %v", c.comm, c.name, c.want)
		}
	}
}
