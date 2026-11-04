package probe

// 回程线路的路由追踪（设计 42.5，修订第 282 条）：面板在三网延迟目标中指定最多 3 个（电信、联通、移动各一），
// Agent 每 TraceInterval 追踪一次，结果随下一份上报带回，由面板判断线路类型。
//
// 【安全】以下写在 Agent 代码中，面板无法放宽（设计 42.6）：只追踪已下发且通过校验的目标（公网地址；域名只用解析出的公网地址），
// 最多 3 个、依次进行，每 6 小时一次，每个目标最多 30 跳、每跳 1 个回显请求、等待 1 秒；本机存在 no-probe 文件时不追踪。
// 一次追踪最多约 60 个小包。

import (
	"context"
	"math"
	"net/netip"
	"time"

	"vpsmon/internal/netprobe"
	"vpsmon/internal/protocol"
)

// 路由追踪的节奏（设计 42.6）：Agent 端写死。
const (
	TraceFirstDelay = 2 * time.Minute // 启动或追踪目标变化后第一次的延迟（避开启动时的第一轮延迟探测）
	TraceInterval   = 6 * time.Hour
	TraceHopWait    = time.Second
)

func realTrace(ctx context.Context, a netip.Addr) ([]netprobe.Hop, bool, error) {
	return netprobe.Trace(ctx, a, protocol.MaxTraceHops, TraceHopWait)
}

// DrainTraces 取出待上报的路由追踪（随下一份上报带回）；没有时返回 nil。
func (p *Prober) DrainTraces() []protocol.TraceResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.traces
	p.traces = nil
	return out
}

// runTraces 按 TraceInterval 追踪；追踪目标变化后 TraceFirstDelay 重新开始。
func (p *Prober) runTraces(ctx context.Context) {
	timer := time.NewTimer(TraceFirstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.traceChanged:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(TraceFirstDelay)
		case <-timer.C:
			p.TraceOnce(ctx)
			timer.Reset(TraceInterval)
		}
	}
}

// traceTargets 返回要追踪的目标：只取当前目标列表中存在的编号。
func (p *Prober) traceTargets() []protocol.ProbeTarget {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []protocol.ProbeTarget
	for _, id := range p.traceIDs {
		for _, t := range p.targets {
			if t.ID == id {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// TraceOnce 依次追踪每个目标，结果替换待上报的旧结果（上报失败时只保留最新一次）。
func (p *Prober) TraceOnce(ctx context.Context) {
	targets := p.traceTargets()
	if len(targets) == 0 || p.off() {
		return
	}
	out := make([]protocol.TraceResult, 0, len(targets))
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		out = append(out, p.traceOne(ctx, t))
	}
	p.mu.Lock()
	p.traces = out
	p.mu.Unlock()
}

func (p *Prober) traceOne(ctx context.Context, t protocol.ProbeTarget) protocol.TraceResult {
	r := protocol.TraceResult{TargetID: t.ID, At: p.Now().Unix()}
	addr := t.Addr
	if t.Host != "" {
		lctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		addrs, err := p.lookup(lctx, t.Host)
		cancel()
		a, ok := protocol.PickProbeAddr(addrs)
		switch {
		case err != nil:
			r.Error = "dns"
			return r
		case !ok:
			r.Error = "blocked" // 【安全】解析结果没有公网地址：不追踪
			return r
		}
		addr = a
	}
	if !protocol.PublicProbeAddr(addr) {
		r.Error = "blocked"
		return r
	}
	hops, reached, err := p.trace(ctx, addr)
	if err != nil {
		r.Error = errorCode(err)
		return r
	}
	r.Reached = reached
	for _, h := range hops {
		th := protocol.TraceHop{TTL: h.TTL}
		if h.Addr.IsValid() {
			th.IP = h.Addr.Unmap().String()
			ms := math.Round(float64(h.RTT.Microseconds())/100) / 10
			th.RTT = &ms
		}
		r.Hops = append(r.Hops, th)
	}
	return r
}
