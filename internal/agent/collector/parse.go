package collector

// /proc 文件的纯解析函数。没有 build tag，因此在任何系统上都能做单元测试（见 parse_test.go）。

import (
	"bufio"
	"math"
	"sort"
	"strconv"
	"strings"

	"vpsmon/internal/protocol"
)

// cpuTimes 是一个 CPU 行开机以来累计的 jiffies：user nice system idle iowait irq softirq steal。
// guest / guest_nice 已包含在 user / nice 中，不再单独保存，避免重复累加。
type cpuTimes [8]uint64

func (t cpuTimes) total() (n uint64) {
	for _, v := range t {
		n += v
	}
	return
}

// idle 把 iowait 计为空闲：CPU 本身空闲，只是在等磁盘。steal 计为忙碌，
// 宿主机超售时会表现为使用率偏高，这正是 VPS 用户需要看到的。
func (t cpuTimes) idle() uint64 { return t[3] + t[4] }

// procStat 是一次 /proc/stat 快照。
type procStat struct {
	all     cpuTimes   // 汇总的 “cpu” 行
	cores   []cpuTimes // “cpuN” 行，按出现顺序
	running int        // procs_running：正在运行或可运行的任务数
}

// parseProcStat 解析 /proc/stat（设计 4.4、4.9）。核心数按 “cpuN” 行计数，反映这台 VPS 实际在线的核心数；
// runtime.NumCPU 可能受 Agent 自身 cgroup / 亲和性限制。
func parseProcStat(s string) procStat {
	var ps procStat
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		switch {
		case f[0] == "cpu":
			ps.all = parseCPULine(f[1:])
		case strings.HasPrefix(f[0], "cpu"):
			ps.cores = append(ps.cores, parseCPULine(f[1:]))
		case f[0] == "procs_running" && len(f) > 1:
			ps.running, _ = strconv.Atoi(f[1])
		}
	}
	return ps
}

func parseCPULine(f []string) cpuTimes {
	var t cpuTimes
	for i := 0; i < len(f) && i < len(t); i++ {
		t[i], _ = strconv.ParseUint(f[i], 10, 64)
	}
	return t
}

// cpuUsage 返回两次采样之间的忙碌百分比（0～100）。
// 首次采样（没有 prev）或计数倒退时返回 0，而不是一个无意义的值。
func cpuUsage(prev, cur cpuTimes) float64 {
	pt, ct := prev.total(), cur.total()
	if pt == 0 || ct <= pt || cur.idle() < prev.idle() {
		return 0
	}
	dt := ct - pt
	return float64(dt-(cur.idle()-prev.idle())) * 100 / float64(dt)
}

// cpuBreakdown 返回两次采样之间各类时间的占比；首次采样或计数倒退时返回 nil（设计 4.4）。
func cpuBreakdown(prev, cur cpuTimes) *protocol.CPUBreakdown {
	pt, ct := prev.total(), cur.total()
	if pt == 0 || ct <= pt {
		return nil
	}
	var p [8]float64
	for i := range cur {
		if cur[i] < prev[i] {
			return nil
		}
		p[i] = float64(cur[i]-prev[i]) * 100 / float64(ct-pt)
	}
	r := func(v float64) float64 { return math.Round(v*10) / 10 }
	return &protocol.CPUBreakdown{User: r(p[0]), Nice: r(p[1]), System: r(p[2]), Idle: r(p[3]),
		IOWait: r(p[4]), IRQ: r(p[5]), SoftIRQ: r(p[6]), Steal: r(p[7])}
}

// perCoreUsage 返回每个核心的使用率；核心数变化（CPU 热插拔）时本轮不返回。
func perCoreUsage(prev, cur []cpuTimes) []float64 {
	if len(prev) != len(cur) || len(cur) == 0 {
		return nil
	}
	out := make([]float64, len(cur))
	for i := range cur {
		out[i] = math.Round(cpuUsage(prev[i], cur[i])*10) / 10
	}
	return out
}

