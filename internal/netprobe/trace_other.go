//go:build !linux

package netprobe

import "net/netip"

// openTracer：路由追踪依赖 Linux 的 IP_RECVERR，其他系统不支持（设计 42.5）。
func openTracer(netip.Addr) (tracer, error) { return nil, ErrICMPUnavailable }
