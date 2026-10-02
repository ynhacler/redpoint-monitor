//go:build !linux

package collector

import "log"

// New 在非 Linux 平台上返回假数据采集器，便于在 macOS 上开发。
func New(_ Options) Collector {
	log.Println("collector: non-Linux platform, using FAKE metrics")
	return NewFake()
}
