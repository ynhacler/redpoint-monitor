//go:build linux

package netprobe

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"
)

// TestTraceLoopback 实际追踪到本机：第一跳就收到目标的回复；本机不允许非特权 ICMP 时跳过。
func TestTraceLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dst := netip.MustParseAddr("127.0.0.1")
	hops, reached, err := Trace(ctx, dst, 5, time.Second)
	if errors.Is(err, ErrICMPUnavailable) {
		t.Skip("本机不允许非特权 ICMP")
	}
	if err != nil || !reached || len(hops) != 1 || hops[0].Addr != dst || hops[0].RTT <= 0 {
		t.Fatalf("追踪本机：%v %v %+v", err, reached, hops)
	}
}
