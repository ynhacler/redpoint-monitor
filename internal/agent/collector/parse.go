package collector

// Pure parsers for /proc files. No build tag, so they are unit-testable on any OS.

import (
	"bufio"
	"strconv"
	"strings"
)

// cpuTimes is a snapshot of cumulative CPU jiffies since boot (all cores summed).
type cpuTimes struct{ idle, total uint64 }

// parseProcStat reads the aggregate "cpu" line of /proc/stat and counts the per-core
// "cpuN" lines. Counting lines reflects the cores this VPS actually has online, unlike
// runtime.NumCPU which can be limited by the agent's own cgroup/affinity.
func parseProcStat(s string) (cpuTimes, int) {
	var t cpuTimes
	cores := 0
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if f[0] == "cpu" {
			for i, v := range f[1:] {
				n, _ := strconv.ParseUint(v, 10, 64)
				// user nice system idle iowait irq softirq steal guest guest_nice
				if i >= 8 { // guest time is already included in user/nice
					break
				}
				t.total += n
				// iowait counts as idle: the CPU is free, just waiting on disk. Steal (i == 7)
				// stays busy so an oversold host shows up as high usage.
				if i == 3 || i == 4 { // idle + iowait
					t.idle += n
				}
			}
		} else if strings.HasPrefix(f[0], "cpu") {
			cores++
		}
	}
	return t, cores
}

// cpuUsage returns the busy percentage between two samples. It returns 0 for the first
// sample (no prev) and when counters went backwards, instead of a nonsense value.
func cpuUsage(prev, cur cpuTimes) float64 {
	dt := cur.total - prev.total
	if prev.total == 0 || dt == 0 || cur.total < prev.total {
		return 0
	}
	di := cur.idle - prev.idle
	return float64(dt-di) * 100 / float64(dt)
}

// parseMeminfo returns values in bytes keyed by field name (MemTotal, MemAvailable, ...).
func parseMeminfo(s string) map[string]uint64 {
	m := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(f[0], 10, 64)
		// The kernel writes "kB" but means KiB (1024). Lines without a unit
		// (e.g. HugePages_Total) are plain counts.
		if len(f) > 1 && f[1] == "kB" {
			n *= 1024
		}
		m[k] = n
	}
	return m
}

type netCounters struct{ rx, tx uint64 }

// parseNetDev parses /proc/net/dev into per-interface byte counters.
func parseNetDev(s string) map[string]netCounters {
	out := map[string]netCounters{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue // header lines
		}
		// 8 receive fields then 8 transmit fields, each group starting with bytes,
		// so RX bytes is f[0] and TX bytes is f[8].
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		out[strings.TrimSpace(name)] = netCounters{rx: rx, tx: tx}
	}
	return out
}

// parseDefaultRouteIfaces returns interfaces holding an IPv4 default route (/proc/net/route).
func parseDefaultRouteIfaces(s string) []string {
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		// Columns: Iface Destination(hex) Gateway ... ; destination 0.0.0.0 is the default route.
		// Multiple entries per interface are possible (metrics), hence the dedupe.
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[1] == "00000000" && !seen[f[0]] {
			seen[f[0]] = true
			out = append(out, f[0])
		}
	}
	return out
}

// parseDefaultRoute6Ifaces returns interfaces holding an IPv6 default route (/proc/net/ipv6_route).
func parseDefaultRoute6Ifaces(s string) []string {
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// dest(32 hex) dest_prefix_len ... iface(last)
		if len(f) >= 10 && f[0] == strings.Repeat("0", 32) && f[1] == "00" {
			name := f[len(f)-1]
			// The kernel lists an unreachable ::/0 route on lo; it carries no real traffic.
			if name != "lo" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// parseOSRelease returns ID and VERSION_ID from /etc/os-release (e.g. "ubuntu", "24.04").
// ID is machine-friendly and stable, unlike PRETTY_NAME; the UI formats it.
func parseOSRelease(s string) (id, version string) {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			id = v
		case "VERSION_ID":
			version = v
		}
	}
	return
}
