package collector

import "testing"

// IP 协议栈（修订第 279 条）：双栈、只有 IPv4、只有 IPv6（NAT 机器的 IPv4 也算），ULA 与拒绝路由不算。
func TestParseIPStack(t *testing.T) {
	const routeHead = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"
	route4 := routeHead + "eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	routeLocal := routeHead + "eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	global6 := "2a0102470000000000000000000000ab 02 40 00 80 eth0\n" +
		"fe80000000000000021132fffe445566 02 40 20 80 eth0\n" +
		"00000000000000000000000000000001 01 80 10 80 lo\n"
	ula6 := "fd00000000000000000000000000000a 03 40 00 80 docker0\n" +
		"fe80000000000000021132fffe445566 02 40 20 80 eth0\n"
	def6 := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000400 00000001 00000000 00000003 eth0\n"
	reject6 := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200 lo\n"
	for _, c := range []struct {
		name                 string
		route, inet6, route6 string
		v4, v6               bool
	}{
		{"双栈", route4, global6, def6, true, true},
		{"只有 IPv4（内核有 IPv6，只有链路本地地址与拒绝路由）", route4, "fe80000000000000021132fffe445566 02 40 20 80 eth0\n", reject6, true, false},
		{"只有 IPv6（没有 IPv4 默认路由）", routeLocal, global6, def6 + reject6, false, true},
		{"只有 ULA（如 Docker 网桥）不算 IPv6", route4, ula6, def6, true, false},
		{"有全局地址但没有默认路由", route4, global6, reject6, true, false},
		{"内核关闭了 IPv6：文件不存在", route4, "", "", true, false},
	} {
		got := parseIPStack(c.route, c.inet6, c.route6)
		if got.IPv4 != c.v4 || got.IPv6 != c.v6 {
			t.Errorf("%s: ipv4=%v ipv6=%v, want %v %v", c.name, got.IPv4, got.IPv6, c.v4, c.v6)
		}
	}
}
