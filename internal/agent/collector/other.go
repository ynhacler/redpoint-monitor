//go:build !linux

package collector

import "log"

// New on non-Linux platforms returns fake data so you can develop on macOS.
func New(_ Options) Collector {
	log.Println("collector: non-Linux platform, using FAKE metrics")
	return NewFake()
}
