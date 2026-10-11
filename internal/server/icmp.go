package server

// 服务监控的 ICMP 检查（设计 33.2.1）。只用标准库：
// 优先使用非特权的 ICMP 数据报套接字（Linux 需 net.ipv4.ping_group_range 包含面板进程的组，
// Ubuntu / Debian / Fedora 默认允许所有组；macOS 默认允许），不可用时再尝试原始套接字（需要 CAP_NET_RAW）。
// 面板不会为此申请特权：两者都不可用时检查失败并给出说明。

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
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
	icmpAttempts     = 3 // 一次检查最多发 3 个请求，收到任一回复即为正常
)

// errICMPUnavailable 表示本机不允许面板发送 ICMP。
var errICMPUnavailable = errors.New("本机不允许面板发送 ICMP：请把 sysctl net.ipv4.ping_group_range 设为包含面板用户的组（如 \"0 2147483647\"）")

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

// resolvePingTarget 解析目标：IP 直接使用；域名优先 IPv4。
func resolvePingTarget(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip, nil
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("域名没有 A / AAAA 记录")
	}
	return ips[0], nil
}

// checkICMP 发送回显请求，返回第一个回复的往返时间。
func checkICMP(ctx context.Context, host string) (time.Duration, error) {
	ip, err := resolvePingTarget(ctx, host)
	if err != nil {
		return 0, err
	}
	v6 := ip.To4() == nil
	conn, dst, err := listenICMP(v6, ip)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	id := uint16(os.Getpid())
	buf := make([]byte, 1500)
	for attempt := 0; attempt < icmpAttempts; attempt++ {
		if ctx.Err() != nil {
			break
		}
		seq := uint16(icmpSeq.Add(1))
		start := time.Now()
		if _, err := conn.WriteTo(icmpPacket(v6, id, seq, []byte("vpsmon")), dst); err != nil {
			return 0, err
		}
		wait := time.Now().Add(time.Second)
		if dl, ok := ctx.Deadline(); ok && dl.Before(wait) {
			wait = dl
		}
		_ = conn.SetReadDeadline(wait)
		for {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break // 这次没有回复，再发下一个
				}
				return 0, err
			}
			if parseICMPReply(v6, buf[:n], seq) {
				return time.Since(start), nil
			}
		}
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return 0, fmt.Errorf("%d 个请求均无回复", icmpAttempts)
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
		return nil, nil, errICMPUnavailable
	}
	return c, &net.IPAddr{IP: ip}, nil
}
