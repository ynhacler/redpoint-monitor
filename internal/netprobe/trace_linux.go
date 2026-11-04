//go:build linux

package netprobe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"syscall"
	"time"
)

// icmpTracer 用一个非特权 ICMP 数据报套接字逐跳追踪：每跳设置 TTL 后发一个回显请求，
// 中间路由器的“TTL 超时”通过 IP_RECVERR 进入套接字的错误队列，目标的回复走正常接收（设计 42.5）。
type icmpTracer struct {
	conn net.PacketConn
	rc   syscall.RawConn
	dst  net.Addr
	ip   netip.Addr
	v6   bool
	buf  []byte
	oob  []byte
}

func openTracer(dst netip.Addr) (tracer, error) {
	v6 := dst.Is6()
	conn, err := listenICMPDgram(v6)
	if err != nil {
		return nil, ErrICMPUnavailable // 【安全】不退回原始套接字，也不申请特权（约束 8）
	}
	sc, ok := conn.(syscall.Conn)
	if !ok {
		conn.Close()
		return nil, ErrICMPUnavailable
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		conn.Close()
		return nil, err
	}
	level, opt := syscall.IPPROTO_IP, syscall.IP_RECVERR
	if v6 {
		level, opt = syscall.IPPROTO_IPV6, syscall.IPV6_RECVERR
	}
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), level, opt, 1) }); err != nil || serr != nil {
		conn.Close()
		return nil, ErrICMPUnavailable
	}
	return &icmpTracer{conn: conn, rc: rc, dst: &net.UDPAddr{IP: dst.AsSlice()}, ip: dst, v6: v6,
		buf: make([]byte, 128), oob: make([]byte, 128)}, nil
}

func (t *icmpTracer) Close() error { return t.conn.Close() }

// Hop 发送 TTL 为 ttl 的回显请求，等到 ctx 的截止时间。
func (t *icmpTracer) Hop(ctx context.Context, ttl int) (netip.Addr, time.Duration, hopKind, error) {
	level, opt := syscall.IPPROTO_IP, syscall.IP_TTL
	if t.v6 {
		level, opt = syscall.IPPROTO_IPV6, syscall.IPV6_UNICAST_HOPS
	}
	var serr error
	if err := t.rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), level, opt, ttl) }); err != nil {
		return netip.Addr{}, 0, hopNone, err
	} else if serr != nil {
		return netip.Addr{}, 0, hopNone, serr
	}
	t.drain() // 上一跳迟到的错误不算到这一跳
	seq := uint16(icmpSeq.Add(1))
	start := time.Now()
	pkt := icmpPacket(t.v6, uint16(os.Getpid()), seq, []byte("vpsmon"))
	if _, err := t.conn.WriteTo(pkt, t.dst); err != nil {
		// 挂起的 ICMP 错误可能让这次发送失败：清掉后重发一次
		t.drain()
		if _, err := t.conn.WriteTo(pkt, t.dst); err != nil {
			return netip.Addr{}, 0, hopNone, err
		}
	}
	dl, ok := ctx.Deadline()
	if !ok {
		dl = start.Add(2 * time.Second)
	}
	_ = t.conn.SetReadDeadline(dl)
	var (
		addr netip.Addr
		kind hopKind
	)
	err := t.rc.Read(func(fd uintptr) bool {
		addr, kind = t.recv(int(fd), seq)
		return kind != hopNone
	})
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) || ctx.Err() != nil {
			return netip.Addr{}, 0, hopNone, nil
		}
		return netip.Addr{}, 0, hopNone, err
	}
	return addr, time.Since(start), kind, nil
}

// recv 不阻塞地读错误队列与正常回复；都没有可读的内容时返回 hopNone，由调用方等待下一次可读。
func (t *icmpTracer) recv(fd int, seq uint16) (netip.Addr, hopKind) {
	for range 32 {
		n, oobn, _, _, err := syscall.Recvmsg(fd, t.buf, t.oob, syscall.MSG_ERRQUEUE|syscall.MSG_DONTWAIT)
		if err == nil {
			if a, kind, ok := t.parseOOB(t.oob[:oobn]); ok && errSeq(t.v6, t.buf[:n], seq) {
				return a, kind
			}
			continue // 其他跳迟到的错误
		}
		n, _, err = syscall.Recvfrom(fd, t.buf, syscall.MSG_DONTWAIT)
		if err == nil {
			if parseICMPReply(t.v6, t.buf[:n], seq) {
				return t.ip, hopReply
			}
			continue
		}
		if errors.Is(err, syscall.EAGAIN) {
			return netip.Addr{}, hopNone
		}
		// 其他错误是 IP_RECVERR 挂起的 ICMP 错误（如 EHOSTUNREACH）：对应的消息在错误队列中，再读一次
	}
	return netip.Addr{}, hopNone
}

// drain 丢弃错误队列中已有的消息。
func (t *icmpTracer) drain() {
	_ = t.rc.Control(func(fd uintptr) {
		for range 32 {
			if _, _, _, _, err := syscall.Recvmsg(int(fd), t.buf, t.oob, syscall.MSG_ERRQUEUE|syscall.MSG_DONTWAIT); err != nil {
				return
			}
		}
	})
}

func (t *icmpTracer) parseOOB(oob []byte) (netip.Addr, hopKind, bool) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return netip.Addr{}, hopNone, false
	}
	for _, m := range msgs {
		if !t.v6 && m.Header.Level == syscall.IPPROTO_IP && m.Header.Type == syscall.IP_RECVERR ||
			t.v6 && m.Header.Level == syscall.IPPROTO_IPV6 && m.Header.Type == syscall.IPV6_RECVERR {
			return parseExtErr(t.v6, m.Data)
		}
	}
	return netip.Addr{}, hopNone, false
}