// parseCPUModel 从 /proc/cpuinfo 取 CPU 型号：x86 为 model name；部分 ARM 内核只有 Hardware 或 Processor。
func parseCPUModel(s string) string {
	var hw string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Join(strings.Fields(v), " ")
		switch k {
		case "model name":
			return v
		case "Hardware", "Processor":
			if hw == "" {
				hw = v
			}
		}
	}
	return hw
}

// parseSockstat 解析 /proc/net/sockstat 或 sockstat6，返回 TCP、UDP 的 inuse 与 TCP 的 tw（设计 4.9）。
//
//	TCP: inuse 5 orphan 0 tw 3 alloc 7 mem 1
//	UDP6: inuse 1
func parseSockstat(s string) (tcp, udp, tw int) {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		field := func(name string) int {
			for i := 1; i+1 < len(f); i += 2 {
				if f[i] == name {
					n, _ := strconv.Atoi(f[i+1])
					return n
				}
			}
			return 0
		}
		switch f[0] {
		case "TCP:", "TCP6:":
			tcp += field("inuse")
			tw += field("tw")
		case "UDP:", "UDP6:":
			udp += field("inuse")
		}
	}
	return
}

// tempReading 是一个温度传感器读数。
type tempReading struct {
	name  string // hwmon 的 name 或 thermal_zone 的 type
	milli int64  // 千分之一摄氏度，与 sysfs 一致
}

// cpuSensors 是可信的 CPU 温度传感器。acpitz 等主板 / 虚拟化传感器在 VPS 上常为固定假值，不采用。
var cpuSensors = []string{"coretemp", "k10temp", "zenpower", "x86_pkg_temp", "cpu_thermal", "cpu-thermal", "soc_thermal", "soc-thermal"}

// isCPUSensor 判断传感器名称是否为可信的 CPU 温度传感器。
func isCPUSensor(name string) bool {
	for _, n := range cpuSensors {
		if strings.EqualFold(name, n) {
			return true
		}
	}
	return false
}

// pickCPUTemp 取可信 CPU 传感器的最高温度（摄氏度，保留一位小数）；没有时返回 0，表示不上报。
func pickCPUTemp(rs []tempReading) float64 {
	var best int64
	for _, r := range rs {
		// 超出 0～150 ℃ 的读数视为传感器异常
		if isCPUSensor(r.name) && r.milli > 0 && r.milli < 150_000 && r.milli > best {
			best = r.milli
		}
	}
	return math.Round(float64(best)/100) / 10
}

// parseMeminfo 返回 /proc/meminfo 各字段的值，单位为字节，键为字段名（MemTotal、MemAvailable …）。
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
		// 内核写的是 “kB”，实际含义是 KiB（1024）。没有单位的行（如 HugePages_Total）是个数。
		if len(f) > 1 && f[1] == "kB" {
			n *= 1024
		}
		m[k] = n
	}
	return m
}

// memoryFrom 由 /proc/meminfo 计算内存（设计 4.5），没有 MemTotal 时返回 false。
//
// 已用 = MemTotal − MemAvailable，而不是 MemTotal − MemFree：页缓存可以回收，算作已用会让长期运行的 VPS 看起来接近 100%。
// MemAvailable 从内核 3.14 开始提供；更旧的内核与部分 OpenVZ 容器没有它，按 MemFree + Buffers + Cached + SReclaimable 估算，
// 否则会显示 100% 并误触发内存告警。LXC（lxcfs）中偶见 MemAvailable > MemTotal，截断为 MemTotal，避免无符号下溢。
func memoryFrom(m map[string]uint64) (mem protocol.Memory, estimated, ok bool) {
	total := m["MemTotal"]
	if total == 0 {
		return protocol.Memory{}, false, false
	}
	cached := m["Cached"] + m["SReclaimable"]
	avail, has := m["MemAvailable"]
	if !has {
		avail, estimated = m["MemFree"]+m["Buffers"]+cached, true
	}
	avail = min(avail, total)
	return protocol.Memory{Total: total, Available: avail, Used: total - avail, Usage: pct(total-avail, total),
		Free: m["MemFree"], Buffers: m["Buffers"], Cached: cached}, estimated, true
}

