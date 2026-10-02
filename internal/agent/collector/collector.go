// Package collector gathers system metrics for the agent.
//
// Real collection only works on Linux (/proc, /sys). On other platforms (your Mac)
// New() returns a fake collector so the whole agent → server → web/app loop can be
// developed locally. Use NewFake() to force fake data on Linux too.
package collector

import (
	"path/filepath"

	"vpsmon/internal/protocol"
)

type Collector interface {
	// Collect returns a report without Timestamp/AgentVersion (the reporter fills those).
	Collect() (protocol.Report, error)
}

// Options configure which network interfaces count toward traffic.
type Options struct {
	Interfaces []string // explicit list; empty = auto (default-route interfaces)
	Exclude    []string // glob patterns, applied in auto mode
}

// DefaultExclude avoids double counting virtual interfaces (design doc 5.6).
var DefaultExclude = []string{"lo", "docker*", "veth*", "br-*", "virbr*", "tun*", "wg*"}

func excluded(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) * 100 / float64(total)
}
