//go:build linux

package collector

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"vpsmon/internal/agent/alog"
	"vpsmon/internal/protocol"
)

// linux 是真实的 Linux 采集器。
//
// 【安全】只读取所有用户可读的 /proc 与 /sys，不需要 root，也不需要任何 capability（设计 1.6.9）。
// 有状态：CPU 与网速依赖上一次采样，同一实例不可并发调用。
//
// 容错（设计 43.5）：每个采集项由 guard 隔离，先算出结果再一次性写入上报与内部状态；
// 某项失败或 panic 时该项本轮留空，原因写入 collect_errors，其余照常上报。
type linux struct {
	opts        Options
	prevCPU     procStat               // 上一次 /proc/stat 采样，用于计算 CPU 使用率与各类占比
	cpuModel    string                 // /proc/cpuinfo 的型号，只在首次采集时读取
	bits        int                    // 内核网卡计数器位数（32 / 64），启动时读取一次（设计 5.5）
	prevNet     map[string]netCounters // 上一次各网卡的累计字节数，用于计算网速
	prevNetAt   time.Time              // prevNet 的采样时间
	lastIfaces  []string               // 上次选中的统计网卡；默认路由短暂缺失时沿用（设计 5.6）
	prevIO      map[string]ioCounters  // 上一次各磁盘的累计 IO，用于计算读写速率
	prevIOAt    time.Time              // prevIO 的采样时间
	ports       []protocol.ListenPort  // 上次采集的监听端口
	portsAt     time.Time              // ports 的采集时间：监听端口变化少，每分钟刷新一次（设计 4.9.1）
	statfs      *statfsProber          // 带超时的 statfs（设计 43.5）
	memEstimate bool                   // 已记录过“没有 MemAvailable，按估算”的日志

	// 很少变化的信息按 refreshEvery 刷新，减少每轮的文件读取（设计 4.2）
	osInfo    cached[[2]string] // /etc/os-release 的 ID 与 VERSION_ID（系统升级后无需重启即可反映）
	kernel    string            // 内核版本、boot_id 在本次启动内不变，读到一次即可
	bootID    string
	sensors   cached[[]sensorFile]    // 可信 CPU 温度传感器的文件；VPS 上通常为空，此后每轮不再扫描 /sys
	wholeDisk cached[map[string]bool] // /proc/diskstats 中的整块磁盘（/sys/block 下存在的设备）
	cgLimit   cached[cgroupLimit]     // 容器根 cgroup 的内存上限；VM / 物理机为 0，此后每轮不再读取用量（设计 4.5）
	cgLogged  bool                    // 已记录过“按容器 cgroup 计算内存”的日志

	// 扩展指标（设计 4.10）：/proc/stat 与 /proc/meminfo 由 CPU、内存采集项读取后留给这里，不重复读文件
	curStat   statCounters
	prevStat  statCounters
	meminfo   map[string]uint64
	prevVM    map[string]uint64
	prevSnmp  map[string]map[string]uint64
	prevExtra time.Time
	env       cached[*protocol.Environment]
}

// sensorFile 是一个可信 CPU 温度传感器的读数文件。
type sensorFile struct{ name, path string }

// New 返回真实的 Linux 采集器。未指定排除列表时使用 DefaultExclude，
// 跳过虚拟网卡，避免容器 / VPN 流量被重复计算（设计 5.6）。
func New(opts Options) Collector {
	if len(opts.Exclude) == 0 {
		opts.Exclude = DefaultExclude
	}
	return &linux{opts: opts, prevNet: map[string]netCounters{}, prevIO: map[string]ioCounters{},
		bits: counterBits(unameMachine()), statfs: newStatfsProber(statfs)}
}

// readFile 读取失败时返回空字符串。文件缺失是正常情况（精简容器、旧内核、未启用 IPv6），
// 单项缺失应留空该字段，而不是让整份上报失败（设计 43.5）。
//
// /proc 文件的 stat 大小为 0，os.ReadFile 只能从 512 字节起逐步扩容，一个文件要分配多次；
// 这里复用同一块缓冲区读取，每个文件只在转为 string 时分配一次（设计 4.2）。
func readFile(p string) string {
	readMu.Lock()
	defer readMu.Unlock()
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := readBuf[:0]
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)] // 扩容；超大的文件（如上千核的 /proc/stat）也能读完
		}
		n, err := f.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			break
		}
	}
	if cap(buf) <= maxReadBuf {
		readBuf = buf // 保留扩容后的缓冲区供下次使用，但不长期占用异常大的内存
	}
	return string(buf)
}

