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
	if in[1].HostPort() != "[2400:da00::6666]:0" || in[0].HostPort() != "220.181.173.35:80" {
		t.Errorf("HostPort：%s %s", in[0].HostPort(), in[1].HostPort())
	}
	if ProbeVersion("") != "0" || ProbeVersion(enc) == ProbeVersion(enc+";") || len(ProbeVersion(enc)) != 12 {
		t.Error("版本：空列表为 0，内容不同版本不同")
	}
	var list []ProbeTarget
	for i := 1; i <= 100; i++ {
		list = append(list, ProbeTarget{ID: int64(i), Proto: "icmp", Addr: netip.MustParseAddr("1.1.1.1")})
	}
	if n := len(ParseProbeTargets(EncodeProbeTargets(list))); n != MaxProbeTargets {
		t.Errorf("最多 %d 个：%d", MaxProbeTargets, n)
	}
}
