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

// linux 是真实的 Linux 采集器。
//
// 【安全】只读取所有用户可读的 /proc 与 /sys，不需要 root，也不需要任何 capability（设计 1.6.9）。
// 有状态：CPU 与网速依赖上一次采样，同一实例不可并发调用。
type linux struct {
	opts     Options
	prevCPU  cpuTimes               // 上一次 /proc/stat 采样，用于计算 CPU 使用率
	prevNet  map[string]netCounters // 上一次各网卡的累计字节数，用于计算网速
	prevTime time.Time              // prevNet 的采样时间
}

// New 返回真实的 Linux 采集器。未指定排除列表时使用 DefaultExclude，
// 跳过虚拟网卡，避免容器 / VPN 流量被重复计算（设计 5.6）。
func New(opts Options) Collector {
	if len(opts.Exclude) == 0 {
		opts.Exclude = DefaultExclude
	}
	return &linux{opts: opts, prevNet: map[string]netCounters{}}
}

// readFile 读取失败时返回空字符串。文件缺失是正常情况（精简容器、旧内核、未启用 IPv6），
// 单项缺失应留空该字段，而不是让整份上报失败（设计 43.5）。
func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// Collect 采集一次完整上报（不含 Timestamp / AgentVersion，由上报方填写）。
func (c *linux) Collect() (protocol.Report, error) {
	now := time.Now()
	var r protocol.Report

	// 系统信息
	r.System.Hostname, _ = os.Hostname()
	r.System.OS, r.System.OSVersion = parseOSRelease(readFile("/etc/os-release"))
	r.System.Kernel = strings.TrimSpace(readFile("/proc/sys/kernel/osrelease"))
	r.System.Arch = runtime.GOARCH
	// boot_id 每次启动都会变化，此时内核网卡计数从 0 重新开始。面板据此把新计数整体计为增量，
	// 启动到首次上报之间的流量不会丢失（设计 5.5，见 server/traffic.go 的 ComputeDelta）。
	r.System.BootID = strings.TrimSpace(readFile("/proc/sys/kernel/random/boot_id"))
	if f := strings.Fields(readFile("/proc/uptime")); len(f) > 0 {
		up, _ := strconv.ParseFloat(f[0], 64)
		r.System.Uptime = uint64(up)
	}

	// CPU：/proc/stat 是开机以来的累计 jiffies，使用率取与上一次采样之间的忙碌占比（首次为 0）。
	cur, cores := parseProcStat(readFile("/proc/stat"))
	r.CPU.Usage = cpuUsage(c.prevCPU, cur)
	r.CPU.Cores = cores
	c.prevCPU = cur
	if f := strings.Fields(readFile("/proc/loadavg")); len(f) >= 3 {
		r.CPU.Load1, _ = strconv.ParseFloat(f[0], 64)
		r.CPU.Load5, _ = strconv.ParseFloat(f[1], 64)
		r.CPU.Load15, _ = strconv.ParseFloat(f[2], 64)
	}

	// 内存：已用 = MemTotal - MemAvailable，而不是 MemTotal - MemFree。
	// 页缓存可以回收，算作已用会让长期运行的 VPS 看起来接近 100%（设计 4.5）。
	m := parseMeminfo(readFile("/proc/meminfo"))
	total, avail := m["MemTotal"], m["MemAvailable"]
	r.Memory = protocol.Memory{Total: total, Available: avail, Used: total - avail, Usage: pct(total-avail, total)}
	r.Swap = protocol.Swap{Total: m["SwapTotal"], Used: m["SwapTotal"] - m["SwapFree"]}

	// 磁盘：目前只采集根分区。TODO(A3): 多挂载点与磁盘 IO（设计 4.6、4.7）。
	// Statfs 不需要权限。已用 = Blocks - Bfree，root 保留块计为已用，与 df 的 Used 列一致。
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err == nil {
		dt := st.Blocks * uint64(st.Bsize)
		used := dt - st.Bfree*uint64(st.Bsize)
		r.Disk = []protocol.Disk{{Mount: "/", Total: dt, Used: used, Usage: pct(used, dt)}}
	}

	// 网络：上报原始累计计数，流量统计由面板负责（设计 5.3）；
	// 网速只用于展示，取与上一次采样的差值（设计 5.2）。
	counters := parseNetDev(readFile("/proc/net/dev"))
	elapsed := now.Sub(c.prevTime).Seconds()
	for _, name := range c.selectIfaces(counters) {
		cn := counters[name]
		ni := protocol.NetIface{Interface: name, RxBytes: cn.rx, TxBytes: cn.tx}
		// ifindex 让面板识别“同名网卡被重建”：未重启但计数从 0 重新开始（设计 5.5）。
		ni.IfIndex, _ = strconv.Atoi(strings.TrimSpace(readFile("/sys/class/net/" + name + "/ifindex")))
		// 计数变小（网卡重置）时本轮不计算网速：一次 0 比一个巨大的错误尖峰好。
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

// selectIfaces 选出参与统计的网卡（设计 5.6）：
// 显式指定的列表优先；否则取持有默认路由的网卡（IPv4 + IPv6，去重）；
// 找不到默认路由时，取所有未被排除的网卡。
// 默认路由网卡就是服务商计费的那块网卡，比把所有网卡相加更接近服务商面板的数值。
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
