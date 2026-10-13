// Package probe 是 Agent 端的三网延迟探测（设计 42.3.2）：按面板下发的目标，每 5 分钟一轮测量延迟与丢包。
//
// 【安全】以下上限写在 Agent 代码中，面板无法放宽（设计 42.6）：
// 目标 ≤ 64 个且只能是公网 IP 或域名（protocol.ProbeTarget.Valid）；每轮间隔由面板在 1～60 分钟内设置（默认 5 分钟），
// 每个样本的超时与样本间隔由面板在 1～10 秒内设置（默认 4 秒、1 秒），
// 上一轮结束后才计时，不会重叠；同时最多探测 4 个目标；
// 只做 TCP 连接（不发送数据）与 ICMP 回显。本机存在 no-probe 文件时不做任何探测。
// 域名目标（修订第 132 条）每轮解析一次，本轮的样本都用同一个地址；【安全】只连接解析结果中的公网地址，
// 解析到内网、回环等地址时不探测，记为 blocked。
//
// 资源（修订第 129 条）：每轮固定 4 个工作 goroutine，不按目标数创建；每个 ICMP 目标的样本复用一个套接字与小缓冲；
// TCP 关闭时直接复位，不留 TIME_WAIT；目标拒绝连接或不可达时不再测剩下的样本（结果不会变，只是多发包）。
// 一个 TCP 样本约 4 个包（SYN、SYN-ACK、ACK、RST），一个 ICMP 样本 2 个包，64 个目标每轮合计约 100 KB 以内。
package probe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"vpsmon/internal/netprobe"
	"vpsmon/internal/protocol"
)

// 每轮探测的节奏与并发（设计 42.6）：Agent 端写死，面板不能放宽。
const (
	FirstDelay  = 20 * time.Second // 启动或目标变化后第一轮的延迟
	Samples     = 5                // 每个目标每轮的样本数
	Concurrency = 4
	maxPending  = 3 // 上报失败时最多保留几轮结果（断网时不无限堆积）
)

// Prober 保存目标列表与待上报的结果；所有方法可并发调用。
type Prober struct {
	Disabled func() bool // 本机是否关闭了探测（no-probe 文件）
	Now      func() time.Time

	mu       sync.Mutex
	version  string
	targets  []protocol.ProbeTarget
	interval time.Duration
	timing   protocol.ProbeTiming // 每个样本的超时与样本间隔（修订第 133 条）
	changed  chan struct{}
	pending  [][]protocol.ProbeResult

	// 以下供测试替换
	tcp    func(ctx context.Context, addr string) (time.Duration, error)
	ping   func(ip net.IP) (pinger, error) // 打开到一个目标的 ICMP 套接字，这个目标的样本共用
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	sleep  func(ctx context.Context, d time.Duration) // 样本之间的等待
}

// pinger 是 netprobe.Pinger 的接口，便于测试替换。
type pinger interface {
	Echo(ctx context.Context) (time.Duration, error)
	Close() error
}

func openPinger(ip net.IP) (pinger, error) {
	p, err := netprobe.NewPinger(ip)
	if err != nil {
		return nil, err // 不能返回装着 nil 指针的接口
	}
	return p, nil
}

// New 返回探测器；disabled 为空表示不检查本机开关。
func New(disabled func() bool) *Prober {
	return &Prober{Disabled: disabled, Now: time.Now, changed: make(chan struct{}, 1),
		timing: protocol.DefaultTiming,
		tcp:    netprobe.TCPConnect, ping: openPinger, sleep: sleepCtx,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}}
}

func (p *Prober) off() bool { return p.Disabled != nil && p.Disabled() }

// Version 是上报请求头 X-Probe-Version 的值：本机关闭时为 off，尚未收到目标时为空。
func (p *Prober) Version() string {
	if p.off() {
		return protocol.ProbeOff
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

// Update 保存面板下发的目标列表、间隔与超时（响应头 X-Probe-Version、X-Probe-Targets、X-Probe-Interval、
// X-Probe-Timing）；版本相同时忽略。旧版面板不发 X-Probe-Timing，按默认超时 4 秒、样本间隔 1 秒。
func (p *Prober) Update(version, encoded, interval, timing string) {
	if version == "" || len(version) > 64 {
		return
	}
	targets := protocol.ParseProbeTargets(encoded)
	iv := time.Duration(protocol.ParseProbeInterval(interval)) * time.Second
	p.mu.Lock()
	if version == p.version {
		p.mu.Unlock()
		return
	}
	p.version, p.targets, p.interval, p.timing = version, targets, iv, protocol.ParseProbeTiming(timing)
	p.mu.Unlock()
	select {
	case p.changed <- struct{}{}:
	default:
	}
}

// Targets 返回当前目标列表。
func (p *Prober) Targets() []protocol.ProbeTarget {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.targets
}

// Interval 返回每轮间隔；面板未指定时为 5 分钟。
func (p *Prober) Interval() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.interval <= 0 {
		return protocol.DefaultProbeInterval * time.Second
	}
	return p.interval
}

// Timing 返回每个样本的超时与样本间隔。
func (p *Prober) Timing() protocol.ProbeTiming {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.timing
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Drain 取出待上报的结果（随下一份上报带回）；没有时返回 nil。
func (p *Prober) Drain() []protocol.ProbeResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []protocol.ProbeResult
	for _, r := range p.pending {
		out = append(out, r...)
	}
	p.pending = nil
	return out
}

// Run 在独立 goroutine 中运行，直到 ctx 取消。
func (p *Prober) Run(ctx context.Context) {
	timer := time.NewTimer(FirstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.changed:
			// 目标变化：稍后立即测一轮，不等满 5 分钟
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(FirstDelay)
		case <-timer.C:
			p.RunOnce(ctx)
			timer.Reset(p.Interval()) // 本轮结束后才开始计时：目标多、间隔短时也不会重叠
		}
	}
}

