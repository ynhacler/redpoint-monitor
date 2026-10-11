package netprobe

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestICMPPacket(t *testing.T) {
	b := icmpPacket(false, 0x1234, 7, []byte("vpsmon"))
	if b[0] != icmpEchoRequest4 || icmpChecksum(b) != 0 {
		t.Fatalf("IPv4 回显请求的校验和应使整包校验为 0：% x", b)
	}
	// 回复：类型 0，同一 seq；带 20 字节 IP 头（macOS）与不带（Linux）都应识别
	reply := append([]byte(nil), b...)
	reply[0] = icmpEchoReply4
	withIP := append(append([]byte{0x45}, make([]byte, 19)...), reply...)
	for _, r := range [][]byte{reply, withIP} {
		if !parseICMPReply(false, r, 7) {
			t.Errorf("应识别为回复：% x", r)
		}
	}
	if parseICMPReply(false, reply, 8) || parseICMPReply(false, b, 7) || parseICMPReply(false, reply[:6], 7) {
		t.Error("seq 不同、请求本身或过短的报文不应识别为回复")
	}
	v6 := icmpPacket(true, 1, 9, nil)
	v6[0] = icmpEchoReply6
	if !parseICMPReply(true, v6, 9) {
		t.Error("应识别 IPv6 回复")
	}
}

// TestEchoAndTCP 实际 ping 本机并连接本机端口；本机不允许非特权 ICMP 时跳过 ICMP 部分。
func TestEchoAndTCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if rtt, err := Echo(ctx, net.ParseIP("127.0.0.1")); err != nil {
		if !strings.Contains(err.Error(), "ping_group_range") {
			t.Errorf("ping 127.0.0.1：%v", err)
		}
	} else if rtt <= 0 || rtt > time.Second {
		t.Errorf("往返时间不合理：%s", rtt)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := TCPConnect(ctx, ln.Addr().String()); err != nil {
		t.Errorf("连接本机端口：%v", err)
	}
}
