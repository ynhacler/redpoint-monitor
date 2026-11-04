package probe

import (
	"context"
	"net/netip"
	"time"

	"vpsmon/internal/netprobe"
)

// UseFakeTrace 让路由追踪返回假数据，供 macOS 与 --fake 联调（设计 40）：路径中同时含有三网的骨干地址，
// 面板按目标的运营商取对应的一段识别；编号为偶数的目标与奇数的目标识别结果不同。不发送任何报文。
func (p *Prober) UseFakeTrace() {
	p.trace = func(ctx context.Context, a netip.Addr) ([]netprobe.Hop, bool, error) {
		odd := a.As16()[15]%2 == 1
		path := []string{"10.0.0.1", "", "59.43.180.9", "219.158.8.5", "223.118.2.1", "", ""}
		if !odd {
			path = []string{"10.0.0.1", "203.0.113.1", "59.43.246.1", "202.97.50.1", "210.51.12.2", "223.120.19.1", ""}
		}
		var hops []netprobe.Hop
		for i, ip := range path {
			h := netprobe.Hop{TTL: i + 1}
			if ip != "" {
				h.Addr, h.RTT = netip.MustParseAddr(ip), time.Duration(5+i*12)*time.Millisecond
			}
			hops = append(hops, h)
		}
		hops = append(hops, netprobe.Hop{TTL: len(path) + 1, Addr: a, RTT: 86 * time.Millisecond})
		return hops, true, nil
	}
}
