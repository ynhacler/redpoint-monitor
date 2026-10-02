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

// Collector is stateful: CPU usage and network speed are computed against the previous
// call, so one instance must be reused for the agent's lifetime and not called concurrently.
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

// excluded reports whether name matches any glob in patterns (e.g. "veth*").
func excluded(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// pct returns used/total as 0-100, and 0 instead of NaN when total is unknown.
func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) * 100 / float64(total)
}
