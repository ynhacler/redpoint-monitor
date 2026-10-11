// Package probe 是 Agent 端的三网延迟探测（设计 42.3.2）：按面板下发的目标，每 5 分钟一轮测量延迟与丢包。
//
// 【安全】以下上限写在 Agent 代码中，面板无法放宽（设计 42.6）：
// 目标 ≤ 64 个且只能是公网 IP（protocol.ProbeTarget.Valid）；每轮间隔固定 5 分钟；同时最多探测 4 个目标；
// 只做 TCP 连接（不发送数据）与 ICMP 回显。本机存在 no-probe 文件时不做任何探测。
package probe

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"syscall"
	"time"

	"vpsmon/internal/netprobe"
	"vpsmon/internal/protocol"
)

const (
	Interval    = 5 * time.Minute  // 每轮间隔
	FirstDelay  = 20 * time.Second // 启动或目标变化后第一轮的延迟
	Samples     = 5                // 每个目标每轮的样本数
	SampleGap   = time.Second      // 样本之间的间隔
	SampleWait  = 2 * time.Second  // 单个样本的等待上限
	Concurrency = 4
	maxPending  = 3 // 上报失败时最多保留几轮结果（断网时不无限堆积）
)

// Prober 保存目标列表与待上报的结果；所有方法可并发调用。
type Prober struct {
	Disabled func() bool // 本机是否关闭了探测（no-probe 文件）
	Now      func() time.Time

	mu      sync.Mutex
	version string
	targets []protocol.ProbeTarget
	changed chan struct{}
	pending [][]protocol.ProbeResult

	// 以下供测试替换
	tcp  func(ctx context.Context, addr string) (time.Duration, error)
	echo func(ctx context.Context, ip net.IP) (time.Duration, error)
	gap  time.Duration
}

// New 返回探测器；disabled 为空表示不检查本机开关。
func New(disabled func() bool) *Prober {
	return &Prober{Disabled: disabled, Now: time.Now, changed: make(chan struct{}, 1),
		tcp: netprobe.TCPConnect, echo: netprobe.Echo, gap: SampleGap}
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

// Update 保存面板下发的目标列表（响应头 X-Probe-Version、X-Probe-Targets）；版本相同时忽略。
func (p *Prober) Update(version, encoded string) {
	if version == "" || len(version) > 64 {
		return
	}
	targets := protocol.ParseProbeTargets(encoded)
	p.mu.Lock()
	if version == p.version {
		p.mu.Unlock()
		return
	}
	p.version, p.targets = version, targets
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
			timer.Reset(Interval)
		}
	}
}

// RunOnce 测一轮：所有目标并发（最多 Concurrency 个），结果放入待上报队列。
func (p *Prober) RunOnce(ctx context.Context) {
	targets := p.Targets()
	if len(targets) == 0 || p.off() {
		return
	}
	at := p.Now().Unix()
	results := make([]protocol.ProbeResult, len(targets))
	sem := make(chan struct{}, Concurrency)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t protocol.ProbeTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = p.measure(ctx, t)
			results[i].At = at
		}(i, t)
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
	var rtts []float64
	var lastErr error
	for i := 0; i < Samples && ctx.Err() == nil; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(p.gap):
			}
		}
		sctx, cancel := context.WithTimeout(ctx, SampleWait)
		var rtt time.Duration
		var err error
		if t.Proto == "icmp" {
			rtt, err = p.echo(sctx, net.IP(t.Addr.Unmap().AsSlice()))
		} else {
			rtt, err = p.tcp(sctx, t.HostPort())
		}
		cancel()
		if err != nil {
			lastErr = err
			if errors.Is(err, netprobe.ErrICMPUnavailable) {
				break // 本机不允许 ICMP：后续样本同样失败，不再尝试
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
