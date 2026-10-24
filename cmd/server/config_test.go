package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeListen(t *testing.T) {
	cases := []struct {
		in, host, want string
		bad            bool
	}{
		{"9090", "127.0.0.1", "127.0.0.1:9090", false},
		{"443", "", ":443", false},
		{"0.0.0.0:8443", "127.0.0.1", "0.0.0.0:8443", false},
		{"[::1]:9000", "127.0.0.1", "[::1]:9000", false},
		{":8080", "127.0.0.1", ":8080", false},
		{"", "127.0.0.1", "", false},
		{"off", "", "", false}, // 环境变量关闭监听（修订第 221 条）
		{"OFF", "", "", false},
		{"0", "127.0.0.1", "", true},
		{"70000", "127.0.0.1", "", true},
		{"localhost", "127.0.0.1", "", true},
		{"1.2.3.4:http", "127.0.0.1", "", true},
	}
	for _, c := range cases {
		got, err := normalizeListen(c.in, c.host)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("normalizeListen(%q) = %q, %v", c.in, got, err)
		}
	}
}

func TestApplyEnv(t *testing.T) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8080", "")
	pub := fs.String("public-url", "", "")
	noSync := fs.Bool("no-release-sync", false, "")
	_ = fs.Parse([]string{"--public-url", "https://cli.example.com"})
	env := map[string]string{"VPSMON_LISTEN": "9090", "VPSMON_PUBLIC_URL": "https://env.example.com", "VPSMON_NO_RELEASE_SYNC": "true"}
	if err := applyEnv(fs, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if *listen != "9090" || *pub != "https://cli.example.com" || !*noSync {
		t.Fatalf("命令行应优先于环境变量，环境变量优先于默认值：%q %q %v", *listen, *pub, *noSync)
	}
	env = map[string]string{"VPSMON_NO_RELEASE_SYNC": "maybe"}
	fs2 := flag.NewFlagSet("run", flag.ContinueOnError)
	fs2.Bool("no-release-sync", false, "")
	_ = fs2.Parse(nil)
	if err := applyEnv(fs2, func(k string) string { return env[k] }); err == nil {
		t.Fatal("不合法的环境变量应报错，而不是忽略")
	}
}

func TestReadEnvFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.env")
	body := "# 注释\n\nVPSMON_DOMAIN=monitor.example.com\nVPSMON_LISTEN = 127.0.0.1:9090\nVPSMON_ACME_EMAIL=\"a@example.com\"\n" +
		"VPSMON_PUBLIC_URL='https://x.example.com'\nnot a pair\nVPSMON_EVIL=$(touch /tmp/pwned)\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := readEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"VPSMON_DOMAIN": "monitor.example.com", "VPSMON_LISTEN": "127.0.0.1:9090", "VPSMON_ACME_EMAIL": "a@example.com",
		"VPSMON_PUBLIC_URL": "https://x.example.com", "VPSMON_EVIL": "$(touch /tmp/pwned)"}
	if len(m) != len(want) {
		t.Fatalf("解析结果 %v", m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q，期望 %q（值原样保留，不经 shell 展开）", k, m[k], v)
		}
	}
	if m, err := readEnvFile(filepath.Join(t.TempDir(), "missing.env")); err != nil || len(m) != 0 {
		t.Fatalf("文件不存在时应返回空：%v %v", m, err)
	}

	// 进程环境变量优先于文件
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8080", "")
	domain := fs.String("domain", "", "")
	_ = fs.Parse(nil)
	proc := map[string]string{"VPSMON_LISTEN": "7070"}
	if err := applyEnv(fs, envLookup(func(k string) string { return proc[k] }, m)); err != nil {
		t.Fatal(err)
	}
	if *listen != "7070" || *domain != "monitor.example.com" {
		t.Fatalf("listen %q domain %q", *listen, *domain)
	}
}

func TestPortOf(t *testing.T) {
	for in, want := range map[string]string{":443": "443", "0.0.0.0:8443": "8443", "[::]:9443": "9443", "": "", "bad": ""} {
		if got := portOf(in); got != want {
			t.Errorf("portOf(%q) = %q，应为 %q", in, got, want)
		}
	}
}

func TestDefaultPublicURL(t *testing.T) {
	if got := defaultPublicURL("m.example.com", ":443"); got != "https://m.example.com" {
		t.Error(got)
	}
	if got := defaultPublicURL("m.example.com", ":8443"); got != "https://m.example.com:8443" {
		t.Error(got)
	}
}
