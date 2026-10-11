// Package netprobe 是面板（服务监控，设计 33.2.1）与 Agent（三网延迟，设计 42.3.2）共用的网络探测：
// ICMP 回显与 TCP 连接耗时。只用标准库；ICMP 优先用非特权数据报套接字，不可用时尝试原始套接字，
// 两者都不可用时返回 ErrICMPUnavailable，调用方不会因此申请特权（约束 8）。
package netprobe

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"sync/atomic"
	"time"
)

const (
	icmpEchoRequest4 = 8
	icmpEchoReply4   = 0
	icmpEchoRequest6 = 128
	icmpEchoReply6   = 129
)

// ErrICMPUnavailable 表示本机不允许当前用户发送 ICMP。
var ErrICMPUnavailable = errors.New("本机不允许发送 ICMP：请把 sysctl net.ipv4.ping_group_range 设为包含该进程用户的组（如 \"0 2147483647\"）")

// ErrNoReply 表示在截止时间前没有收到回复。
var ErrNoReply = errors.New("没有回复")

var icmpSeq atomic.Uint32

// icmpPacket 构造回显请求；IPv4 计算校验和，IPv6 由内核计算。
func icmpPacket(v6 bool, id, seq uint16, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	b[0] = icmpEchoRequest4
	if v6 {
		b[0] = icmpEchoRequest6
	}
	binary.BigEndian.PutUint16(b[4:], id)
	binary.BigEndian.PutUint16(b[6:], seq)
	copy(b[8:], payload)
	if !v6 {
		binary.BigEndian.PutUint16(b[2:], icmpChecksum(b))
	}
	return b
}

func icmpChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// parseICMPReply 判断收到的报文是否是 seq 对应的回显回复。
// macOS 的 IPv4 数据报套接字与原始套接字会带上 IP 头，先去掉；数据报套接字的 id 由内核改写，不比较。
func parseICMPReply(v6 bool, b []byte, seq uint16) bool {
	if !v6 && len(b) >= 20 && b[0]>>4 == 4 {
		ihl := int(b[0]&0x0f) * 4
		if ihl < 20 || len(b) < ihl {
			return false
		}
		b = b[ihl:]
	}
	if len(b) < 8 {
		return false
	}
	want := byte(icmpEchoReply4)
	if v6 {
		want = icmpEchoReply6
	}
	return b[0] == want && b[1] == 0 && binary.BigEndian.Uint16(b[6:]) == seq
}

// Echo 向 ip 发送一个回显请求，返回往返时间；ctx 的截止时间即等待上限（没有截止时间时最多等 5 秒）。
func Echo(ctx context.Context, ip net.IP) (time.Duration, error) {
	v6 := ip.To4() == nil
	conn, dst, err := listenICMP(v6, ip)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	dl, ok := ctx.Deadline()
	if !ok {
		dl = time.Now().Add(5 * time.Second)
	}
	_ = conn.SetDeadline(dl)
	seq := uint16(icmpSeq.Add(1))
	start := time.Now()
	if _, err := conn.WriteTo(icmpPacket(v6, uint16(os.Getpid()), seq, []byte("vpsmon")), dst); err != nil {
		return 0, err
	}
	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if ctx.Err() != nil {
					return 0, ctx.Err()
				}
				return 0, ErrNoReply
			}
			return 0, err
		}
		if parseICMPReply(v6, buf[:n], seq) {
			return time.Since(start), nil
		}
	}
}

// TCPConnect 返回建立 TCP 连接的耗时（约等于一个往返）；连接建立后立即关闭，不发送任何数据。
func TCPConnect(ctx context.Context, addr string) (time.Duration, error) {
	start := time.Now()
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return 0, err
	}
	rtt := time.Since(start)
	c.Close()
	return rtt, nil
}

// listenICMP 先试非特权数据报套接字，再试原始套接字。
func listenICMP(v6 bool, ip net.IP) (net.PacketConn, net.Addr, error) {
	if c, err := listenICMPDgram(v6); err == nil {
		return c, &net.UDPAddr{IP: ip}, nil
	}
	network := "ip4:icmp"
	if v6 {
		network = "ip6:ipv6-icmp"
	}
	c, err := net.ListenPacket(network, "")
	if err != nil {
		return nil, nil, ErrICMPUnavailable
	}
	return c, &net.IPAddr{IP: ip}, nil
}