// RunOnce 测一轮：Concurrency 个工作 goroutine 依次领取目标，结果放入待上报队列。
func (p *Prober) RunOnce(ctx context.Context) {
	targets := p.Targets()
	if len(targets) == 0 || p.off() {
		return
	}
	at := p.Now().Unix()
	results := make([]protocol.ProbeResult, len(targets))
	var next atomic.Int32
	var wg sync.WaitGroup
	for w := 0; w < min(Concurrency, len(targets)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(targets) {
					return
				}
				results[i] = p.measure(ctx, targets[i])
				results[i].At = at
			}
		}()
	}
	wg.Wait()
	p.mu.Lock()
	p.pending = append(p.pending, results)
	if len(p.pending) > maxPending {
		p.pending = p.pending[len(p.pending)-maxPending:]
	}
	p.mu.Unlock()
}

// measure 对一个目标取 Samples 个样本：延迟取成功样本的中位数，丢包率为失败样本的占比。
func (p *Prober) measure(ctx context.Context, t protocol.ProbeTarget) protocol.ProbeResult {
	r := protocol.ProbeResult{TargetID: t.ID}
	tm := p.Timing()
	wait, gap := time.Duration(tm.Timeout)*time.Second, time.Duration(tm.Gap)*time.Second
	addr := t.Addr
	if t.Host != "" {
		lctx, cancel := context.WithTimeout(ctx, wait)
		addrs, err := p.lookup(lctx, t.Host)
		cancel()
		a, ok := protocol.PickProbeAddr(addrs)
		switch {
		case err != nil:
			r.Loss, r.Error = 100, "dns"
			return r
		case !ok:
			r.Loss, r.Error = 100, "blocked" // 【安全】解析结果没有公网地址：不探测
			return r
		}
		addr = a
	}
	sample := func(ctx context.Context) (time.Duration, error) { return p.tcp(ctx, t.HostPort(addr)) }
	if t.Proto == "icmp" {
		pg, err := p.ping(net.IP(addr.Unmap().AsSlice()))
		if err != nil {
			r.Loss, r.Error = 100, errorCode(err) // 本机不允许 ICMP：不必逐个样本再试
			return r
		}
		defer pg.Close()
		sample = pg.Echo
	}
	rtts := make([]float64, 0, Samples)
	var lastErr error
	for i := 0; i < Samples && ctx.Err() == nil; i++ {
		if i > 0 {
			p.sleep(ctx, gap)
		}
		sctx, cancel := context.WithTimeout(ctx, wait)
		rtt, err := sample(sctx)
		cancel()
		if err != nil {
			lastErr = err
			if len(rtts) == 0 && definite(err) {
				break // 拒绝连接、不可达或本机不允许 ICMP：后面的样本结果一样，不再发包
			}
			continue
		}
		rtts = append(rtts, float64(rtt.Microseconds())/1000)
	}
	r.Loss = 100 * float64(Samples-len(rtts)) / Samples
	if len(rtts) > 0 {
		sort.Float64s(rtts)
		m := rtts[len(rtts)/2]
		if len(rtts)%2 == 0 {
			m = (rtts[len(rtts)/2-1] + m) / 2
		}
		m = float64(int(m*10+0.5)) / 10 // 保留 1 位小数
		r.Latency = &m
	} else {
		r.Error = errorCode(lastErr)
	}
	return r
}

// definite 表示失败是确定的（重试结果相同），不是偶发的超时。
func definite(err error) bool {
	switch errorCode(err) {
	case "refused", "unreachable", "icmp_unavailable":
		return true
	}
	return false
}

// errorCode 把失败原因归为几类，供界面显示中文说明。
func errorCode(err error) string {
	var ne net.Error
	switch {
	case err == nil:
		return ""
	case errors.Is(err, netprobe.ErrICMPUnavailable):
		return "icmp_unavailable"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return "unreachable"
	case errors.Is(err, netprobe.ErrNoReply), errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	return "error"
}
