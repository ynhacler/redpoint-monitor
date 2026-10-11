package protocol

import (
	"net/netip"
	"testing"
)

func TestProbeTargets(t *testing.T) {
	// 【安全】只允许公网地址
	for a, want := range map[string]bool{
		"220.181.173.35": true, "2400:da00::6666": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "224.0.0.1": false, "::1": false, "fd00::1": false, "fe80::1": false,
		"192.0.2.1": false, "::ffff:10.0.0.1": false, "255.255.255.255": false,
	} {
		if got := PublicProbeAddr(netip.MustParseAddr(a)); got != want {
			t.Errorf("PublicProbeAddr(%s) = %v", a, got)
		}
	}
	in := []ProbeTarget{
		{ID: 1, Proto: "tcp", Addr: netip.MustParseAddr("220.181.173.35"), Port: 80},
		{ID: 2, Proto: "icmp", Addr: netip.MustParseAddr("2400:da00::6666")},
	}
	enc := EncodeProbeTargets(in)
	if enc != "1,tcp,220.181.173.35,80;2,icmp,2400:da00::6666,0" {
		t.Fatalf("编码：%s", enc)
	}
	got := ParseProbeTargets(enc + ";3,tcp,10.0.0.1,22;4,udp,1.1.1.1,53;5,icmp,1.1.1.1,80;1,tcp,1.1.1.1,80;bad;6,tcp,1.1.1.1,0")
	if len(got) != 2 || got[0] != in[0] || got[1] != in[1] {
		t.Errorf("应只保留合法、不重复的目标：%+v", got)
	}
	if in[1].HostPort(in[1].Addr) != "[2400:da00::6666]:0" || in[0].HostPort(in[0].Addr) != "220.181.173.35:80" {
		t.Errorf("HostPort：%s %s", in[0].HostPort(in[0].Addr), in[1].HostPort(in[1].Addr))
	}
	if ProbeVersion("", 300) != "0" || ProbeVersion(enc, 300) == ProbeVersion(enc+";", 300) ||
		ProbeVersion(enc, 300) == ProbeVersion(enc, 60) || len(ProbeVersion(enc, 300)) != 12 {
		t.Error("版本：空列表为 0，内容或间隔不同版本不同")
	}
	for v, want := range map[string]int{"60": 60, "1800": 1800, "30": 300, "7200": 300, "x": 300, "": 300} {
		if got := ParseProbeInterval(v); got != want {
			t.Errorf("ParseProbeInterval(%q) = %d，应为 %d", v, got, want)
		}
	}
	var list []ProbeTarget
	for i := 1; i <= 100; i++ {
		list = append(list, ProbeTarget{ID: int64(i), Proto: "icmp", Addr: netip.MustParseAddr("1.1.1.1")})
	}
	if n := len(ParseProbeTargets(EncodeProbeTargets(list))); n != MaxProbeTargets {
		t.Errorf("最多 %d 个：%d", MaxProbeTargets, n)
	}
}

// 域名目标（修订第 132 条）：只接受正常的公网域名；解析结果只用公网地址，优先 IPv4。
func TestProbeHosts(t *testing.T) {
	for h, want := range map[string]bool{
		"speedtest.example-isp.cn": true, "a.b": true, "xn--fiqs8s.cn": true,
		"localhost": false, "db.local": false, "nas.lan": false, "x.internal": false, "router.home.arpa": false,
		"1.2.3.4": false, "a..b": false, "-a.cn": false, "a-.cn": false, "a_b.cn": false, "Upper.cn": false,
		"cn": false, "a.cn:80": false, "a.cn,b": false, "1.1.1.1.in-addr.arpa": false,
	} {
		if got := ValidProbeHost(h); got != want {
			t.Errorf("ValidProbeHost(%q) = %v", h, got)
		}
	}
	for in, want := range map[string]string{" Speed.Test.CN. ": "speed.test.cn", "https://www.zstaticcdn.com/": "www.zstaticcdn.com",
		"http://a.cn/x?y": "a.cn", "a.cn:8080": "a.cn:8080"} {
		if got := NormalizeProbeHost(in); got != want {
			t.Errorf("NormalizeProbeHost(%q) = %q，应为 %q", in, got, want)
		}
	}
	in := []ProbeTarget{{ID: 1, Proto: "tcp", Host: "speed.test.cn", Port: 80}, {ID: 2, Proto: "icmp", Addr: netip.MustParseAddr("1.1.1.1")}}
	enc := EncodeProbeTargets(in)
	if enc != "1,tcp,speed.test.cn,80;2,icmp,1.1.1.1,0" {
		t.Fatalf("编码：%s", enc)
	}
	got := ParseProbeTargets(enc + ";3,tcp,localhost,80;4,icmp,nas.lan,0")
	if len(got) != 2 || got[0] != in[0] || got[1] != in[1] {
		t.Errorf("域名与 IP 都能解析，内网名称跳过：%+v", got)
	}
	a := func(s string) netip.Addr { return netip.MustParseAddr(s) }
	if p, ok := PickProbeAddr([]netip.Addr{a("2400:da00::6666"), a("10.0.0.1"), a("220.181.173.35")}); !ok || p != a("220.181.173.35") {
		t.Errorf("优先公网 IPv4：%s", p)
	}
	if p, ok := PickProbeAddr([]netip.Addr{a("fd00::1"), a("2400:da00::6666")}); !ok || p != a("2400:da00::6666") {
		t.Errorf("没有 IPv4 时用公网 IPv6：%s", p)
	}
	// 【安全】解析到内网、回环时不探测（防止借 DNS 访问内网）
	if _, ok := PickProbeAddr([]netip.Addr{a("127.0.0.1"), a("192.168.1.1"), a("::1")}); ok {
		t.Error("只有内网地址时不应探测")
	}
}

func TestProbeTiming(t *testing.T) {
	for v, want := range map[string]ProbeTiming{
		"": {4, 1}, "6,2": {6, 2}, "0,0": {4, 1}, "11,3": {4, 3}, "x,y": {4, 1}, "2": {4, 1}, " 10 , 10 ": {10, 10},
	} {
		if got := ParseProbeTiming(v); got != want {
			t.Errorf("ParseProbeTiming(%q) = %+v，应为 %+v", v, got, want)
		}
	}
	if (ProbeTiming{6, 2}).Encode() != "6,2" {
		t.Error("编码")
	}
}
