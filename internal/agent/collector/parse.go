package collector

// /proc 文件的纯解析函数。没有 build tag，因此在任何系统上都能做单元测试（见 parse_test.go）。

import (
	"bufio"
	"sort"
	"strconv"
	"strings"
)

// cpuTimes 是开机以来累计 CPU jiffies 的快照（所有核心之和）。
type cpuTimes struct{ idle, total uint64 }

// parseProcStat 读取 /proc/stat 中汇总的 “cpu” 行，并统计 “cpuN” 行数作为核心数（设计 4.4）。
// 按行计数反映的是这台 VPS 实际在线的核心数；runtime.NumCPU 可能受 Agent 自身 cgroup / 亲和性限制。
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
				// 字段顺序：user nice system idle iowait irq softirq steal guest guest_nice
				if i >= 8 { // guest 时间已经包含在 user / nice 中，不能重复累加
					break
				}
				t.total += n
				// iowait 计为空闲：CPU 本身空闲，只是在等磁盘。steal（i == 7）计为忙碌，
				// 宿主机超售时会表现为使用率偏高，这正是 VPS 用户需要看到的。
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

// cpuUsage 返回两次采样之间的忙碌百分比（0～100）。
// 首次采样（没有 prev）或计数倒退时返回 0，而不是一个无意义的值。
func cpuUsage(prev, cur cpuTimes) float64 {
	dt := cur.total - prev.total
	if prev.total == 0 || dt == 0 || cur.total < prev.total {
		return 0
	}
	di := cur.idle - prev.idle
	return float64(dt-di) * 100 / float64(dt)
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
		out[f[2]] = ioCounters{readOps: u(3), readBytes: u(5) * 512, writeOps: u(7), writeBytes: u(9) * 512, ioTimeMs: u(12)}
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
