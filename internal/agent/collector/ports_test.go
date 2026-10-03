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

func TestListenPorts(t *testing.T) {
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
	var socks []listenSocket
	for i := 1; i <= 100; i++ {
		socks = append(socks, listenSocket{proto: "tcp", addr: "0.0.0.0", port: i})
	}
	if n := len(mergeListenPorts(socks)); n != maxPorts {
		t.Errorf("最多上报 %d 个端口，实际 %d", maxPorts, n)
	}
}
