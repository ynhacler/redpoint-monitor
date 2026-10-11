package probe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"

	"vpsmon/internal/netprobe"
	"vpsmon/internal/protocol"
)

func TestProber(t *testing.T) {
	p := New(nil)
	p.gap = 0
	// tcp：5 个样本 10、30、20、失败、40 ms → 中位数 25，丢包 20%
	seq := []time.Duration{10, 30, 20, -1, 40}
	i := 0
	p.tcp = func(ctx context.Context, addr string) (time.Duration, error) {
		d := seq[i%len(seq)]
		i++
		if d < 0 {
			return 0, context.DeadlineExceeded
		}
		return d * time.Millisecond, nil
	}
	calls := 0
	p.echo = func(ctx context.Context, ip net.IP) (time.Duration, error) {
		calls++
		return 0, netprobe.ErrICMPUnavailable
	}
	if p.Version() != "" {
		t.Error("尚未收到目标时版本为空")
	}
	enc := protocol.EncodeProbeTargets([]protocol.ProbeTarget{
		{ID: 7, Proto: "tcp", Addr: netip.MustParseAddr("220.181.173.35"), Port: 80},
		{ID: 8, Proto: "icmp", Addr: netip.MustParseAddr("1.1.1.1")},
	})
	p.Update("v1", enc+";9,tcp,10.0.0.1,22", "10") // 【安全】内网目标被丢弃；间隔低于 60 秒按默认
	if p.Version() != "v1" || len(p.Targets()) != 2 || p.Interval() != 5*time.Minute {
		t.Fatalf("目标：%s %+v %s", p.Version(), p.Targets(), p.Interval())
	}
	p.RunOnce(context.Background())
	got := p.Drain()
	if len(got) != 2 || p.Drain() != nil {
		t.Fatalf("应有 2 个结果且取出后清空：%+v", got)
	}
	if r := got[0]; r.TargetID != 7 || r.Latency == nil || *r.Latency != 25 || r.Loss != 20 || r.Error != "" {
		t.Errorf("tcp 结果：%+v", r)
	}
	if r := got[1]; r.Latency != nil || r.Loss != 100 || r.Error != "icmp_unavailable" || calls != 1 {
		t.Errorf("本机不允许 ICMP 时应只试一次并说明原因：%+v calls=%d", r, calls)
	}

	// 本机关闭：版本为 off，不探测
	off := true
	p.Disabled = func() bool { return off }
	p.RunOnce(context.Background())
	if p.Version() != protocol.ProbeOff || p.Drain() != nil {
		t.Error("本机关闭时不应探测")
	}
	// 断网时最多保留 3 轮
	off = false
	for range 5 {
		p.RunOnce(context.Background())
	}
	if n := len(p.Drain()); n != 3*2 {
		t.Errorf("最多保留 3 轮：%d", n)
	}
	// 清空目标
	p.Update("v2", enc, "600")
	if p.Interval() != 10*time.Minute {
		t.Errorf("应采用面板下发的间隔：%s", p.Interval())
	}
	p.Update("0", "", "")
	if len(p.Targets()) != 0 {
		t.Error("版本 0 表示没有目标")
	}
}

func TestErrorCode(t *testing.T) {
	for err, want := range map[error]string{
		syscall.ECONNREFUSED:        "refused",
		context.DeadlineExceeded:    "timeout",
		netprobe.ErrNoReply:         "timeout",
		syscall.EHOSTUNREACH:        "unreachable",
		errors.New("x"):             "error",
		netprobe.ErrICMPUnavailable: "icmp_unavailable",
	} {
		if got := errorCode(&net.OpError{Op: "dial", Err: err}); got != want {
			t.Errorf("%v → %s，应为 %s", err, got, want)
		}
	}
}
