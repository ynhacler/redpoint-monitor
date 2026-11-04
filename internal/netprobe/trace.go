package netprobe

// 路由追踪（设计 42.5，修订第 282 条）：逐跳递增 TTL 发送 ICMP 回显，记录每一跳返回“超时”的路由器地址与耗时，
// 直到收到目标本身的回复。只用非特权 ICMP 数据报套接字（Linux 的 ping socket + IP_RECVERR，与 tracepath 相同的做法），
// 不需要 root 或 CAP_NET_RAW；其他系统与不允许 ICMP 的主机返回 ErrICMPUnavailable（约束 8）。

import (
	"context"
	"encoding/binary"
	"net/netip"
	"time"
)

// Hop 是一跳：没有回复时 Addr 无效、RTT 为 0。
type Hop struct {
	TTL  int
	Addr netip.Addr
	RTT  time.Duration
}

// hopKind 是一跳收到的回复类型。
type hopKind int

const (
	hopNone     hopKind = iota // 等待超时，没有回复
	hopExceeded                // 中间路由器：TTL 超时
	hopUnreach                 // 不可达：不再继续
	hopReply                   // 目标本身的回显回复
)

// traceGiveUp 是连续几跳没有回复时放弃：目标或其前几跳不回 ICMP 时，不必把 30 跳都等完。
const traceGiveUp = 5

// tracer 发送一跳并等待回复，便于测试替换真实套接字。
type tracer interface {
	Hop(ctx context.Context, ttl int) (netip.Addr, time.Duration, hopKind, error)
	Close() error
}

// Trace 追踪到 dst 的路由，最多 maxHops 跳，每跳最多等待 wait；返回逐跳结果与是否到达目标。
// 本机不允许 ICMP 时返回 ErrICMPUnavailable。
func Trace(ctx context.Context, dst netip.Addr, maxHops int, wait time.Duration) ([]Hop, bool, error) {
	t, err := openTracer(dst.Unmap())
	if err != nil {
		return nil, false, err
	}
	defer t.Close()
	return traceLoop(ctx, t, maxHops, wait)
}

// traceLoop 逐跳追踪：到达目标或不可达时结束；连续 traceGiveUp 跳没有回复时放弃，末尾没有回复的跳不返回。
func traceLoop(ctx context.Context, t tracer, maxHops int, wait time.Duration) ([]Hop, bool, error) {
	var hops []Hop
	silent := 0
	for ttl := 1; ttl <= maxHops && ctx.Err() == nil; ttl++ {
		hctx, cancel := context.WithTimeout(ctx, wait)
		a, rtt, kind, err := t.Hop(hctx, ttl)
		cancel()
		if err != nil {
			if len(hops) == 0 {
				return nil, false, err // 第一跳就发不出去（如没有默认路由）
			}
			break
		}
		h := Hop{TTL: ttl}
		if kind == hopNone {
			silent++
		} else {
			h.Addr, h.RTT, silent = a, rtt, 0
		}
		hops = append(hops, h)
		if kind == hopReply {
			return hops, true, nil
		}
		if kind == hopUnreach || silent >= traceGiveUp {
			break
		}
	}
	for len(hops) > 0 && !hops[len(hops)-1].Addr.IsValid() {
		hops = hops[:len(hops)-1]
	}
	return hops, false, nil
}

// parseExtErr 解析 IP_RECVERR / IPV6_RECVERR 控制消息的内容（Linux 的 struct sock_extended_err 加发出错误的地址）：
// 前 16 字节为 errno、origin、type、code、pad、info、data，随后是 sockaddr_in / sockaddr_in6。
func parseExtErr(v6 bool, b []byte) (netip.Addr, hopKind, bool) {
	const originICMP, originICMP6 = 2, 3
	if len(b) < 16 {
		return netip.Addr{}, hopNone, false
	}
	origin, typ := b[4], b[5]
	var a netip.Addr
	kind := hopNone
	if v6 {
		if origin != originICMP6 || len(b) < 16+24 {
			return netip.Addr{}, hopNone, false
		}
		a = netip.AddrFrom16([16]byte(b[24:40]))
		switch typ {
		case 3: // Time Exceeded
			kind = hopExceeded
		case 1: // Destination Unreachable
			kind = hopUnreach
		}
	} else {
		if origin != originICMP || len(b) < 16+8 {
			return netip.Addr{}, hopNone, false
		}
		a = netip.AddrFrom4([4]byte(b[20:24]))
		switch typ {
		case 11:
			kind = hopExceeded
		case 3:
			kind = hopUnreach
		}
	}
	if kind == hopNone {
		return netip.Addr{}, hopNone, false
	}
	return a, kind, true
}

// errSeq 判断错误队列中带回的原始报文是否是序号 seq 的回显请求；只带回不足 8 字节时无法判断，按是处理。
func errSeq(v6 bool, b []byte, seq uint16) bool {
	if len(b) < 8 {
		return true
	}
	want := byte(icmpEchoRequest4)
	if v6 {
		want = icmpEchoRequest6
	}
	return b[0] == want && binary.BigEndian.Uint16(b[6:]) == seq
}