// swapFrom 由 /proc/meminfo 计算交换分区；SwapFree > SwapTotal（部分容器）时已用记为 0。
func swapFrom(m map[string]uint64) protocol.Swap {
	total := m["SwapTotal"]
	return protocol.Swap{Total: total, Used: total - min(m["SwapFree"], total)}
}

// counterBits 由 uname 的 machine 判断内核网卡计数器的位数（设计 5.5）；不认识时返回 0（未知）。
//
// 32 位内核上，只维护 unsigned long 统计的驱动在约 4 GiB 处回绕；64 位内核不会。
// 32 位 Agent 运行在 64 位内核上时，arm64 返回 armv8l、x86_64 仍返回 x86_64，都按 64 位处理。
func counterBits(machine string) int {
	switch {
	case machine == "":
		return 0
	case machine == "x86_64", machine == "aarch64", machine == "arm64", machine == "armv8l", machine == "aarch64_be",
		machine == "riscv64", machine == "ppc64", machine == "ppc64le", machine == "s390x",
		machine == "mips64", machine == "loongarch64", machine == "sparc64":
		return 64
	case len(machine) == 4 && machine[0] == 'i' && machine[2:] == "86", // i386 … i686
		strings.HasPrefix(machine, "armv"), machine == "arm", machine == "riscv32", machine == "mips", machine == "mipsel",
		machine == "ppc", machine == "s390":
		return 32
	}
	return 0
}

// netCounters 是一块网卡的累计收发字节数。
type netCounters struct{ rx, tx uint64 }

// parseNetDev 把 /proc/net/dev 解析为各网卡的累计字节数。
func parseNetDev(s string) map[string]netCounters {
	out := map[string]netCounters{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue // 表头行
		}
		// 先是 8 个接收字段，再是 8 个发送字段，每组第一个都是字节数，
		// 所以接收字节为 f[0]，发送字节为 f[8]。
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

// parseDefaultRouteIfaces 返回持有 IPv4 默认路由的网卡（/proc/net/route）。
func parseDefaultRouteIfaces(s string) []string {
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		// 列：Iface Destination(十六进制) Gateway …；目的地址 0.0.0.0 即默认路由。
		// 同一网卡可能有多条默认路由（不同 metric），因此去重。
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[1] == "00000000" && !seen[f[0]] {
			seen[f[0]] = true
			out = append(out, f[0])
		}
	}
	return out
}

// parseDefaultRoute6Ifaces 返回持有 IPv6 默认路由的网卡（/proc/net/ipv6_route）。
func parseDefaultRoute6Ifaces(s string) []string {
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// 列：目的地址（32 位十六进制）、前缀长度 … 网卡名（最后一列）
		if len(f) >= 10 && f[0] == strings.Repeat("0", 32) && f[1] == "00" {
			name := f[len(f)-1]
			// 内核会在 lo 上列出一条不可达的 ::/0 路由，它不承载真实流量。
			if name != "lo" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// parseOSRelease 返回 /etc/os-release 中的 ID 与 VERSION_ID（如 “ubuntu”、“24.04”）。
// ID 稳定、便于程序处理，不像 PRETTY_NAME；展示格式由界面负责。
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

// mountEntry 是 /proc/self/mounts 中的一行。
type mountEntry struct {
	Device, Mount, FSType string
}

// diskFSTypes 是会采集容量的本地块设备文件系统（设计 4.6）。
//
// 用白名单而不是黑名单：tmpfs、overlay、proc、cgroup、squashfs（snap）等虚拟文件系统自然被排除；
// 更重要的是排除 NFS / CIFS 等网络文件系统——服务端失联时 statfs 会长时间阻塞，拖住整个上报。
var diskFSTypes = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true, "f2fs": true,
	"vfat": true, "exfat": true, "ntfs": true, "ntfs3": true, "jfs": true, "reiserfs": true, "bcachefs": true,
}

// maxMounts：最多上报的挂载点数量，防止异常主机（大量绑定挂载）撑大上报体。
const maxMounts = 16

// parseMounts 解析 /proc/self/mounts。挂载路径中的空格等字符被内核转义为 \040 形式，这里还原。
func parseMounts(s string) []mountEntry {
	var out []mountEntry
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		out = append(out, mountEntry{Device: unescapeMount(f[0]), Mount: unescapeMount(f[1]), FSType: f[2]})
	}
	return out
}

