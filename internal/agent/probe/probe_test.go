package probe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"testing"
	"time"

	"vpsmon/internal/netprobe"
	"vpsmon/internal/protocol"
)

func TestProber(t *testing.T) {
	p := New(nil)
	p.sleep = func(context.Context, time.Duration) {}
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
	p.ping = func(ip net.IP) (pinger, error) {
		calls++
		return nil, netprobe.ErrICMPUnavailable
	}
	if p.Version() != "" {
		t.Error("尚未收到目标时版本为空")
	}
	enc := protocol.EncodeProbeTargets([]protocol.ProbeTarget{
		{ID: 7, Proto: "tcp", Addr: netip.MustParseAddr("220.181.173.35"), Port: 80},
		{ID: 8, Proto: "icmp", Addr: netip.MustParseAddr("1.1.1.1")},
	})
	p.Update("v1", enc+";9,tcp,10.0.0.1,22", "10", "") // 【安全】内网目标被丢弃；间隔低于 60 秒按默认
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
	if p.Timing() != protocol.DefaultTiming {
		t.Errorf("旧版面板不下发超时：默认 4 秒、间隔 1 秒：%+v", p.Timing())
	}
	p.Update("v2", enc, "600", "6,2")
	if p.Interval() != 10*time.Minute || p.Timing() != (protocol.ProbeTiming{Timeout: 6, Gap: 2}) {
		t.Errorf("应采用面板下发的间隔与超时：%s %+v", p.Interval(), p.Timing())
	}
	// 每个样本的等待上限即下发的超时
	var waited time.Duration
	p.tcp = func(ctx context.Context, addr string) (time.Duration, error) {
		dl, _ := ctx.Deadline()
		waited = time.Until(dl).Round(time.Second)
		return time.Millisecond, nil
	}
	p.RunOnce(context.Background())
	p.Drain()
	if waited != 6*time.Second {
		t.Errorf("样本超时应为 6 秒：%s", waited)
	}
	p.Update("0", "", "", "")
	if len(p.Targets()) != 0 {
		t.Error("版本 0 表示没有目标")
	}
}

// fakePinger 按顺序返回样本结果，并记录是否关闭。
type fakePinger struct {
	seq    []error
	n      int
	closed bool
}

func (f *fakePinger) Echo(ctx context.Context) (time.Duration, error) {
	err := f.seq[f.n%len(f.seq)]
	f.n++
	return 15 * time.Millisecond, err
}
func (f *fakePinger) Close() error { f.closed = true; return nil }

// 资源（修订第 129 条）：一个 ICMP 目标的样本共用一个套接字；确定的失败不再发剩下的样本；偶发超时照常测完。
func TestProberEfficiency(t *testing.T) {
	p := New(nil)
	p.sleep = func(context.Context, time.Duration) {}
	opened := 0
	fp := &fakePinger{seq: []error{nil, netprobe.ErrNoReply}}
	p.ping = func(ip net.IP) (pinger, error) { opened++; return fp, nil }
	tcpCalls := 0
	p.tcp = func(ctx context.Context, addr string) (time.Duration, error) {
		tcpCalls++
		return 0, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	}
	p.Update("v1", protocol.EncodeProbeTargets([]protocol.ProbeTarget{
		{ID: 1, Proto: "icmp", Addr: netip.MustParseAddr("1.1.1.1")},
		{ID: 2, Proto: "tcp", Addr: netip.MustParseAddr("8.8.8.8"), Port: 80},
	}), "300", "")
	p.RunOnce(context.Background())
	got := p.Drain()
	if opened != 1 || fp.n != Samples || !fp.closed {
		t.Errorf("ICMP 目标应只打开一次套接字、测满 %d 个样本并关闭：opened=%d n=%d closed=%v", Samples, opened, fp.n, fp.closed)
	}
	if r := got[0]; r.Latency == nil || r.Loss != 40 {
		t.Errorf("偶发超时照常计入丢包：%+v", r)
	}
	if r := got[1]; tcpCalls != 1 || r.Loss != 100 || r.Error != "refused" {
		t.Errorf("拒绝连接时只试一次：calls=%d %+v", tcpCalls, r)
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

// 域名目标（修订第 132 条）：每轮解析一次，用公网地址探测；解析失败记 dns；【安全】只解析到内网时不探测，记 blocked。
func TestProberHosts(t *testing.T) {
	p := New(nil)
	p.sleep = func(context.Context, time.Duration) {}
	var mu sync.Mutex // 4 个工作 goroutine 并发调用下面的替身
	var dialed []string
	p.tcp = func(ctx context.Context, addr string) (time.Duration, error) {
		mu.Lock()
		defer mu.Unlock()
		dialed = append(dialed, addr)
		return 20 * time.Millisecond, nil
	}
	lookups := 0
	p.lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
		mu.Lock()
		defer mu.Unlock()
		lookups++
		switch host {
		case "speed.test.cn":
			return []netip.Addr{netip.MustParseAddr("2400:da00::6666"), netip.MustParseAddr("220.181.173.35")}, nil
		case "evil.test-rebind.cn":
			return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.0.0.5")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	p.Update("v1", "1,tcp,speed.test.cn,443;2,tcp,evil.test-rebind.cn,80;3,tcp,gone.test-nx.cn,80", "300", "")
	if len(p.Targets()) != 3 {
		t.Fatalf("三个域名目标都应接收：%+v", p.Targets())
	}
	p.RunOnce(context.Background())
	got := p.Drain()
	byID := map[int64]protocol.ProbeResult{}
	for _, r := range got {
		byID[r.TargetID] = r
	}
	if r := byID[1]; r.Latency == nil || r.Loss != 0 {
		t.Errorf("正常域名应测出延迟：%+v", r)
	}
	if r := byID[2]; r.Error != "blocked" || r.Loss != 100 {
		t.Errorf("只解析到内网时应拒绝：%+v", r)
	}
	if r := byID[3]; r.Error != "dns" || r.Loss != 100 {
		t.Errorf("解析失败：%+v", r)
	}
	if lookups != 3 || len(dialed) != Samples {
		t.Errorf("每个域名每轮只解析一次，只连接公网地址：lookups=%d dialed=%v", lookups, dialed)
	}
	for _, d := range dialed {
		if d != "220.181.173.35:443" {
			t.Errorf("应优先连接公网 IPv4：%s", d)
		}
	}
}
