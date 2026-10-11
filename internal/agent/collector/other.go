//go:build !linux

package collector

import (
	"strings"

	"vpsmon/internal/agent/alog"
	"vpsmon/internal/protocol"
)

// New 在非 Linux 平台上返回假数据采集器，便于在 macOS 上开发。
func New(_ Options) Collector {
	alog.Printf("collector: non-Linux platform, using FAKE metrics")
	return NewFake()
}

// WatchProcs 在非 Linux 平台上返回假数据：名称以 “stopped” 开头的视为没有运行，其余各 1 个，便于联调界面与告警。
func WatchProcs(names []string) []protocol.ProcWatch {
	out := make([]protocol.ProcWatch, len(names))
	for i, n := range names {
		out[i] = protocol.ProcWatch{Name: n, Count: 1}
		if strings.HasPrefix(n, "stopped") {
			out[i].Count = 0
		}
	}
	return out
}