const maxReadBuf = 256 << 10

var (
	readMu  sync.Mutex
	readBuf = make([]byte, 0, 16<<10)
)

// mustRead 读取采集项必需的文件；失败或为空时记录原因并返回 false。
func mustRead(errs *errorList, item, p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 {
		errs.add(item, "%s 读取失败", p)
		return "", false
	}
	return string(b), true
}

// unameMachine 返回 uname 的 machine（如 x86_64、armv7l）。
func unameMachine() string {
	var u syscall.Utsname
	if syscall.Uname(&u) != nil {
		return ""
	}
	b := make([]byte, 0, len(u.Machine))
	for _, c := range u.Machine { // 各架构上元素类型为 int8 或 uint8，统一转为 byte
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

// statfs 调用 statfs(2)（不需要权限）。
func statfs(path string) (diskStat, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return diskStat{}, err
	}
	return diskStat{Blocks: uint64(st.Blocks), Bfree: uint64(st.Bfree), Bavail: uint64(st.Bavail), Bsize: uint64(st.Bsize),
		Files: uint64(st.Files), Ffree: uint64(st.Ffree)}, nil
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
// 不返回错误：单项失败写入 CollectErrors，整份上报始终可发送（设计 43.5）。
func (c *linux) Collect() (protocol.Report, error) {
	now := time.Now()
	var r protocol.Report
	var errs errorList

	guard(&errs, "system", func() { r.System = c.collectSystem(&errs) })
	var running int
	guard(&errs, "cpu", func() { r.CPU, running = c.collectCPU(&errs) })
	guard(&errs, "memory", func() { r.Memory, r.Swap = c.collectMemory(&errs) })
	guard(&errs, "processes", func() { r.Processes = &protocol.Processes{Total: countProcesses(), Running: running} })
	guard(&errs, "conns", func() {
		tcp4, udp4, tw := parseSockstat(readFile("/proc/net/sockstat"))
		tcp6, udp6, _ := parseSockstat(readFile("/proc/net/sockstat6"))
		r.Conns = &protocol.Conns{TCP: tcp4 + tcp6, UDP: udp4 + udp6, TimeWait: tw}
	})
	guard(&errs, "ports", func() {
		if now.Sub(c.portsAt) >= time.Minute {
			c.ports, c.portsAt = collectListenPorts(), now
		}
		r.Ports = c.ports
	})
	guard(&errs, "disk", func() { r.Disk = c.collectDisks(&errs, readFile("/proc/self/mounts")) })
	guard(&errs, "disk_io", func() { r.DiskIO = c.collectDiskIO(now) })
	guard(&errs, "network", func() { r.Network = c.collectNetwork(&errs, now) })
	if now.Sub(c.prevExtra) >= extraEvery {
		guard(&errs, "extra", func() { r.Extra = c.collectExtra(now) })
	}
	r.System.CounterBits = c.bits
	r.CollectErrors = errs
	return r, nil
}

// collectSystem 采集系统信息。
func (c *linux) collectSystem(errs *errorList) protocol.System {
	var s protocol.System
	s.Hostname, _ = os.Hostname()
	rel := c.osInfo.get(time.Now(), time.Hour, func() [2]string {
		id, ver := parseOSRelease(readFile("/etc/os-release"))
		return [2]string{id, ver}
	})
	s.OS, s.OSVersion = rel[0], rel[1]
	if c.kernel == "" {
		c.kernel = strings.TrimSpace(readFile("/proc/sys/kernel/osrelease"))
	}
	s.Kernel = c.kernel
	s.Arch = runtime.GOARCH
	// boot_id 每次启动都会变化，此时内核网卡计数从 0 重新开始。面板据此把新计数整体计为增量，
	// 启动到首次上报之间的流量不会丢失（设计 5.5，见 server/traffic.go 的 ComputeDelta）。
	// 本次启动内不变（重启后 Agent 进程也是新的），读到一次即可；读不到时流量仍可按计数递增统计，
	// 但无法识别重启，因此记录原因并在下一轮重试。
	if c.bootID == "" {
		if b, ok := mustRead(errs, "system", "/proc/sys/kernel/random/boot_id"); ok {
			c.bootID = strings.TrimSpace(b)
		}
	}
	s.BootID = c.bootID
	if f := strings.Fields(readFile("/proc/uptime")); len(f) > 0 {
		up, _ := strconv.ParseFloat(f[0], 64)
		s.Uptime = uint64(up)
	}
	if c.cpuModel == "" {
		c.cpuModel = parseCPUModel(readFile("/proc/cpuinfo"))
	}
	s.CPUModel = c.cpuModel
	return s
}

// collectCPU 采集 CPU（设计 4.4）。/proc/stat 是开机以来的累计 jiffies，使用率取与上一次采样之间的忙碌占比（首次为 0）。
// 同时返回 procs_running，供进程数使用。
func (c *linux) collectCPU(errs *errorList) (protocol.CPU, int) {
	var cpu protocol.CPU
	if f := strings.Fields(readFile("/proc/loadavg")); len(f) >= 3 {
		cpu.Load1, _ = strconv.ParseFloat(f[0], 64)
		cpu.Load5, _ = strconv.ParseFloat(f[1], 64)
		cpu.Load15, _ = strconv.ParseFloat(f[2], 64)
	}
	raw, ok := mustRead(errs, "cpu", "/proc/stat")
	if !ok {
		return cpu, 0
	}
	cur := parseProcStat(raw)
	c.curStat = parseStatCounters(raw)
	cpu.Usage = cpuUsage(c.prevCPU.all, cur.all)
	cpu.Cores = len(cur.cores)
	cpu.Breakdown = cpuBreakdown(c.prevCPU.all, cur.all)
	cpu.PerCore = perCoreUsage(c.prevCPU.cores, cur.cores)
	cpu.TempC = readCPUTemp(c.sensors.get(time.Now(), refreshEvery, findCPUSensors))
	c.prevCPU = cur
	return cpu, cur.running
}

// collectMemory 采集内存与交换分区（设计 4.5）。
func (c *linux) collectMemory(errs *errorList) (protocol.Memory, protocol.Swap) {
	raw, ok := mustRead(errs, "memory", "/proc/meminfo")
	if !ok {
		return protocol.Memory{}, protocol.Swap{}
	}
	m := parseMeminfo(raw)
	c.meminfo = m
	mem, estimated, ok := memoryFrom(m)
	if !ok {
		errs.add("memory", "/proc/meminfo 缺少 MemTotal")
		return protocol.Memory{}, protocol.Swap{}
	}
	if estimated && !c.memEstimate {
		c.memEstimate = true
		alog.Printf("collect memory: MemAvailable not provided by this kernel, estimating from MemFree + Buffers + Cached")
	}
	if lim := c.cgLimit.get(time.Now(), refreshEvery, readCgroupLimit); lim.limit > 0 {
		inactive, cache := parseCgroupStat(readFile(lim.dir + "/memory.stat"))
		usage, _ := strconv.ParseUint(strings.TrimSpace(readFile(lim.dir+"/"+lim.usageFile)), 10, 64)
		var applied bool
		mem, applied = applyCgroupMemory(mem, cgroupMemory{Limit: lim.limit, Usage: usage, Inactive: inactive, Cache: cache})
		if applied && !c.cgLogged {
			c.cgLogged = true
			alog.Printf("collect memory: container memory limit %d MiB is below /proc/meminfo, using cgroup accounting", lim.limit>>20)
		}
	}
	return mem, swapFrom(m)
}

// cgroupLimit 是容器根 cgroup 的内存上限与读取用量的文件位置。
type cgroupLimit struct {
	limit     uint64
	dir       string // /sys/fs/cgroup（v2）或 /sys/fs/cgroup/memory（v1）
	usageFile string // memory.current（v2）或 memory.usage_in_bytes（v1）
}

// readCgroupLimit 读取当前 cgroup 命名空间根的内存上限（设计 4.5）。容器内看到的根就是容器自身的 cgroup；
// VM 与物理机的根 cgroup 没有 memory.max（v2）或为不限（v1），返回 0。
func readCgroupLimit() cgroupLimit {
	if v := parseCgroupLimit(readFile("/sys/fs/cgroup/memory.max")); v > 0 {
		return cgroupLimit{limit: v, dir: "/sys/fs/cgroup", usageFile: "memory.current"}
	}
	if v := parseCgroupLimit(readFile("/sys/fs/cgroup/memory/memory.limit_in_bytes")); v > 0 {
		return cgroupLimit{limit: v, dir: "/sys/fs/cgroup/memory", usageFile: "memory.usage_in_bytes"}
	}
	return cgroupLimit{}
}

// collectDisks 采集本地块设备文件系统各挂载点的容量（设计 4.6）。读不到挂载表时退回只采集 “/”。
// statfs 并发执行、整体限时；失败或超时的挂载点本轮不上报并记录原因，其余照常（设计 43.5）。
func (c *linux) collectDisks(errs *errorList, mounts string) []protocol.Disk {
	sel := selectMounts(parseMounts(mounts))
	if len(sel) == 0 {
		sel = []mountEntry{{Mount: "/"}}
	}
	paths := make([]string, len(sel))
	for i, m := range sel {
		paths[i] = m.Mount
	}
	stats, failed := c.statfs.statAll(paths)
	var out []protocol.Disk
	for _, m := range sel {
		if reason, bad := failed[m.Mount]; bad {
			errs.add("disk", "%s：%s", m.Mount, reason)
			continue
		}
		if d, ok := diskFromStat(m, stats[m.Mount]); ok {
			out = append(out, d)
		}
	}
	return out
}

// collectDiskIO 采集磁盘 IO（设计 4.7）：只统计整块磁盘（/sys/block 下的设备），分区的 IO 已包含在所属磁盘中。
func (c *linux) collectDiskIO(now time.Time) []protocol.DiskIO {
	ioNow := parseDiskstats(readFile("/proc/diskstats"))
	// 整盘列表缓存：设备增减（热插拔、挂载新盘）时立即刷新，否则每 refreshEvery 一次，不再每轮逐个 stat
	if c.wholeDisk.loaded && !sameKeys(ioNow, c.prevIO) {
		c.wholeDisk.invalidate()
	}
	whole := c.wholeDisk.get(now, refreshEvery, func() map[string]bool { return findWholeDisks(ioNow) })
	elapsed := now.Sub(c.prevIOAt).Seconds()
	var out []protocol.DiskIO
	for name, cur := range ioNow {
		if !whole[name] {
			continue // 分区、虚拟设备或已移除的设备
		}
		d := protocol.DiskIO{Device: name, ReadBytes: cur.readBytes, WriteBytes: cur.writeBytes,
			ReadOps: cur.readOps, WriteOps: cur.writeOps, IOTimeMs: cur.ioTimeMs, InFlight: cur.inFlight}
		// 计数倒退（设备重新挂载）时本轮不计算速率，与网速处理一致
		if p, ok := c.prevIO[name]; ok && elapsed > 0 && cur.readBytes >= p.readBytes && cur.writeBytes >= p.writeBytes {
			d.ReadSpeed = uint64(float64(cur.readBytes-p.readBytes) / elapsed)
			d.WriteSpeed = uint64(float64(cur.writeBytes-p.writeBytes) / elapsed)
			d.ReadIOPS, d.WriteIOPS, d.AwaitMs, d.Util, _ = ioRates(p, cur, elapsed)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	c.prevIO, c.prevIOAt = ioNow, now
	return out
}

// collectNetwork 采集网卡计数与网速。上报原始累计计数，流量统计由面板负责（设计 5.3）；
// 网速只用于展示，取与上一次采样的差值（设计 5.2）。
func (c *linux) collectNetwork(errs *errorList, now time.Time) []protocol.NetIface {
	raw, ok := mustRead(errs, "network", "/proc/net/dev")
	if !ok {
		return nil
	}
	counters := parseNetDev(raw)
	routed := append(parseDefaultRouteIfaces(readFile("/proc/net/route")), parseDefaultRoute6Ifaces(readFile("/proc/net/ipv6_route"))...)
	names, missing, sticky := pickIfaces(c.opts.Interfaces, routed, counters, c.opts.Exclude, c.lastIfaces)
	if len(missing) > 0 {
		errs.add("network", "指定的网卡不存在：%s", strings.Join(missing, ", "))
	}
	if sticky {
		errs.add("network", "未找到默认路由，沿用上次的统计网卡：%s", strings.Join(names, ", "))
	}
	elapsed := now.Sub(c.prevNetAt).Seconds()
	var out []protocol.NetIface
	for _, name := range names {
		cn := counters[name]
		ni := protocol.NetIface{Interface: name, RxBytes: cn.rx, TxBytes: cn.tx,
			RxErrors: cn.rxErrs, TxErrors: cn.txErrs, RxDropped: cn.rxDrop, TxDropped: cn.txDrop}
		// ifindex 让面板识别“同名网卡被重建”：未重启但计数从 0 重新开始（设计 5.5）。
		ni.IfIndex, _ = strconv.Atoi(strings.TrimSpace(readFile("/sys/class/net/" + name + "/ifindex")))
		// 计数回退时：32 位计数器回绕按回绕补算；其他情况（网卡重置）本轮不计算网速，一次 0 比一个巨大的错误尖峰好。
		if p, ok := c.prevNet[name]; ok && elapsed > 0 {
			drx, okRx := protocol.CounterDelta(p.rx, cn.rx, c.bits)
			dtx, okTx := protocol.CounterDelta(p.tx, cn.tx, c.bits)
			if okRx && okTx {
				ni.RxSpeed, ni.TxSpeed = uint64(float64(drx)/elapsed), uint64(float64(dtx)/elapsed)
			}
			ni.RxPPS, ni.TxPPS = rate(p.rxPackets, cn.rxPackets, elapsed), rate(p.txPackets, cn.txPackets, elapsed)
		}
		out = append(out, ni)
	}
	if len(names) > 0 {
		c.lastIfaces = names
	}
	c.prevNet, c.prevNetAt = counters, now
	return out
}

// findWholeDisks 返回 diskstats 中代表真实整块磁盘的设备：/sys/block 下存在（分区不在其中），且不是回环、内存盘等。
func findWholeDisks(stats map[string]ioCounters) map[string]bool {
	out := map[string]bool{}
	for name := range stats {
		if skipIODevice(name) {
			continue
		}
		if _, err := os.Stat("/sys/block/" + name); err == nil {
			out[name] = true
		}
	}
	return out
}

// countProcesses 统计 /proc 下的进程目录数（只读目录名，不读取任何进程的内容）。
// 分批读取目录项，进程很多的主机上也不会一次分配全部名称。
func countProcesses() int {
	f, err := os.Open("/proc")
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	for {
		names, err := f.Readdirnames(256)
		for _, name := range names {
			if name != "" && name[0] >= '0' && name[0] <= '9' {
				n++
			}
		}
		if err != nil || len(names) == 0 {
			return n
		}
	}
}

// findCPUSensors 扫描 hwmon 与 thermal_zone，返回可信 CPU 温度传感器的读数文件（设计 4.4）。
// 由调用方缓存：传感器在运行期间几乎不变，没必要每轮扫描整个目录。
// 【安全】只读 /sys，不需要任何权限。
func findCPUSensors() []sensorFile {
	var out []sensorFile
	hw, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, d := range hw {
		name := strings.TrimSpace(readFile(d + "/name"))
		if !isCPUSensor(name) {
			continue
		}
		inputs, _ := filepath.Glob(d + "/temp*_input")
		for _, in := range inputs {
			out = append(out, sensorFile{name, in})
		}
	}
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*")
	for _, d := range zones {
		if name := strings.TrimSpace(readFile(d + "/type")); isCPUSensor(name) {
			out = append(out, sensorFile{name, d + "/temp"})
		}
	}
	return out
}

// readCPUTemp 读取已发现传感器的温度；没有传感器或都读不到时返回 0，不上报。
func readCPUTemp(sensors []sensorFile) float64 {
	if len(sensors) == 0 {
		return 0
	}
	rs := make([]tempReading, 0, len(sensors))
	for _, sf := range sensors {
		if v, err := strconv.ParseInt(strings.TrimSpace(readFile(sf.path)), 10, 64); err == nil {
			rs = append(rs, tempReading{sf.name, v})
		}
	}
	return pickCPUTemp(rs)
}

// extraEvery：扩展指标每分钟随上报附带一次，速率为这一分钟的平均值。暂不入库也不展示，
// 不必每 10 秒发送；这样每份上报平均只多约 70 字节（压缩后），Agent 自身的流量同样计入用户的套餐。
const extraEvery = time.Minute

// collectExtra 采集扩展指标（设计 4.10）。每一项读不到都只省略该项，不记为失败：
// PSI、oom_kill、conntrack 等取决于内核版本与模块，缺失是正常情况。
func (c *linux) collectExtra(now time.Time) *protocol.Extra {
	elapsed := now.Sub(c.prevExtra).Seconds()
	vm := parseKeyValues(readFile("/proc/vmstat"))
	snmp := parseSnmp(readFile("/proc/net/snmp"))
	x := &protocol.Extra{
		Activity:    activityFrom(c.prevStat, c.curStat, elapsed),
		VM:          vmFrom(c.prevVM, vm, elapsed, uint64(os.Getpagesize())),
		Pressure:    readPressure(),
		MemoryMore:  memoryMoreFrom(c.meminfo),
		Net:         netStackFrom(c.prevSnmp, snmp, elapsed),
		FileHandles: parseFileNr(readFile("/proc/sys/fs/file-nr")),
		Conntrack: parseConntrack(readFile("/proc/sys/net/netfilter/nf_conntrack_count"),
			readFile("/proc/sys/net/netfilter/nf_conntrack_max")),
		Clock: readClockSync(),
		Env:   c.env.get(now, refreshEvery, readEnvironment),
	}
	c.prevStat, c.prevVM, c.prevSnmp, c.prevExtra = c.curStat, vm, snmp, now
	return x
}

// readPressure 读取 PSI（内核 4.20+，且需启用 CONFIG_PSI）；没有时返回 nil。
func readPressure() *protocol.Pressure {
	cs, c60, _, ok := parsePSI(readFile("/proc/pressure/cpu"))
	if !ok {
		return nil
	}
	ms, _, mf, _ := parsePSI(readFile("/proc/pressure/memory"))
	is, _, iof, _ := parsePSI(readFile("/proc/pressure/io"))
	return &protocol.Pressure{CPUSome10: cs, CPUSome60: c60, MemorySome10: ms, MemoryFull10: mf, IOSome10: is, IOFull10: iof}
}

// readClockSync 以只读方式调用 adjtimex（modes 为 0，不需要任何权限）：内核时钟同步状态与最大误差。
// TIME_ERROR（5）或状态中的 STA_UNSYNC 表示未同步；调用被容器的 seccomp 拒绝时返回 nil。
func readClockSync() *protocol.ClockSync {
	var tx syscall.Timex
	state, err := syscall.Adjtimex(&tx)
	if err != nil {
		return nil
	}
	const timeError, staUnsync = 5, 0x0040
	return &protocol.ClockSync{Synced: state != timeError && tx.Status&staUnsync == 0, MaxErrorUs: int64(tx.Maxerror)}
}

// readEnvironment 读取运行环境的线索并判断虚拟化 / 容器类型。DMI 的厂商与型号所有用户可读（序列号等需要 root，不读）。
func readEnvironment() *protocol.Environment {
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	h := envHints{
		openVZ:        exists("/proc/vz") && !exists("/proc/bc"),
		containerFile: strings.TrimSpace(readFile("/run/systemd/container")),
		dockerEnv:     exists("/.dockerenv"),
		podmanEnv:     exists("/run/.containerenv"),
		cgroup:        readFile("/proc/self/cgroup"),
		lxcfs:         strings.Contains(readFile("/proc/self/mountinfo"), "lxcfs /proc/"),
		wsl:           strings.Contains(strings.ToLower(readFile("/proc/sys/kernel/osrelease")), "microsoft"),
		xen:           strings.TrimSpace(readFile("/sys/hypervisor/type")) == "xen",
		vendor:        strings.TrimSpace(readFile("/sys/class/dmi/id/sys_vendor")),
		product:       strings.TrimSpace(readFile("/sys/class/dmi/id/product_name")),
		hypervisorCPU: cpuHasHypervisorFlag(readFile("/proc/cpuinfo")),
	}
	return &protocol.Environment{Virt: detectVirt(h), DMIVendor: h.vendor, DMIProduct: h.product}
}
