package collector

import (
	"reflect"
	"strings"
	"testing"

	"vpsmon/internal/protocol"
)

const procTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1001 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0CEA 00000000:0000 0A 00000000:00000000 00:00000000 00000000   112        0 1002 1 0000000000000000 100 0 0 10 0
   2: 0A00000F:0016 0B00000F:D431 01 00000000:00000000 02:000A7E5B 00000000     0        0 1003 4 0000000000000000 20 4 30 10 -1
   3: 00000000:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1004 1 0000000000000000 100 0 0 10 0
`

const procTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2001 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000001000000:0277 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2002 1 0000000000000000 100 0 0 10 0
`

const procUDP = `   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  100: 3500007F:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000   101        0 3001 2 0000000000000000 0
  200: 0A00000F:9C40 08080808:0035 01 00000000:00000000 00:00000000 00000000     0        0 3002 2 0000000000000000 0
  300: 00000000:0044 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 3003 2 0000000000000000 0
`

// littleEndianFixture 让测试数据（按小端内核的写法）在任何机器上都按小端解码，
// CI 在大端 MIPS 的 qemu 模拟下运行同一组测试时结果不变。
func littleEndianFixture(t *testing.T) {
	old := hostLittleEndian
	hostLittleEndian = true
	t.Cleanup(func() { hostLittleEndian = old })
}

func TestListenPorts(t *testing.T) {
	littleEndianFixture(t)
	var socks []listenSocket
	socks = append(socks, parseListenSockets(strings.NewReader(procTCP), "tcp")...)
	socks = append(socks, parseListenSockets(strings.NewReader(procTCP6), "tcp6")...)
	socks = append(socks, parseListenSockets(strings.NewReader(procUDP), "udp")...)
	got := mergeListenPorts(socks)
	want := []protocol.ListenPort{
		{Proto: "tcp", Port: 22, Addrs: []string{"0.0.0.0", "::"}},
		{Proto: "tcp", Port: 631, Addrs: []string{"::1"}},
		{Proto: "tcp", Port: 3306, Addrs: []string{"127.0.0.1"}},
		{Proto: "udp", Port: 53, Addrs: []string{"127.0.0.53"}},
		{Proto: "udp", Port: 68, Addrs: []string{"0.0.0.0"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("监听端口：\n%+v\n应为：\n%+v", got, want)
	}
	// 已建立连接之后的监听行（443）不会被读到：内核先输出全部监听套接字，遇到第一个连接即停止
	for _, p := range got {
		if p.Port == 443 {
			t.Error("遇到第一条已建立连接后应停止读取")
		}
	}
}

func TestListenPortsCap(t *testing.T) {
	littleEndianFixture(t)
	var socks []listenSocket
	for i := 1; i <= 100; i++ {
		socks = append(socks, listenSocket{proto: "tcp", addr: "0.0.0.0", port: i})
	}
	if n := len(mergeListenPorts(socks)); n != maxPorts {
		t.Errorf("最多上报 %d 个端口，实际 %d", maxPorts, n)
	}
}

// 大端（mips）内核按网络字节序写出地址，不能翻转；小端翻转每个 32 位字。
func TestDecodeEndpointByteOrder(t *testing.T) {
	cases := []struct {
		in     string
		little bool
		want   string
	}{
		{"0100007F:0016", true, "127.0.0.1"},
		{"7F000001:0016", false, "127.0.0.1"}, // mips：同一地址在大端内核上的写法
		{"00000000000000000000000001000000:01BB", true, "::1"},
		{"00000000000000000000000000000001:01BB", false, "::1"},
	}
	for _, c := range cases {
		addr, port, ok := decodeEndpointOrder(c.in, c.little)
		if !ok || addr != c.want || (port != 22 && port != 443) {
			t.Errorf("decode(%q, little=%v) = %s:%d，应为 %s", c.in, c.little, addr, port, c.want)
		}
	}
}
