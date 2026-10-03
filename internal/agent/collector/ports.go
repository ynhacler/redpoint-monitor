package collector

import (
	"bufio"
	"encoding/hex"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"

	"vpsmon/internal/protocol"
)

// 监听端口（设计 4.9.1）：解析 /proc/net/tcp、tcp6、udp、udp6，无需 root。
// 只取本机监听的端口；不采集连接明细、对端地址与进程信息（读取其他用户进程的 fd 需要 root，也涉及隐私）。

// maxPorts：最多上报的端口数（按协议、端口合并后），防止异常主机撑大上报体。
const maxPorts = 64

// maxUDPLines：UDP 表没有“监听”状态、需要整表扫描，限制读取行数，避免套接字极多的主机拖慢采集。
const maxUDPLines = 20000

type listenSocket struct {
	proto string
	addr  string
	port  int
}

// parseListenSockets 解析 /proc/net/{tcp,tcp6,udp,udp6} 的内容。
//
// TCP 只取状态 0A（LISTEN）。内核先输出监听表、再输出已建立连接，因此遇到第一个非监听行即停止，
// 连接再多也只读开头一小段。UDP 取未连接的套接字（状态 07 且对端为 0），即在端口上接收数据的服务。
func parseListenSockets(r io.Reader, proto string) []listenSocket {
	udp := strings.HasPrefix(proto, "udp")
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	var out []listenSocket
	lines := 0
	for sc.Scan() {
		lines++
		if lines == 1 {
			continue // 表头
		}
		if udp && lines > maxUDPLines {
			break
		}
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		local, remote, state := f[1], f[2], f[3]
		if udp {
			if state != "07" || !zeroEndpoint(remote) {
				continue
			}
		} else if state != "0A" {
			break
		}
		addr, port, ok := decodeEndpoint(local)
		if !ok || port == 0 {
			continue
		}
		out = append(out, listenSocket{proto: strings.TrimSuffix(proto, "6"), addr: addr, port: port})
	}
	return out
}

// decodeEndpoint 解码 “0100007F:0016” 形式的地址与端口。地址按 32 位字以主机字节序存放；
// 支持的架构（amd64 / arm64 / arm / 386 / riscv64）均为小端。
func decodeEndpoint(s string) (string, int, bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", 0, false
	}
	b, err := hex.DecodeString(s[:i])
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return "", 0, false
	}
	for w := 0; w < len(b); w += 4 {
		b[w], b[w+1], b[w+2], b[w+3] = b[w+3], b[w+2], b[w+1], b[w]
	}
	port, err := strconv.ParseUint(s[i+1:], 16, 16)
	if err != nil {
		return "", 0, false
	}
	ip := net.IP(b)
	if v4 := ip.To4(); v4 != nil && len(b) == 16 && !ip.IsUnspecified() {
		ip = v4 // IPv4 映射地址（::ffff:a.b.c.d）显示为 IPv4
	}
	return ip.String(), int(port), true
}

func zeroEndpoint(s string) bool {
	return strings.Trim(s, "0:") == ""
}

// mergeListenPorts 按协议与端口合并监听地址，排序后最多保留 maxPorts 项。
func mergeListenPorts(socks []listenSocket) []protocol.ListenPort {
	type key struct {
		proto string
		port  int
	}
	addrs := map[key]map[string]bool{}
	for _, s := range socks {
		k := key{s.proto, s.port}
		if addrs[k] == nil {
			addrs[k] = map[string]bool{}
		}
		addrs[k][s.addr] = true
	}
	out := make([]protocol.ListenPort, 0, len(addrs))
	for k, set := range addrs {
		p := protocol.ListenPort{Proto: k.proto, Port: k.port}
		for a := range set {
			p.Addrs = append(p.Addrs, a)
		}
		sort.Strings(p.Addrs)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Proto != out[j].Proto {
			return out[i].Proto < out[j].Proto // tcp 在前
		}
		return out[i].Port < out[j].Port
	})
	if len(out) > maxPorts {
		out = out[:maxPorts]
	}
	return out
}
