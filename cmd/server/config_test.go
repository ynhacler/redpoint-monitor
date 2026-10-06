package main

import (
	"flag"
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
