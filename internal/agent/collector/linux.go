//go:build linux

package collector

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vpsmon/internal/protocol"
)

// linux reads everything from /proc and /sys, which are world-readable, so the agent needs
// no root and no capabilities (security invariant 8, design 1.6.9).
type linux struct {
	opts     Options
	prevCPU  cpuTimes               // previous /proc/stat sample, for CPU usage delta
	prevNet  map[string]netCounters // previous byte counters per interface, for speed
	prevTime time.Time              // when prevNet was taken
}

// New returns the real Linux collector. Without an explicit exclude list it skips virtual
// interfaces so container/VPN traffic is not counted twice (design 5.6).
func New(opts Options) Collector {
	if len(opts.Exclude) == 0 {
		opts.Exclude = DefaultExclude
	}
	return &linux{opts: opts, prevNet: map[string]netCounters{}}
}

// readFile returns "" on error. Missing files are normal (minimal containers, old kernels,
// no IPv6), and a missing metric should leave a zero field rather than drop the whole report.
func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func (c *linux) Collect() (protocol.Report, error) {
	now := time.Now()
	var r protocol.Report

	// System
	r.System.Hostname, _ = os.Hostname()
	r.System.OS, r.System.OSVersion = parseOSRelease(readFile("/etc/os-release"))
	r.System.Kernel = strings.TrimSpace(readFile("/proc/sys/kernel/osrelease"))
	r.System.Arch = runtime.GOARCH
	// boot_id changes on every reboot, when the kernel counters restart from 0. The server
	// then counts the new counter value as the delta, so traffic between boot and the first
	// report is not lost (design 5.5, server/traffic.go ComputeDelta).
	r.System.BootID = strings.TrimSpace(readFile("/proc/sys/kernel/random/boot_id"))
	if f := strings.Fields(readFile("/proc/uptime")); len(f) > 0 {
		up, _ := strconv.ParseFloat(f[0], 64)
		r.System.Uptime = uint64(up)
	}

	// CPU: /proc/stat holds cumulative jiffies since boot, so usage is the busy share of the
	// delta since the last call (the first call returns 0).
	cur, cores := parseProcStat(readFile("/proc/stat"))
	r.CPU.Usage = cpuUsage(c.prevCPU, cur)
	r.CPU.Cores = cores
	c.prevCPU = cur
	if f := strings.Fields(readFile("/proc/loadavg")); len(f) >= 3 {
		r.CPU.Load1, _ = strconv.ParseFloat(f[0], 64)
		r.CPU.Load5, _ = strconv.ParseFloat(f[1], 64)
		r.CPU.Load15, _ = strconv.ParseFloat(f[2], 64)
	}

	// Memory: "used" = MemTotal - MemAvailable, not MemTotal - MemFree. Page cache is
	// reclaimable, so counting it as used would make every long-running VPS look ~100% full.
	m := parseMeminfo(readFile("/proc/meminfo"))
	total, avail := m["MemTotal"], m["MemAvailable"]
	r.Memory = protocol.Memory{Total: total, Available: avail, Used: total - avail, Usage: pct(total-avail, total)}
	r.Swap = protocol.Swap{Total: m["SwapTotal"], Used: m["SwapTotal"] - m["SwapFree"]}

	// Disk (root only for now; TODO: configurable mounts, design 4.6)
	// Statfs needs no privileges. Used = Blocks - Bfree, so root-reserved blocks count as used,
	// matching `df`'s Used column.
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err == nil {
		dt := st.Blocks * uint64(st.Bsize)
		used := dt - st.Bfree*uint64(st.Bsize)
		r.Disk = []protocol.Disk{{Mount: "/", Total: dt, Used: used, Usage: pct(used, dt)}}
	}

	// Network: report raw cumulative counters; the server owns traffic accounting (design 5.3).
	// Speed is display-only, from the delta to the previous sample (design 5.2).
	counters := parseNetDev(readFile("/proc/net/dev"))
	elapsed := now.Sub(c.prevTime).Seconds()
	for _, name := range c.selectIfaces(counters) {
		cn := counters[name]
		ni := protocol.NetIface{Interface: name, RxBytes: cn.rx, TxBytes: cn.tx}
		// ifindex lets the server notice an interface was recreated under the same name
		// (counters restart from 0 without a reboot).
		ni.IfIndex, _ = strconv.Atoi(strings.TrimSpace(readFile("/sys/class/net/" + name + "/ifindex")))
		// Skip speed when a counter went backwards (interface reset); a huge bogus spike is
		// worse than one sample of zero.
		if p, ok := c.prevNet[name]; ok && elapsed > 0 && cn.rx >= p.rx && cn.tx >= p.tx {
			ni.RxSpeed = uint64(float64(cn.rx-p.rx) / elapsed)
			ni.TxSpeed = uint64(float64(cn.tx-p.tx) / elapsed)
		}
		r.Network = append(r.Network, ni)
	}
	c.prevNet = counters
	c.prevTime = now
	return r, nil
}

// selectIfaces: explicit list wins; otherwise default-route interfaces (v4+v6, deduped);
// if no default route is found, every non-excluded interface (design 5.6).
// The default-route interface is what the provider meters, so it matches the billing
// numbers better than summing every NIC.
func (c *linux) selectIfaces(counters map[string]netCounters) []string {
	if len(c.opts.Interfaces) > 0 {
		var out []string
		for _, n := range c.opts.Interfaces {
			if _, ok := counters[n]; ok {
				out = append(out, n)
			}
		}
		return out
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range append(parseDefaultRouteIfaces(readFile("/proc/net/route")),
		parseDefaultRoute6Ifaces(readFile("/proc/net/ipv6_route"))...) {
		if _, ok := counters[n]; ok && !seen[n] && !excluded(n, c.opts.Exclude) {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > 0 {
		return out
	}
	for n := range counters {
		if !excluded(n, c.opts.Exclude) {
			out = append(out, n)
		}
	}
	return out
}
