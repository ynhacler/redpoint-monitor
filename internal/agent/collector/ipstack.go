package collector

// IP 协议栈（修订第 279 条，设计 4.3）：仪表板的 v4 / v6 标志。只读 /proc，不需要 root，也不访问外部地址。
//
//	ipv4  /proc/net/route 中有默认路由（目的与掩码都为 0）。NAT 机器（只有内网 IPv4、共享公网地址）也算有 IPv4
//	ipv6  /proc/net/if_inet6 中有全局地址（scope 0，不含 fc00::/7 的 ULA 与回环网卡），且 /proc/net/ipv6_route 中有
//	      不是拒绝路由的 ::/0 默认路由
//
//	public_ipv4  /proc/net/fib_trie 中本机（/32 host LOCAL）的第一个公网地址（修订第 286 条）
//	public_ipv6  /proc/net/if_inet6 中第一个全局、非临时的公网地址
//
// 解析函数是纯函数，测试中直接传入文件内容。

import (
	"net/netip"
	"strconv"
	"strings"

	"vpsmon/internal/protocol"
)

const rtfReject = 0x0200 // RTF_REJECT：内核为没有 IPv6 的机器在 lo 上加的拒绝路由

// parseIPStack 根据 /proc/net/route、/proc/net/if_inet6 与 /proc/net/ipv6_route 的内容判断协议栈。
func parseIPStack(route, ifInet6, ipv6Route, fibTrie string) *protocol.IPStack {
	st := &protocol.IPStack{IPv4: hasV4Default(route), IPv6: hasV6Global(ifInet6) && hasV6Default(ipv6Route)}
	if st.IPv4 {
		st.PublicIPv4 = publicV4(fibTrie)
	}
	if st.IPv6 {
		st.PublicIPv6 = publicV6(ifInet6)
	}
	return st
}

// publicV4 从 /proc/net/fib_trie 中找本机地址：“|-- a.b.c.d” 后面紧跟 “/32 host LOCAL” 的一行；返回第一个公网地址。
func publicV4(s string) string {
	lines := strings.Split(s, "\n")
	for i := 0; i+1 < len(lines); i++ {
		f := strings.Fields(lines[i])
		if len(f) != 2 || f[0] != "|--" || !strings.Contains(lines[i+1], "/32 host LOCAL") {
			continue
		}
		if a, err := netip.ParseAddr(f[1]); err == nil && a.Is4() && protocol.PublicProbeAddr(a) {
			return a.String()
		}
	}
	return ""
}

// publicV6 从 /proc/net/if_inet6 中取第一个全局公网地址，跳过临时（隐私扩展）地址与 lo。
func publicV6(s string) string {
	const ifaTemporary = 0x01
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || len(f[0]) != 32 || f[3] != "00" || f[5] == "lo" {
			continue
		}
		if flags, err := strconv.ParseUint(f[4], 16, 32); err != nil || flags&ifaTemporary != 0 {
			continue
		}
		var b [16]byte
		ok := true
		for i := range b {
			v, err := strconv.ParseUint(f[0][2*i:2*i+2], 16, 8)
			if err != nil {
				ok = false
				break
			}
			b[i] = byte(v)
		}
		if a := netip.AddrFrom16(b); ok && protocol.PublicProbeAddr(a) {
			return a.String()
		}
	}
	return ""
}

// hasV4Default 判断 /proc/net/route 中是否有默认路由。
func hasV4Default(s string) bool {
	for i, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 8 {
			continue // 第一行是表头
		}
		if f[1] == "00000000" && f[7] == "00000000" {
			return true
		}
	}
	return false
}

// hasV6Global 判断 /proc/net/if_inet6 中是否有全局单播地址（排除 ULA 与 lo）。
func hasV6Global(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || len(f[0]) != 32 || f[3] != "00" || f[5] == "lo" {
			continue
		}
		if b, err := strconv.ParseUint(f[0][:2], 16, 8); err != nil || b&0xfe == 0xfc {
			continue // fc00::/7
		}
		return true
	}
	return false
}

// hasV6Default 判断 /proc/net/ipv6_route 中是否有可用的 ::/0 默认路由。
func hasV6Default(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[1] != "00" || strings.Trim(f[0], "0") != "" || f[9] == "lo" {
			continue
		}
		if flags, err := strconv.ParseUint(f[8], 16, 32); err != nil || flags&rtfReject != 0 {
			continue
		}
		return true
	}
	return false
}
