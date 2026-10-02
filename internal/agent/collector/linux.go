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

type linux struct {
	opts     Options
	prevCPU  cpuTimes
	prevNet  map[string]netCounters
	prevTime time.Time
}

func New(opts Options) Collector {
	if len(opts.Exclude) == 0 {
		opts.Exclude = DefaultExclude
	}
	return &linux{opts: opts, prevNet: map[string]netCounters{}}
}

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
	r.System.BootID = strings.TrimSpace(readFile("/proc/sys/kernel/random/boot_id"))
	if f := strings.Fields(readFile("/proc/uptime")); len(f) > 0 {
		up, _ := strconv.ParseFloat(f[0], 64)
		r.System.Uptime = uint64(up)
	}

	// CPU
	cur, cores := parseProcStat(readFile("/proc/stat"))
	r.CPU.Usage = cpuUsage(c.prevCPU, cur)
	r.CPU.Cores = cores
	c.prevCPU = cur
	if f := strings.Fields(readFile("/proc/loadavg")); len(f) >= 3 {
		r.CPU.Load1, _ = strconv.ParseFloat(f[0], 64)
		r.CPU.Load5, _ = strconv.ParseFloat(f[1], 64)
		r.CPU.Load15, _ = strconv.ParseFloat(f[2], 64)
	}

	// Memory
	m := parseMeminfo(readFile("/proc/meminfo"))
	total, avail := m["MemTotal"], m["MemAvailable"]
	r.Memory = protocol.Memory{Total: total, Available: avail, Used: total - avail, Usage: pct(total-avail, total)}
	r.Swap = protocol.Swap{Total: m["SwapTotal"], Used: m["SwapTotal"] - m["SwapFree"]}

	// Disk (root only for now; TODO: configurable mounts)
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err == nil {
		dt := st.Blocks * uint64(st.Bsize)
		used := dt - st.Bfree*uint64(st.Bsize)
		r.Disk = []protocol.Disk{{Mount: "/", Total: dt, Used: used, Usage: pct(used, dt)}}
	}

	// Network
	counters := parseNetDev(readFile("/proc/net/dev"))
	elapsed := now.Sub(c.prevTime).Seconds()
	for _, name := range c.selectIfaces(counters) {
		cn := counters[name]
		ni := protocol.NetIface{Interface: name, RxBytes: cn.rx, TxBytes: cn.tx}
		ni.IfIndex, _ = strconv.Atoi(strings.TrimSpace(readFile("/sys/class/net/" + name + "/ifindex")))
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
// if no default route is found, every non-excluded interface.
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
