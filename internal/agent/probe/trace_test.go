package probe

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"vpsmon/internal/netprobe"
	"vpsmon/internal/protocol"
)

func TestTraceOnce(t *testing.T) {
	p := New(nil)
	var traced []netip.Addr
	p.trace = func(_ context.Context, a netip.Addr) ([]netprobe.Hop, bool, error) {
		traced = append(traced, a)
		if a.Is6() {
			return nil, false, netprobe.ErrICMPUnavailable
		}
		return []netprobe.Hop{{TTL: 1, Addr: netip.MustParseAddr("10.0.0.1"), RTT: 1234 * time.Microsecond}, {TTL: 2},
			{TTL: 3, Addr: a, RTT: 80 * time.Millisecond}}, true, nil
	}
	p.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.168.1.1")}, nil // 【安全】解析到内网：不追踪
	}
	enc := protocol.EncodeProbeTargets([]protocol.ProbeTarget{
		{ID: 1, Proto: "icmp", Addr: netip.MustParseAddr("220.181.173.35")},
		{ID: 2, Proto: "tcp", Host: "speed.test.cn", Port: 80},
		{ID: 3, Proto: "icmp", Addr: netip.MustParseAddr("2400:da00::6666")},
		{ID: 4, Proto: "icmp", Addr: netip.MustParseAddr("1.1.1.1")},
	})
	// 超过 3 个时只取前 3 个
	p.Update("v1", enc, "300", "", "1,2,3,4")
	p.TraceOnce(context.Background())
	got := p.DrainTraces()
	if len(traced) != 2 || traced[0] != netip.MustParseAddr("220.181.173.35") {
		t.Fatalf("应只追踪目标 1 与 3：%v", traced)
	}
	if len(got) != 3 || got[0].TargetID != 1 || !got[0].Reached || len(got[0].Hops) != 3 || *got[0].Hops[0].RTT != 1.2 ||
		got[0].Hops[1].IP != "" || got[0].Hops[2].IP != "220.181.173.35" {
		t.Fatalf("目标 1：%+v", got)
	}
	if got[1].TargetID != 2 || got[1].Error != "blocked" || got[2].TargetID != 3 || got[2].Error != "icmp_unavailable" {
		t.Fatalf("域名解析到内网应为 blocked，不允许 ICMP 应为 icmp_unavailable：%+v", got[1:])
	}
	if p.DrainTraces() != nil {
		t.Error("取出后应清空")
	}
	// 不在目标列表中的编号忽略
	p.Update("v1b", enc, "300", "", "9")
	traced = nil
	p.TraceOnce(context.Background())
	if len(traced) != 0 || p.DrainTraces() != nil {
		t.Error("不在目标列表中的编号不应追踪")
	}
	// 面板不再下发追踪目标：不追踪
	p.Update("v2", enc, "300", "", "")
	traced = nil
	p.TraceOnce(context.Background())
	if len(traced) != 0 || p.DrainTraces() != nil {
		t.Error("没有追踪目标时不应追踪")
	}
	// 本机关闭探测：不追踪
	p.Update("v3", enc, "300", "", "1")
	p.Disabled = func() bool { return true }
	p.TraceOnce(context.Background())
	if len(traced) != 0 {
		t.Error("no-probe 时不应追踪")
	}
}

func TestFakeTrace(t *testing.T) {
	p := New(nil)
	p.UseFakeTrace()
	hops, reached, err := p.trace(context.Background(), netip.MustParseAddr("220.181.173.35"))
	if err != nil || !reached || len(hops) < 5 {
		t.Fatalf("%v %v %+v", err, reached, hops)
	}
}