// unescapeMount 还原 \040（空格）、\011（制表符）、\012（换行）、\134（反斜杠）等八进制转义。
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// selectMounts 选出要采集容量的挂载点：白名单文件系统，同一设备只取一个（Docker 等的绑定挂载会让
// 同一块盘出现多次，重复计算会误导），“/” 排在最前，其余按路径排序，最多 maxMounts 个。
func selectMounts(entries []mountEntry) []mountEntry {
	byDevice := map[string]mountEntry{}
	for _, e := range entries {
		if !diskFSTypes[e.FSType] {
			continue
		}
		prev, ok := byDevice[e.Device]
		// 同一设备保留路径最短的挂载点（通常是原始挂载，而不是绑定挂载）
		if !ok || len(e.Mount) < len(prev.Mount) {
			byDevice[e.Device] = e
		}
	}
	out := make([]mountEntry, 0, len(byDevice))
	for _, e := range byDevice {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Mount == "/") != (out[j].Mount == "/") {
			return out[i].Mount == "/"
		}
		return out[i].Mount < out[j].Mount
	})
	if len(out) > maxMounts {
		out = out[:maxMounts]
	}
	return out
}

// ioCounters 是一块磁盘的累计 IO 计数。
type ioCounters struct {
	readBytes, writeBytes, readOps, writeOps, ioTimeMs uint64
	readMs, writeMs                                    uint64 // 读 / 写请求累计耗时（含排队），用于平均耗时
}

// parseDiskstats 解析 /proc/diskstats（设计 4.7）。
// 列：major minor name reads merged sectors_read ms_read writes merged sectors_written ms_write in_flight io_ms …
// 扇区固定按 512 字节计（内核文档 iostats.rst），与设备实际扇区大小无关。
func parseDiskstats(s string) map[string]ioCounters {
	out := map[string]ioCounters{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 13 {
			continue
		}
		u := func(i int) uint64 { n, _ := strconv.ParseUint(f[i], 10, 64); return n }
		out[f[2]] = ioCounters{readOps: u(3), readBytes: u(5) * 512, readMs: u(6), writeOps: u(7), writeBytes: u(9) * 512,
			writeMs: u(10), ioTimeMs: u(12)}
	}
	return out
}

// skipIODevice 排除不代表真实磁盘的设备：回环、内存盘、压缩内存交换、光驱、软驱、网络块设备。
func skipIODevice(name string) bool {
	for _, p := range []string{"loop", "ram", "zram", "sr", "fd", "nbd"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ioRates 计算两次采样之间的 IOPS、平均耗时与忙碌占比（设计 4.7）；计数倒退或间隔无效时返回 false。
func ioRates(prev, cur ioCounters, elapsed float64) (readIOPS, writeIOPS, awaitMs, util float64, ok bool) {
	if elapsed <= 0 || cur.readOps < prev.readOps || cur.writeOps < prev.writeOps ||
		cur.readMs < prev.readMs || cur.writeMs < prev.writeMs || cur.ioTimeMs < prev.ioTimeMs {
		return 0, 0, 0, 0, false
	}
	r2 := func(v float64) float64 { return math.Round(v*100) / 100 }
	dr, dw := cur.readOps-prev.readOps, cur.writeOps-prev.writeOps
	readIOPS, writeIOPS = r2(float64(dr)/elapsed), r2(float64(dw)/elapsed)
	if dr+dw > 0 {
		awaitMs = r2(float64(cur.readMs-prev.readMs+cur.writeMs-prev.writeMs) / float64(dr+dw))
	}
	util = math.Min(100, r2(float64(cur.ioTimeMs-prev.ioTimeMs)/(elapsed*10)))
	return readIOPS, writeIOPS, awaitMs, util, true
}
