//go:build linux

package collector

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
	prevCPU  procStat               // 上一次 /proc/stat 采样，用于计算 CPU 使用率与各类占比
	cpuModel string                 // /proc/cpuinfo 的型号，只在首次采集时读取
	prevNet  map[string]netCounters // 上一次各网卡的累计字节数，用于计算网速
	prevIO   map[string]ioCounters  // 上一次各磁盘的累计 IO，用于计算读写速率
	prevTime time.Time              // prevNet / prevIO 的采样时间
	ports    []protocol.ListenPort  // 上次采集的监听端口
	portsAt  time.Time              // ports 的采集时间：监听端口变化少，每分钟刷新一次（设计 4.9.1）
}

// New 返回真实的 Linux 采集器。未指定排除列表时使用 DefaultExclude，
// 跳过虚拟网卡，避免容器 / VPN 流量被重复计算（设计 5.6）。
func New(opts Options) Collector {
	if len(opts.Exclude) == 0 {
		opts.Exclude = DefaultExclude
	}
	return &linux{opts: opts, prevNet: map[string]netCounters{}, prevIO: map[string]ioCounters{}}
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

// collectListenPorts 读取 IPv4 / IPv6 的 TCP 与 UDP 表（设计 4.9.1）。按流读取：TCP 读到第一条已建立连接即停止。
func collectListenPorts() []protocol.ListenPort {
	var socks []listenSocket
	for _, proto := range []string{"tcp", "tcp6", "udp", "udp6"} {
		f, err := os.Open("/proc/net/" + proto)
		if err != nil {
			continue // 未启用 IPv6 等
		}
		socks = append(socks, parseListenSockets(f, proto)...)
		f.Close()
	}
	return mergeListenPorts(socks)
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

	if c.cpuModel == "" {
		c.cpuModel = parseCPUModel(readFile("/proc/cpuinfo"))
	}
	r.System.CPUModel = c.cpuModel

	// CPU：/proc/stat 是开机以来的累计 jiffies，使用率取与上一次采样之间的忙碌占比（首次为 0）。
	// 同时给出各类时间占比与每核使用率（设计 4.4）。
	cur := parseProcStat(readFile("/proc/stat"))
	r.CPU.Usage = cpuUsage(c.prevCPU.all, cur.all)
	r.CPU.Cores = len(cur.cores)
	r.CPU.Breakdown = cpuBreakdown(c.prevCPU.all, cur.all)
	r.CPU.PerCore = perCoreUsage(c.prevCPU.cores, cur.cores)
	r.CPU.TempC = readCPUTemp()
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
	r.Memory = protocol.Memory{Total: total, Available: avail, Used: total - avail, Usage: pct(total-avail, total),
		Free: m["MemFree"], Buffers: m["Buffers"], Cached: m["Cached"] + m["SReclaimable"]}

	// 进程数与套接字数（设计 4.9）
	r.Processes = &protocol.Processes{Total: countProcesses(), Running: cur.running}
	tcp4, udp4, tw := parseSockstat(readFile("/proc/net/sockstat"))
	tcp6, udp6, _ := parseSockstat(readFile("/proc/net/sockstat6"))
	r.Conns = &protocol.Conns{TCP: tcp4 + tcp6, UDP: udp4 + udp6, TimeWait: tw}
	if now.Sub(c.portsAt) >= time.Minute {
		c.ports, c.portsAt = collectListenPorts(), now
	}
	r.Ports = c.ports
	r.Swap = protocol.Swap{Total: m["SwapTotal"], Used: m["SwapTotal"] - m["SwapFree"]}

	// 磁盘容量：本地块设备文件系统的每个挂载点（设计 4.6）
	r.Disk = collectDisks(readFile("/proc/self/mounts"))

	// 磁盘 IO：只统计整块磁盘（/sys/block 下的设备），分区的 IO 已包含在所属磁盘中（设计 4.7）
	ioNow := parseDiskstats(readFile("/proc/diskstats"))
	elapsedIO := now.Sub(c.prevTime).Seconds()
	for name, cur := range ioNow {
		if skipIODevice(name) {
			continue
		}
		if _, err := os.Stat("/sys/block/" + name); err != nil {
			continue // 分区或已移除的设备
		}
		d := protocol.DiskIO{Device: name, ReadBytes: cur.readBytes, WriteBytes: cur.writeBytes,
			ReadOps: cur.readOps, WriteOps: cur.writeOps, IOTimeMs: cur.ioTimeMs}
		// 计数倒退（设备重新挂载）时本轮不计算速率，与网速处理一致
		if p, ok := c.prevIO[name]; ok && elapsedIO > 0 && cur.readBytes >= p.readBytes && cur.writeBytes >= p.writeBytes {
			d.ReadSpeed = uint64(float64(cur.readBytes-p.readBytes) / elapsedIO)
			d.WriteSpeed = uint64(float64(cur.writeBytes-p.writeBytes) / elapsedIO)
			d.ReadIOPS, d.WriteIOPS, d.AwaitMs, d.Util, _ = ioRates(p, cur, elapsedIO)
		}
		r.DiskIO = append(r.DiskIO, d)
	}
	sort.Slice(r.DiskIO, func(i, j int) bool { return r.DiskIO[i].Device < r.DiskIO[j].Device })
	c.prevIO = ioNow

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

// collectDisks 对选出的挂载点调用 statfs（不需要权限）。读不到挂载表时退回只采集 “/”。
//
// 已用 = Blocks - Bfree（root 保留块计为已用）；使用率 = 已用 / (已用 + Bavail)，与 df 的 Use% 一致：
// 普通用户可用空间耗尽时显示 100%，即使 root 保留块还有剩余。
func collectDisks(mounts string) []protocol.Disk {
	sel := selectMounts(parseMounts(mounts))
	if len(sel) == 0 {
		sel = []mountEntry{{Mount: "/"}}
	}
	var out []protocol.Disk
	for _, m := range sel {
		var st syscall.Statfs_t
		if err := syscall.Statfs(m.Mount, &st); err != nil || st.Blocks == 0 {
			continue // 单个挂载点失败不影响其他（设计 43.5）
		}
		bs := uint64(st.Bsize)
		total := st.Blocks * bs
		used := total - st.Bfree*bs
		avail := st.Bavail * bs
		out = append(out, protocol.Disk{Mount: m.Mount, Total: total, Used: used, Available: avail,
			Usage: pct(used, used+avail), FSType: m.FSType, Device: m.Device})
	}
	return out
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

// countProcesses 统计 /proc 下的进程目录数（只读目录名，不读取任何进程的内容）。
func countProcesses() int {
	f, err := os.Open("/proc")
	if err != nil {
		return 0
	}
	defer f.Close()
	names, _ := f.Readdirnames(-1)
	n := 0
	for _, name := range names {
		if name != "" && name[0] >= '0' && name[0] <= '9' {
			n++
		}
	}
	return n
}

// readCPUTemp 读取 hwmon 与 thermal_zone 中可信的 CPU 温度（设计 4.4）；都读不到时返回 0，不上报。
// 【安全】只读 /sys，不需要任何权限。
func readCPUTemp() float64 {
	var rs []tempReading
	hw, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, d := range hw {
		name := strings.TrimSpace(readFile(d + "/name"))
		inputs, _ := filepath.Glob(d + "/temp*_input")
		for _, in := range inputs {
			if v, err := strconv.ParseInt(strings.TrimSpace(readFile(in)), 10, 64); err == nil {
				rs = append(rs, tempReading{name, v})
			}
		}
	}
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*")
	for _, d := range zones {
		if v, err := strconv.ParseInt(strings.TrimSpace(readFile(d+"/temp")), 10, 64); err == nil {
			rs = append(rs, tempReading{strings.TrimSpace(readFile(d + "/type")), v})
		}
	}
	return pickCPUTemp(rs)
}
